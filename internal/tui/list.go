package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/merge"
	"github.com/BryanMcGahan/wrktree/internal/worktree"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type (
	wtsLoadedMsg struct {
		wts     []git.Worktree
		err     error
		pending *merge.State
		sel     string
	}
	wtStatusMsg struct {
		path string
		st   git.Status
	}
	fetchDoneMsg struct{ err error }
)

type list struct {
	e         *env
	wts       []git.Worktree
	statuses  map[string]git.Status
	view      []git.Worktree
	cur       int
	filter    textinput.Model
	filtering bool
	loading   bool
	fetching  bool
	showHelp  bool
	pending   *merge.State
	err       error
	spin      spinner.Model
	selectOn  string
	base      string
}

func newList(e *env) *list {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "search worktrees"
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = sAccent
	return &list{e: e, filter: ti, statuses: map[string]git.Status{}, loading: true, spin: sp, base: e.m.BaseBranch()}
}

func (l *list) Init() tea.Cmd { return tea.Batch(l.spin.Tick, l.load("")) }

func (l *list) load(sel string) tea.Cmd {
	m := l.e.m
	return func() tea.Msg {
		wts, err := m.List()
		return wtsLoadedMsg{wts: wts, err: err, pending: merge.Pending(m), sel: sel}
	}
}

func statusCmd(path string) tea.Cmd {
	return func() tea.Msg { return wtStatusMsg{path: path, st: git.GetStatus(path)} }
}

func (l *list) applyFilter() {
	l.view = worktree.Search(l.wts, l.filter.Value())
	if l.cur >= len(l.view) {
		l.cur = len(l.view) - 1
	}
	if l.cur < 0 {
		l.cur = 0
	}
}

func (l *list) selected() *git.Worktree {
	if l.cur < len(l.view) {
		return &l.view[l.cur]
	}
	return nil
}

func (l *list) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case wtsLoadedMsg:
		l.loading = false
		l.err = msg.err
		l.pending = msg.pending
		prev := ""
		if w := l.selected(); w != nil {
			prev = w.Path
		}
		if msg.sel != "" {
			prev = msg.sel
		}
		l.wts = msg.wts
		l.applyFilter()
		for i, w := range l.view {
			if w.Path == prev {
				l.cur = i
			}
		}
		var cmds []tea.Cmd
		for _, w := range l.wts {
			if !w.Bare && !w.Prunable {
				cmds = append(cmds, statusCmd(w.Path))
			}
		}
		return l, tea.Batch(cmds...)
	case wtStatusMsg:
		l.statuses[msg.path] = msg.st
		return l, nil
	case refreshMsg:
		l.base = l.e.m.BaseBranch()
		return l, l.load(msg.selectPath)
	case fetchDoneMsg:
		l.fetching = false
		if msg.err != nil {
			return l, statusErr(msg.err)
		}
		return l, tea.Batch(status("Fetched"), l.load(""))
	case createdMsg:
		return l, tea.Batch(l.load(msg.res.Path), status("Created "+msg.res.Branch+" ("+msg.res.How+")"), postCreateHooks(l.e, msg.res.Path))
	case removedMsg:
		return l, l.onRemoved(msg)
	case hooksDoneMsg:
		if msg.err != nil {
			return l, statusErr(fmt.Errorf("%s hooks failed: %w", msg.what, msg.err))
		}
		if msg.then != nil {
			return l, msg.then
		}
		return l, status(msg.what + " hooks finished")
	case spinner.TickMsg:
		if !l.loading && !l.fetching {
			return l, nil
		}
		var cmd tea.Cmd
		l.spin, cmd = l.spin.Update(msg)
		return l, cmd
	case tea.KeyMsg:
		if l.filtering {
			return l.updateFilter(msg)
		}
		return l.updateKeys(msg)
	}
	return l, nil
}

