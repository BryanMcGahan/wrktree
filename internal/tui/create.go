package tui

import (
	"fmt"
	"strings"

	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/worktree"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

type suggestKind int

const (
	sugNew suggestKind = iota
	sugLocal
	sugRemote
	sugPR
)

type suggestion struct {
	kind  suggestKind
	value string // branch name / remote ref
	pr    worktree.PR
}

func (s suggestion) label() string {
	switch s.kind {
	case sugLocal:
		return s.value
	case sugRemote:
		return s.value
	case sugPR:
		return fmt.Sprintf("#%d %s", s.pr.Number, s.pr.Title)
	}
	return s.value
}

func (s suggestion) tag() string {
	switch s.kind {
	case sugLocal:
		return "local branch"
	case sugRemote:
		return "remote branch"
	case sugPR:
		return "PR · " + s.pr.Head + " · @" + s.pr.Author.Login
	}
	return ""
}

type (
	suggestionsMsg struct{ s []suggestion }
	prsMsg         struct {
		s   []suggestion
		err error
	}
	createErrMsg struct{ err error }
	createOKMsg  struct{ res *worktree.CreateResult }
)

type create struct {
	e        *env
	branch   textinput.Model
	from     textinput.Model
	focus    int // 0 branch, 1 from
	all      []suggestion
	rows     []suggestion
	cur      int
	busy     bool
	fetching bool
	spin     spinner.Model
	err      error
}

func newCreate(e *env) *create {
	b := textinput.New()
	b.Prompt = ""
	b.Placeholder = "branch name, remote branch or #PR"
	b.Focus()
	f := textinput.New()
	f.Prompt = ""
	f.Placeholder = e.m.BaseBranch()
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = sAccent
	return &create{e: e, branch: b, from: f, spin: sp, fetching: true}
}

func (c *create) Init() tea.Cmd {
	m := c.e.m
	load := func() tea.Msg {
		dir := m.Dir()
		used := map[string]bool{}
		if wts, err := m.List(); err == nil {
			for _, w := range wts {
				used[w.Branch] = true
			}
		}
		var s []suggestion
		local := map[string]bool{}
		for _, b := range git.LocalBranches(dir) {
			local[b] = true
			if !used[b] {
				s = append(s, suggestion{kind: sugLocal, value: b})
			}
		}
		for _, r := range git.RemoteBranches(dir) {
			if _, b, ok := git.SplitRemoteBranch(dir, r); ok && !local[b] {
				s = append(s, suggestion{kind: sugRemote, value: r})
			}
		}
		return suggestionsMsg{s}
	}
	prs := func() tea.Msg {
		if !worktree.HasGH() {
			return prsMsg{}
		}
		list, err := worktree.ListPRs(m.Dir())
		var s []suggestion
		for _, p := range list {
			s = append(s, suggestion{kind: sugPR, pr: p})
		}
		return prsMsg{s: s, err: err}
	}
	return tea.Batch(textinput.Blink, c.spin.Tick, load, prs)
}

func (c *create) typed() string { return strings.TrimSpace(c.branch.Value()) }

func (c *create) refilter() {
	q := c.typed()
	c.rows = c.rows[:0]
	exact := false
	for _, s := range c.all {
		if s.kind == sugLocal && s.value == q {
			exact = true
		}
	}
	if q != "" && !exact && !strings.HasPrefix(q, "#") {
		c.rows = append(c.rows, suggestion{kind: sugNew, value: q})
	}
	if q == "" {
		c.rows = append(c.rows, c.all...)
	} else {
		keys := make([]string, len(c.all))
		for i, s := range c.all {
			keys[i] = s.label()
			if s.kind == sugPR {
				keys[i] = fmt.Sprintf("#%d %s %s", s.pr.Number, s.pr.Title, s.pr.Head)
			}
		}
		for _, mt := range fuzzy.Find(q, keys) {
			c.rows = append(c.rows, c.all[mt.Index])
		}
	}
	if c.cur >= len(c.rows) {
		c.cur = len(c.rows) - 1
	}
	if c.cur < 0 {
		c.cur = 0
	}
}

func (c *create) submit() tea.Cmd {
	opts := worktree.CreateOptions{}
	q := c.typed()
	if len(c.rows) > 0 {
		s := c.rows[c.cur]
		switch s.kind {
		case sugNew:
			opts.Branch = s.value
			if f := strings.TrimSpace(c.from.Value()); f != "" {
				opts.From = f
			}
		case sugLocal, sugRemote:
			opts.Branch = s.value
		case sugPR:
			opts.PR = s.pr.Number
			opts.Branch = s.pr.Head
		}
	} else if strings.HasPrefix(q, "#") {
		var n int
		if _, err := fmt.Sscanf(q[1:], "%d", &n); err != nil || n <= 0 {
			c.err = fmt.Errorf("invalid PR number %q", q)
			return nil
		}
		opts.PR = n
	} else {
		return nil
	}
	if opts.PR > 0 && opts.Branch != "" && git.LocalBranchExists(c.e.m.Dir(), opts.Branch) {
		opts.PR = 0 // branch already fetched; just check it out
	}
	c.busy = true
	c.err = nil
	m := c.e.m
	return tea.Batch(c.spin.Tick, func() tea.Msg {
		res, err := m.Create(opts)
		if err != nil {
			return createErrMsg{err}
		}
		return createOKMsg{res}
	})
}

func (c *create) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case suggestionsMsg:
		c.all = append(msg.s, c.all...)
		c.refilter()
		return c, nil
	case prsMsg:
		c.fetching = false
		c.all = append(c.all, msg.s...)
		c.refilter()
		return c, nil
	case createErrMsg:
		c.busy = false
		c.err = msg.err
		return c, nil
	case createOKMsg:
		return c, pop(createdMsg{msg.res})
	case spinner.TickMsg:
		if !c.busy && !c.fetching {
			return c, nil
		}
		var cmd tea.Cmd
		c.spin, cmd = c.spin.Update(msg)
		return c, cmd
	case tea.KeyMsg:
		if c.busy {
			return c, nil
		}
		switch msg.String() {
		case "esc":
			return c, pop(nil)
		case "enter":
			return c, c.submit()
		case "tab", "shift+tab":
			if c.focus == 0 {
				c.focus = 1
				c.branch.Blur()
				return c, c.from.Focus()
			}
			c.focus = 0
			c.from.Blur()
			return c, c.branch.Focus()
		case "up", "ctrl+p":
			if c.cur > 0 {
				c.cur--
			}
			return c, nil
		case "down", "ctrl+n":
			if c.cur < len(c.rows)-1 {
				c.cur++
			}
			return c, nil
		}
	}
	var cmd tea.Cmd
	if c.focus == 0 {
		old := c.branch.Value()
		c.branch, cmd = c.branch.Update(msg)
		if c.branch.Value() != old {
			c.cur = 0
			c.refilter()
		}
	} else {
		c.from, cmd = c.from.Update(msg)
	}
	return c, cmd
}

