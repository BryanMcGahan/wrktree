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
	right := ""
	switch {
	case l.fetching:
		right = l.spin.View() + " fetching"
	case l.loading:
		right = l.spin.View() + " loading"
	}
	info := fmt.Sprintf("%d worktrees  ·  base %s", len(l.wts), l.base)
	header := headerBar(w, e.m.Repo.Name, info, right)
	keys := help("enter", "cd", "/", "search", "n", "new", "d", "delete", "c", "commit", "m", "merge", "P", "PR", "o", "open", "?", "more")

	var top strings.Builder
	if l.filtering || l.filter.Value() != "" {
		top.WriteString(" " + l.filter.View() + "\n")
	} else {
		top.WriteString(" " + sDim.Render("/ search  ·  ● current worktree") + "\n")
	}
	if l.pending != nil {
		top.WriteString(" " + sYellow.Render(fmt.Sprintf("⚠ merge of %s into %s stopped on conflicts — press M to resolve", l.pending.Source, l.pending.Target)) + "\n")
	}
	if l.err != nil {
		top.WriteString(" " + sErr.Render(errText(l.err)) + "\n")
	}
	if l.showHelp {
		hv := l.helpView()
		return frame(e, header, top.String()+"\n"+indent(panel("Keyboard shortcuts", hv, min(w-2, 72), lipgloss.Height(hv)+2, true), 1), help("any key", "close"))
	}

	nameW := 6
	for _, wt := range l.view {
		nameW = max(nameW, lipgloss.Width(wt.Name()))
	}
	nameW = min(max(nameW+2, 12), w/3)

	// Column headings + rule.
	cols := "   " + sColHead.Render(pad("BRANCH", nameW)) + " " + sColHead.Render(pad("STATUS", colStatus)) + " " +
		sColHead.Render(pad("SYNC", colSync)) + " " + sColHead.Render(pad("AGE", colAge)) + " " + sColHead.Render("LAST COMMIT")
	top.WriteString(truncate(cols, w) + "\n")
	top.WriteString(sRule.Render(strings.Repeat("─", w)) + "\n")

	const detailsH = 4
	avail := e.height - 1 - 1 - 2 // status line, header, footer
	listH := avail - lipgloss.Height(strings.TrimSuffix(top.String(), "\n")) - detailsH
	listH = max(listH, 1)
	start := 0
	if l.cur >= listH {
		start = l.cur - listH + 1
	}
	var rows []string
	if len(l.view) == 0 && !l.loading {
		rows = append(rows, sDim.Render("   no worktrees match"))
	}
	for i := start; i < len(l.view) && i < start+listH; i++ {
		rows = append(rows, l.row(l.view[i], i == l.cur, nameW, w))
	}
	for len(rows) < listH {
		rows = append(rows, "")
	}
	body := top.String() + strings.Join(rows, "\n") + "\n" + l.details(w, detailsH)
	return frame(e, header, body, keys)
}

const (
	colStatus = 16
	colSync   = 8
	colAge    = 4
)

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
		ab = syncText(st)
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
	line := marker + name + " " + pad(stat, colStatus) + " " + pad(ab, colSync) + " " + sDim.Render(pad(age, colAge)) + " "
	rest := width - lipgloss.Width(line) - lipgloss.Width(tags) - 2
	line += truncate(subj, rest) + tags
	if sel {
		return sAccent.Render("▌") + line
	}
	return " " + line
}

func syncText(st git.Status) string {
	if st.Upstream == "" {
		return sDim.Render("—")
	}
	var parts []string
	if st.Ahead > 0 {
		parts = append(parts, sGreen.Render(fmt.Sprintf("↑%d", st.Ahead)))
	}
	if st.Behind > 0 {
		parts = append(parts, sRed.Render(fmt.Sprintf("↓%d", st.Behind)))
	}
	if len(parts) == 0 {
		return sDim.Render("=")
	}
	return strings.Join(parts, " ")
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

func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func (l *list) details(width, h int) string {
	wt := l.selected()
	if wt == nil {
		return panel("", "", width, h, false)
	}
	label := func(s string) string { return sDim.Render(pad(s, 9)) }
	line1 := label("path") + shortPath(wt.Path)
	var line2 string
	st, ok := l.statuses[wt.Path]
	switch {
	case wt.Prunable:
		line2 = label("status") + sRed.Render("directory is missing — prune to clean up")
	case ok && st.Err == nil:
		var parts []string
		add := func(n int, what string, s lipgloss.Style) {
			if n > 0 {
				parts = append(parts, s.Render(fmt.Sprintf("%d %s", n, what)))
			}
		}
		add(st.Conflicts, "conflicted", sRed)
		add(st.Staged, "staged", sGreen)
		add(st.Unstaged, "modified", sYellow)
		add(st.Untracked, "untracked", sDim)
		if len(parts) == 0 {
			parts = append(parts, sGreen.Render("clean"))
		}
		line2 = label("status") + strings.Join(parts, sDim.Render(" · "))
		if st.Upstream != "" {
			line2 += sDim.Render("   upstream ") + st.Upstream + " " + syncText(st)
		} else if !wt.Bare {
			line2 += sDim.Render("   not pushed")
		}
	}
	if wt.Locked {
		reason := wt.LockReason
		if reason == "" {
			reason = "locked"
		}
		line2 += sYellow.Render("   🔒 " + reason)
	}
	return panel(wt.Name(), line1+"\n"+line2, width, h, false)
}

func (l *list) helpView() string {
	groups := []struct {
		title string
		rows  [][2]string
	}{
		{"Navigate", [][2]string{
			{"enter", "cd into worktree"},
			{"/", "fuzzy search (esc clears)"},
			{"j/k ↑/↓", "move"},
		}},
		{"Worktrees", [][2]string{
			{"n", "new (branch, remote branch or PR)"},
			{"d", "delete (git worktree remove; branch kept)"},
			{"o", "open in editor"},
			{"p", "prune merged / stale / missing"},
		}},
		{"Git", [][2]string{
			{"c", "commit (files, hunks, lines)"},
			{"m", "merge locally"},
			{"P", "push & open pull request"},
			{"f · r · q", "fetch · refresh · quit"},
		}},
	}
	var b strings.Builder
	for i, g := range groups {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(sSection.Render(g.title) + "\n")
		for _, r := range g.rows {
			b.WriteString("  " + sKey.Render(pad(r[0], 10)) + " " + r[1] + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
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