func (l *list) updateFilter(k tea.KeyMsg) (screen, tea.Cmd) {
	switch k.String() {
	case "esc":
		l.filtering = false
		l.filter.Blur()
		l.filter.SetValue("")
		l.applyFilter()
		return l, nil
	case "enter":
		l.filtering = false
		l.filter.Blur()
		if len(l.view) == 1 {
			return l, msgCmd(quitMsg{cd: l.view[0].Path})
		}
		return l, nil
	case "up", "ctrl+p":
		if l.cur > 0 {
			l.cur--
		}
		return l, nil
	case "down", "ctrl+n":
		if l.cur < len(l.view)-1 {
			l.cur++
		}
		return l, nil
	}
	var cmd tea.Cmd
	l.filter, cmd = l.filter.Update(k)
	l.cur = 0
	l.applyFilter()
	return l, cmd
}

func (l *list) updateKeys(k tea.KeyMsg) (screen, tea.Cmd) {
	w := l.selected()
	if l.showHelp {
		l.showHelp = false
		return l, nil
	}
	switch k.String() {
	case "q", "esc":
		if l.filter.Value() != "" {
			l.filter.SetValue("")
			l.applyFilter()
			return l, nil
		}
		return l, tea.Quit
	case "?":
		l.showHelp = true
	case "up", "k":
		if l.cur > 0 {
			l.cur--
		}
	case "down", "j":
		if l.cur < len(l.view)-1 {
			l.cur++
		}
	case "g", "home":
		l.cur = 0
	case "G", "end":
		l.cur = len(l.view) - 1
	case "/":
		l.filtering = true
		return l, l.filter.Focus()
	case "enter":
		if w != nil && !w.Bare {
			return l, msgCmd(quitMsg{cd: w.Path})
		}
	case "n":
		return l, push(newCreate(l.e))
	case "d", "x":
		if w != nil {
			return l, deleteFlow(l.e, *w)
		}
	case "o":
		if w != nil && !w.Bare {
			return l, openEditor(l.e, w.Path)
		}
	case "c":
		if w != nil && !w.Bare {
			return l, push(newCommit(l.e, w.Path, false))
		}
	case "m":
		if l.pending != nil {
			return l, push(newMergeResume(l.e))
		}
		if w != nil && w.Branch != "" {
			return l, push(newMerge(l.e, *w))
		}
		return l, status("select a worktree with a branch to merge")
	case "M":
		if l.pending != nil {
			return l, push(newMergeResume(l.e))
		}
	case "P":
		if w != nil && w.Branch != "" {
			return l, push(newPR(l.e, *w))
		}
	case "p":
		return l, push(newPrune(l.e))
	case "r":
		l.statuses = map[string]git.Status{}
		return l, l.load("")
	case "f":
		l.fetching = true
		dir := l.e.m.Dir()
		return l, tea.Batch(l.spin.Tick, func() tea.Msg {
			_, err := git.Run(dir, "fetch", "--all", "--prune")
			return fetchDoneMsg{err}
		})
	}
	return l, nil
}

func (l *list) View() string {
	e := l.e
	w := e.width
	var b strings.Builder

	head := sTitle.Render("wrk") + sDim.Render(" · ") + sBold.Render(e.m.Repo.Name) +
		sDim.Render(fmt.Sprintf(" · %d worktrees · base ", len(l.wts))) + sCyan.Render(l.base)
	if l.loading || l.fetching {
		head += "  " + l.spin.View()
	}
	b.WriteString(" " + head + "\n")
	if l.filtering || l.filter.Value() != "" {
		b.WriteString(" " + l.filter.View() + "\n")
	} else {
		b.WriteString("\n")
	}
	if l.pending != nil {
		b.WriteString(" " + sYellow.Render(fmt.Sprintf("⚠ merge of %s into %s stopped on conflicts — press M to resolve", l.pending.Source, l.pending.Target)) + "\n")
	}
	if l.err != nil {
		b.WriteString(" " + sErr.Render(errText(l.err)) + "\n")
	}
	if l.showHelp {
		b.WriteString(l.helpView())
		return b.String()
	}

	nameW := 0
	for _, wt := range l.view {
		if n := lipgloss.Width(wt.Name()); n > nameW {
			nameW = n
		}
	}
	nameW += 2
	if nameW > w/3 {
		nameW = w / 3
	}
	if nameW < 12 {
		nameW = 12
	}
	listH := e.height - 8
	if l.pending != nil {
		listH--
	}
	start := 0
	if l.cur >= listH {
		start = l.cur - listH + 1
	}
	if len(l.view) == 0 && !l.loading {
		b.WriteString(sDim.Render("   no worktrees match") + "\n")
	}
	for i := start; i < len(l.view) && i < start+listH; i++ {
		b.WriteString(l.row(l.view[i], i == l.cur, nameW, w) + "\n")
	}
	used := lipgloss.Height(b.String())
	if n := e.height - 3 - used; n > 0 {
		b.WriteString(strings.Repeat("\n", n))
	}
	b.WriteString(l.details(w) + "\n")
	b.WriteString(" " + help("enter", "cd", "/", "search", "n", "new", "d", "delete", "c", "commit", "m", "merge", "P", "PR", "o", "open", "?", "more"))
	return b.String()
}