func (c *create) View() string {
	e := c.e
	w := e.width
	right := ""
	switch {
	case c.busy:
		right = c.spin.View() + " creating"
	case c.fetching:
		right = c.spin.View() + " loading PRs"
	}
	header := headerBar(w, "New worktree", e.m.Repo.Name, right)

	label := func(s string, on bool) string {
		if on {
			return sAccent.Bold(true).Render(pad(s, 8))
		}
		return sDim.Render(pad(s, 8))
	}
	path := sDim.Render("—")
	if len(c.rows) > 0 {
		s := c.rows[c.cur]
		name := s.value
		switch s.kind {
		case sugRemote:
			if _, b, ok := git.SplitRemoteBranch(e.m.Dir(), s.value); ok {
				name = b
			}
		case sugPR:
			name = s.pr.Head
		}
		if name != "" {
			path = shortPath(e.m.PathFor(name))
		}
	}
	form := label("Branch", c.focus == 0) + c.branch.View() + "\n" +
		label("From", c.focus == 1) + c.from.View() + sDim.Render("   new branches only · tab to edit") + "\n" +
		label("Path", false) + path
	var b strings.Builder
	b.WriteString("\n" + indent(panel("Branch", form, w-2, 5, true), 1) + "\n\n")

	var msg string
	switch {
	case c.busy:
		msg = c.spin.View() + " creating worktree…"
	case c.err != nil:
		msg = sErr.Render("✗ " + errText(c.err))
	}
	b.WriteString(" " + section(fmt.Sprintf("Choose  %d", len(c.rows)), w-2) + "\n")

	listH := e.height - 1 - 1 - 2 - 9
	if msg != "" {
		listH -= 2
	}
	start := 0
	if c.cur >= listH {
		start = c.cur - listH + 1
	}
	tagW := 0
	for _, s := range c.rows {
		tagW = max(tagW, lipgloss.Width(s.tag()))
	}
	tagW = min(max(tagW, 14), w/3)
	textW := w - tagW - 8
	for i := start; i < len(c.rows) && i < start+listH; i++ {
		s := c.rows[i]
		var icon, text, tag string
		if s.kind == sugNew {
			from := strings.TrimSpace(c.from.Value())
			if from == "" {
				from = e.m.BaseBranch()
			}
			icon, text, tag = "+", "create new branch "+s.value, "from "+from
		} else {
			icon, text, tag = kindIcon(s.kind), s.label(), s.tag()
		}
		if i == c.cur {
			b.WriteString(" " + selRow("▸ "+icon+" "+pad(text, textW)+" "+tag, w-2) + "\n")
		} else {
			b.WriteString("   " + sDim.Render(icon) + " " + pad(text, textW) + " " + sDim.Render(truncate(tag, tagW)) + "\n")
		}
	}
	if len(c.rows) == 0 && c.typed() == "" {
		b.WriteString(sDim.Render("   type a name to create a branch, or pick an existing one") + "\n")
	}
	if msg != "" {
		b.WriteString("\n " + msg + "\n")
	}
	return frame(e, header, b.String(), help("enter", "create", "↑/↓", "choose", "tab", "edit start point", "esc", "cancel"))
}

func kindIcon(k suggestKind) string {
	switch k {
	case sugLocal:
		return "⎇"
	case sugRemote:
		return "☁"
	case sugPR:
		return "⇄"
	}
	return "+"
}