func (l *list) row(wt git.Worktree, sel bool, nameW, width int) string {
	st, have := l.statuses[wt.Path]
	marker := "  "
	if wt.Path == l.e.m.Repo.Current {
		marker = sAccent.Render("● ")
	}
	name := pad(wt.Name(), nameW)
	var stat, ab, age, subj string
	switch {
	case wt.Bare:
		stat = sDim.Render("bare")
	case wt.Prunable:
		stat = sRed.Render("missing")
	case !have:
		stat = sDim.Render("…")
	case st.Err != nil:
		stat = sRed.Render("error")
	default:
		stat = statusText(st)
		if st.Upstream == "" {
			ab = sDim.Render("—")
		} else {
			var parts []string
			if st.Ahead > 0 {
				parts = append(parts, sGreen.Render(fmt.Sprintf("↑%d", st.Ahead)))
			}
			if st.Behind > 0 {
				parts = append(parts, sRed.Render(fmt.Sprintf("↓%d", st.Behind)))
			}
			if len(parts) == 0 {
				parts = append(parts, sDim.Render("="))
			}
			ab = strings.Join(parts, " ")
		}
		if !st.CommitTime.IsZero() {
			age = worktree.HumanDuration(time.Since(st.CommitTime))
		}
		subj = st.Subject
	}
	tags := ""
	if wt.Main {
		tags += sDim.Render(" [main]")
	}
	if wt.Locked {
		tags += sYellow.Render(" [locked]")
	}
	if sel {
		name = sAccent.Bold(true).Render(name)
	}
	line := marker + name + " " + pad(stat, 16) + " " + pad(ab, 9) + " " + sDim.Render(pad(age, 4)) + " "
	rest := width - lipgloss.Width(line) - lipgloss.Width(tags) - 1
	line += truncate(subj, rest) + tags
	if sel {
		return sAccent.Render("▌") + line
	}
	return " " + line
}

func statusText(st git.Status) string {
	if !st.Dirty() {
		return sGreen.Render("✓ clean")
	}
	var parts []string
	if st.Conflicts > 0 {
		parts = append(parts, sRed.Render(fmt.Sprintf("!%d", st.Conflicts)))
	}
	if st.Staged > 0 {
		parts = append(parts, sGreen.Render(fmt.Sprintf("+%d", st.Staged)))
	}
	if st.Unstaged > 0 {
		parts = append(parts, sYellow.Render(fmt.Sprintf("~%d", st.Unstaged)))
	}
	if st.Untracked > 0 {
		parts = append(parts, sDim.Render(fmt.Sprintf("?%d", st.Untracked)))
	}
	return sYellow.Render("● ") + strings.Join(parts, " ")
}

func (l *list) details(width int) string {
	wt := l.selected()
	if wt == nil {
		return ""
	}
	p := wt.Path
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		p = "~" + strings.TrimPrefix(p, home)
	}
	s := " " + sDim.Render("path ") + p
	if st, ok := l.statuses[wt.Path]; ok && st.Upstream != "" {
		s += sDim.Render("  upstream ") + st.Upstream
	}
	if wt.Locked && wt.LockReason != "" {
		s += sYellow.Render("  locked: " + wt.LockReason)
	}
	return truncate(s, width)
}

func (l *list) helpView() string {
	rows := [][2]string{
		{"enter", "cd into worktree (needs shell integration)"},
		{"/", "fuzzy search · esc clears"},
		{"j/k ↑/↓", "move"},
		{"n", "new worktree (new, local, remote branch or PR)"},
		{"d", "delete worktree (like git worktree remove; branch kept)"},
		{"c", "commit: stage files / hunks / lines"},
		{"m", "merge branch locally (merge, ff, squash, rebase)"},
		{"P", "push and open a pull request in the browser"},
		{"p", "prune merged, stale or missing worktrees"},
		{"o", "open in editor"},
		{"f", "git fetch --all --prune"},
		{"r", "refresh"},
		{"q", "quit"},
	}
	var b strings.Builder
	b.WriteString("\n")
	for _, r := range rows {
		b.WriteString("   " + sKey.Render(pad(r[0], 10)) + " " + r[1] + "\n")
	}
	b.WriteString("\n   " + sDim.Render("press any key to close"))
	return b.String()
}

// ------------------------------------------------------------- actions --

type removedMsg struct {
	wt    git.Worktree
	force int
	err   error
}

func removeCmd(e *env, wt git.Worktree, force int) tea.Cmd {
	return func() tea.Msg {
		err := e.m.Remove(wt.Path, worktree.RemoveOptions{Force: force})
		return removedMsg{wt: wt, force: force, err: err}
	}
}

// deleteFlow confirms and removes a worktree the way `git worktree remove` does.
func deleteFlow(e *env, wt git.Worktree) tea.Cmd {
	if wt.Main {
		return statusErr(fmt.Errorf("the main worktree cannot be removed"))
	}
	if wt.Bare {
		return statusErr(fmt.Errorf("the bare repository cannot be removed"))
	}
	body := fmt.Sprintf("%s\n\n%s", wt.Path, sDim.Render("Runs `git worktree remove`. The branch "+wt.Name()+" is kept."))
	doRemove := func() tea.Cmd {
		plan := preRemovePlan(e, wt.Path)
		if plan.Empty() {
			return removeCmd(e, wt, 0)
		}
		return push(newConfirm(e, "Run pre_remove hooks?", plan.Summary(),
			option{label: "Run", key: "r", run: func() tea.Cmd { return runHooks(plan, "pre_remove", removeCmd(e, wt, 0)) }},
			option{label: "Skip", key: "s", run: func() tea.Cmd { return removeCmd(e, wt, 0) }},
			option{label: "Abort", key: "a"},
		))
	}
	return push(newConfirm(e, "Remove worktree "+wt.Name()+"?", body,
		option{label: "Remove", key: "y", run: doRemove, danger: true},
		option{label: "Cancel", key: "n"},
	))
}

func (l *list) onRemoved(msg removedMsg) tea.Cmd {
	e := l.e
	if msg.err != nil {
		if msg.force >= 2 || (msg.force == 1 && !msg.wt.Locked) {
			return statusErr(msg.err)
		}
		force := 1
		if msg.wt.Locked || strings.Contains(git.ErrMessage(msg.err), "locked") {
			force = 2
		}
		c := newConfirm(e, "git refused to remove "+msg.wt.Name(), sYellow.Render(git.ErrMessage(msg.err))+"\n\n"+sDim.Render("Force removal discards uncommitted changes and untracked files."),
			option{label: "Force remove", key: "f", danger: true, run: func() tea.Cmd { return removeCmd(e, msg.wt, force) }},
			option{label: "Cancel", key: "n"},
		)
		c.cur = 1 // default to the safe choice
		return push(c)
	}
	if msg.wt.Path == e.m.Repo.Current {
		e.fallbackCD = e.m.Repo.MainRoot
	}
	return tea.Batch(l.load(""), status("Removed "+msg.wt.Name()+" (branch kept)"))
}

type editorDoneMsg struct{ err error }

func openEditor(e *env, path string) tea.Cmd {
	cmd := editorCmd(e, path)
	cmd.Dir = path
	name := filepath.Base(cmd.Path)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return statusMsg{text: name + ": " + err.Error(), err: true}
		}
		return statusMsg{text: "Opened in " + name}
	})
}
