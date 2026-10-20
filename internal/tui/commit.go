package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/patch"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	focusFiles = iota
	focusDiff
	focusSubject
	focusBody
)

type fileEntry struct {
	fc     git.FileChange
	staged bool
}

type diffRow struct {
	hunk, line int
	header     bool
	kind       patch.Kind
	text       string
}

type (
	commitDoneMsg struct {
		out string
		err error
	}
	msgEditedMsg struct {
		text string
		err  error
	}
)

type commit struct {
	e          *env
	dir        string
	branch     string
	standalone bool

	entries []fileEntry
	fcur    int
	fscroll int
	focus   int

	diff    *patch.File
	rows    []diffRow
	dcur    int
	anchor  int // -1 when not selecting a range
	dscroll int
	diffErr error

	subject textinput.Model
	body    textarea.Model
	amend   bool
	busy    bool
	err     error
}

func newCommit(e *env, dir string, standalone bool) *commit {
	s := textinput.New()
	s.Prompt = ""
	s.Placeholder = "Summary (required)"
	s.CharLimit = 200
	b := textarea.New()
	b.Prompt = ""
	b.Placeholder = "Description"
	b.ShowLineNumbers = false
	b.SetHeight(3)
	b.FocusedStyle.CursorLine = lipgloss.NewStyle()
	branch, _ := git.Run(dir, "symbolic-ref", "--short", "-q", "HEAD")
	c := &commit{e: e, dir: dir, branch: branch, standalone: standalone, subject: s, body: b, anchor: -1}
	c.reload()
	return c
}

func (c *commit) Init() tea.Cmd { return textinput.Blink }

// reload re-reads status and the current diff, preserving the selection.
func (c *commit) reload() {
	var prev *fileEntry
	if c.fcur < len(c.entries) {
		p := c.entries[c.fcur]
		prev = &p
	}
	changes, err := git.Changes(c.dir)
	c.err = err
	var unstaged, staged []fileEntry
	for _, fc := range changes {
		if fc.HasUnstaged() {
			unstaged = append(unstaged, fileEntry{fc: fc})
		}
		if fc.HasStaged() {
			staged = append(staged, fileEntry{fc: fc, staged: true})
		}
	}
	c.entries = append(unstaged, staged...)
	if prev != nil {
		found := false
		for i, en := range c.entries {
			if en.fc.Path == prev.fc.Path && en.staged == prev.staged {
				c.fcur, found = i, true
				break
			}
		}
		if !found && c.fcur >= len(c.entries) {
			c.fcur = len(c.entries) - 1
		}
	}
	if c.fcur < 0 {
		c.fcur = 0
	}
	c.loadDiff(true)
}

func (c *commit) current() *fileEntry {
	if c.fcur < len(c.entries) {
		return &c.entries[c.fcur]
	}
	return nil
}

func (c *commit) loadDiff(keepCursor bool) {
	oldCur := c.dcur
	c.diff, c.rows, c.diffErr = nil, nil, nil
	c.anchor = -1
	en := c.current()
	if en == nil {
		return
	}
	raw, err := git.Diff(c.dir, en.fc, en.staged)
	if err != nil {
		c.diffErr = err
		return
	}
	f, err := patch.Parse(raw)
	if err != nil {
		c.diffErr = err
		return
	}
	c.diff = f
	for hi, h := range f.Hunks {
		c.rows = append(c.rows, diffRow{hunk: hi, header: true,
			text: fmt.Sprintf("@@ -%d,%d +%d,%d @@ %s", h.OldStart, h.OldCount, h.NewStart, h.NewCount, h.Section)})
		for li, l := range h.Lines {
			c.rows = append(c.rows, diffRow{hunk: hi, line: li, kind: l.Kind, text: l.Text})
		}
	}
	if !keepCursor {
		c.dcur, c.dscroll = 0, 0
	} else {
		c.dcur = oldCur
	}
	if c.dcur >= len(c.rows) {
		c.dcur = len(c.rows) - 1
	}
	if c.dcur < 0 {
		c.dcur = 0
	}
	c.snapCursor(1)
}

func (c *commit) isChange(i int) bool {
	return i >= 0 && i < len(c.rows) && !c.rows[i].header && (c.rows[i].kind == patch.Add || c.rows[i].kind == patch.Del)
}

// snapCursor moves the diff cursor to the nearest change line in direction dir.
func (c *commit) snapCursor(dir int) {
	if len(c.rows) == 0 || c.isChange(c.dcur) {
		return
	}
	for i := c.dcur; i >= 0 && i < len(c.rows); i += dir {
		if c.isChange(i) {
			c.dcur = i
			return
		}
	}
	for i := c.dcur; i >= 0 && i < len(c.rows); i -= dir {
		if c.isChange(i) {
			c.dcur = i
			return
		}
	}
}

func (c *commit) moveDiff(dir int) {
	for i := c.dcur + dir; i >= 0 && i < len(c.rows); i += dir {
		if c.isChange(i) {
			c.dcur = i
			return
		}
	}
}

func (c *commit) jumpHunk(dir int) {
	if len(c.rows) == 0 {
		return
	}
	h := c.rows[c.dcur].hunk + dir
	for i, r := range c.rows {
		if r.hunk == h && c.isChange(i) {
			c.dcur = i
			return
		}
	}
}

// applyLines stages or unstages the selected diff rows.
func (c *commit) applyLines(rows []int) error {
	en := c.current()
	if en == nil || c.diff == nil {
		return nil
	}
	if c.diff.Binary {
		return fmt.Errorf("binary files can only be staged as a whole (space in the file list)")
	}
	sel := patch.Selection{}
	for _, i := range rows {
		if c.isChange(i) {
			sel[patch.Pos{Hunk: c.rows[i].hunk, Line: c.rows[i].line}] = true
		}
	}
	if len(sel) == 0 {
		return nil
	}
	f := c.diff
	if en.fc.Untracked() {
		// Mark intent-to-add so the index has an entry to patch, then rebuild
		// the diff (it has the same hunks as the untracked preview).
		if err := git.IntentToAdd(c.dir, en.fc.Path); err != nil {
			return err
		}
		raw, err := git.Diff(c.dir, git.FileChange{Path: en.fc.Path, X: 'A', Y: 'M'}, false)
		if err != nil {
			return err
		}
		if f, err = patch.Parse(raw); err != nil {
			return err
		}
	}
	p := f.Build(sel, en.staged)
	if p == "" {
		return nil
	}
	return git.ApplyCached(c.dir, p, en.staged)
}

func (c *commit) selectedRows() []int {
	if c.anchor < 0 {
		return []int{c.dcur}
	}
	a, b := c.anchor, c.dcur
	if a > b {
		a, b = b, a
	}
	var res []int
	for i := a; i <= b; i++ {
		res = append(res, i)
	}
	return res
}

func (c *commit) hunkRows() []int {
	if len(c.rows) == 0 {
		return nil
	}
	h := c.rows[c.dcur].hunk
	var res []int
	for i, r := range c.rows {
		if r.hunk == h {
			res = append(res, i)
		}
	}
	return res
}

func (c *commit) toggleFile() error {
	en := c.current()
	if en == nil {
		return nil
	}
	if en.staged {
		return git.UnstageFile(c.dir, en.fc)
	}
	return git.StageFile(c.dir, en.fc.Path)
}

func (c *commit) message() string {
	subj := strings.TrimSpace(c.subject.Value())
	body := strings.TrimSpace(c.body.Value())
	if body == "" {
		return subj
	}
	return subj + "\n\n" + body
}

func (c *commit) setMessage(msg string) {
	msg = strings.TrimSpace(msg)
	subj, body, _ := strings.Cut(msg, "\n")
	c.subject.SetValue(strings.TrimSpace(subj))
	c.body.SetValue(strings.TrimSpace(body))
}

func (c *commit) hasStaged() bool {
	for _, en := range c.entries {
		if en.staged {
			return true
		}
	}
	return false
}

func (c *commit) doCommit() tea.Cmd {
	if strings.TrimSpace(c.subject.Value()) == "" {
		c.err = fmt.Errorf("a commit summary is required")
		c.setFocus(focusSubject)
		return nil
	}
	if !c.hasStaged() && !c.amend {
		c.err = fmt.Errorf("nothing staged — press space to stage files or lines")
		return nil
	}
	c.busy, c.err = true, nil
	dir, msg, amend := c.dir, c.message(), c.amend
	return func() tea.Msg {
		out, err := git.Commit(dir, msg, amend)
		return commitDoneMsg{out: out, err: err}
	}
}

func (c *commit) editMessage() tea.Cmd {
	f, err := os.CreateTemp("", "wrk-COMMIT_EDITMSG-*")
	if err != nil {
		return statusErr(err)
	}
	f.WriteString(c.message() + "\n\n# Lines starting with '#' are ignored. First line is the summary.\n")
	f.Close()
	name := f.Name()
	return tea.ExecProcess(editorCmd(c.e, name), func(err error) tea.Msg {
		defer os.Remove(name)
		if err != nil {
			return msgEditedMsg{err: err}
		}
		data, rerr := os.ReadFile(name)
		var keep []string
		for _, l := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(l, "#") {
				keep = append(keep, l)
			}
		}
		return msgEditedMsg{text: strings.Join(keep, "\n"), err: rerr}
	})
}

func (c *commit) setFocus(f int) tea.Cmd {
	c.focus = f
	c.subject.Blur()
	c.body.Blur()
	switch f {
	case focusSubject:
		return c.subject.Focus()
	case focusBody:
		return c.body.Focus()
	}
	return nil
}

func (c *commit) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case commitDoneMsg:
		c.busy = false
		if msg.err != nil {
			c.err = msg.err
			return c, nil
		}
		first := strings.SplitN(msg.out, "\n", 2)[0]
		c.subject.SetValue("")
		c.body.SetValue("")
		c.amend = false
		c.reload()
		c.setFocus(focusFiles)
		return c, status("Committed: " + first)
	case msgEditedMsg:
		if msg.err != nil {
			return c, statusErr(msg.err)
		}
		c.setMessage(msg.text)
		return c, nil
	case tea.KeyMsg:
		if c.busy {
			return c, nil
		}
		c.err = nil
		switch msg.String() {
		case "ctrl+s":
			return c, c.doCommit()
		case "ctrl+e":
			return c, c.editMessage()
		}
		switch c.focus {
		case focusFiles:
			return c.updateFiles(msg)
		case focusDiff:
			return c.updateDiff(msg)
		default:
			return c.updateMessage(msg)
		}
	}
	if c.focus == focusSubject {
		var cmd tea.Cmd
		c.subject, cmd = c.subject.Update(msg)
		return c, cmd
	}
	if c.focus == focusBody {
		var cmd tea.Cmd
		c.body, cmd = c.body.Update(msg)
		return c, cmd
	}
	return c, nil
}

func (c *commit) commonKeys(k string) (tea.Cmd, bool) {
	switch k {
	case "A":
		c.amend = !c.amend
		if c.amend && strings.TrimSpace(c.message()) == "" {
			c.setMessage(git.LastCommitMessage(c.dir))
		}
		return nil, true
	case "e":
		return c.editMessage(), true
	case "i":
		return c.setFocus(focusSubject), true
	case "r":
		c.reload()
		return nil, true
	}
	return nil, false
}

func (c *commit) updateFiles(k tea.KeyMsg) (screen, tea.Cmd) {
	if cmd, ok := c.commonKeys(k.String()); ok {
		return c, cmd
	}
	switch k.String() {
	case "esc", "q":
		return c, pop(refreshMsg{})
	case "up", "k":
		if c.fcur > 0 {
			c.fcur--
			c.loadDiff(false)
		}
	case "down", "j":
		if c.fcur < len(c.entries)-1 {
			c.fcur++
			c.loadDiff(false)
		}
	case " ":
		if err := c.toggleFile(); err != nil {
			c.err = err
		}
		c.reload()
	case "a":
		if _, err := git.Run(c.dir, "add", "-A"); err != nil {
			c.err = err
		}
		c.reload()
	case "u":
		if _, err := git.Run(c.dir, "reset", "-q"); err != nil {
			if _, err2 := git.Run(c.dir, "rm", "-r", "--cached", "-q", "."); err2 != nil {
				c.err = err
			}
		}
		c.reload()
	case "enter", "right", "l":
		if len(c.rows) > 0 {
			c.focus = focusDiff
		}
	case "tab":
		if len(c.rows) > 0 {
			c.focus = focusDiff
			return c, nil
		}
		return c, c.setFocus(focusSubject)
	}
	return c, nil
}

func (c *commit) updateDiff(k tea.KeyMsg) (screen, tea.Cmd) {
	if cmd, ok := c.commonKeys(k.String()); ok {
		return c, cmd
	}
	switch k.String() {
	case "esc":
		if c.anchor >= 0 {
			c.anchor = -1
			return c, nil
		}
		c.focus = focusFiles
	case "left", "h":
		c.anchor = -1
		c.focus = focusFiles
	case "up", "k":
		c.moveDiff(-1)
	case "down", "j":
		c.moveDiff(1)
	case "n", "]":
		c.jumpHunk(1)
	case "N", "[", "p":
		c.jumpHunk(-1)
	case "v":
		if c.anchor >= 0 {
			c.anchor = -1
		} else {
			c.anchor = c.dcur
		}
	case " ", "enter":
		if err := c.applyLines(c.selectedRows()); err != nil {
			c.err = err
		}
		c.afterApply()
	case "a":
		if err := c.applyLines(c.hunkRows()); err != nil {
			c.err = err
		}
		c.afterApply()
	case "tab":
		return c, c.setFocus(focusSubject)
	}
	return c, nil
}

func (c *commit) afterApply() {
	path, staged := "", false
	if en := c.current(); en != nil {
		path, staged = en.fc.Path, en.staged
	}
	c.reload()
	// Stay on the same file/section if it still has changes there.
	if en := c.current(); en == nil || en.fc.Path != path || en.staged != staged || len(c.rows) == 0 {
		c.focus = focusFiles
	}
}

func (c *commit) updateMessage(k tea.KeyMsg) (screen, tea.Cmd) {
	switch k.String() {
	case "esc":
		return c, c.setFocus(focusFiles)
	case "tab":
		if c.focus == focusSubject {
			return c, c.setFocus(focusBody)
		}
		return c, c.setFocus(focusFiles)
	case "shift+tab":
		if c.focus == focusBody {
			return c, c.setFocus(focusSubject)
		}
		return c, c.setFocus(focusFiles)
	case "enter":
		if c.focus == focusSubject {
			return c, c.setFocus(focusBody)
		}
	}
	var cmd tea.Cmd
	if c.focus == focusSubject {
		c.subject, cmd = c.subject.Update(k)
	} else {
		c.body, cmd = c.body.Update(k)
	}
	return c, cmd
}

// ------------------------------------------------------------------ view --

func (c *commit) View() string {
	e := c.e
	W, H := e.width, e.height-1

	head := " " + sTitle.Render("Commit") + sDim.Render(" · ") + sBold.Render(c.branch) + sDim.Render(" · "+c.dir)
	if c.amend {
		head += "  " + sYellow.Render("[amend]")
	}

	msgH := 7 // subject + body(3) + counter + borders
	footerH := 2
	paneH := H - 1 - msgH - footerH
	if paneH < 5 {
		paneH = 5
	}
	fw := W / 3
	if fw < 26 {
		fw = 26
	}
	if fw > 50 {
		fw = 50
	}
	dw := W - fw

	files := c.filesView(fw-4, paneH-2)
	diff := c.diffView(dw-4, paneH-2)
	box := func(s string, w, h int, focused bool) string {
		st := sBox
		if focused {
			st = sFocusBox
		}
		return st.Width(w - 2).Height(h - 2).Render(s)
	}
	panes := lipgloss.JoinHorizontal(lipgloss.Top,
		box(files, fw, paneH, c.focus == focusFiles),
		box(diff, dw, paneH, c.focus == focusDiff))

	c.subject.Width = W - 12
	c.body.SetWidth(W - 6)
	n := len([]rune(c.subject.Value()))
	counter := sDim.Render(fmt.Sprintf("%d", n))
	if n > 72 {
		counter = sRed.Render(fmt.Sprintf("%d", n))
	} else if n > 50 {
		counter = sYellow.Render(fmt.Sprintf("%d", n))
	}
	msgContent := pad(c.subject.View(), W-10) + " " + counter + "\n" + sDim.Render(strings.Repeat("─", W-6)) + "\n" + c.body.View()
	msgBox := box(msgContent, W, msgH, c.focus == focusSubject || c.focus == focusBody)

	var foot string
	switch {
	case c.busy:
		foot = sAccent.Render(" committing…")
	case c.err != nil:
		foot = " " + sErr.Render(truncate(errText(c.err), W-2))
	}
	var keys string
	switch c.focus {
	case focusFiles:
		keys = help("space", "stage/unstage file", "a", "stage all", "u", "unstage all", "enter", "lines", "i", "message", "A", "amend", "ctrl+s", "commit", "esc", "back")
	case focusDiff:
		keys = help("space", "stage/unstage line", "v", "select range", "a", "hunk", "n/N", "next/prev hunk", "esc", "files", "ctrl+s", "commit")
	default:
		keys = help("tab", "next field", "ctrl+e", "open $EDITOR", "ctrl+s", "commit", "esc", "files")
	}
	return head + "\n" + panes + "\n" + msgBox + "\n" + foot + "\n " + truncate(keys, W-2)
}

func fileCode(en fileEntry) string {
	fc := en.fc
	var ch byte
	switch {
	case fc.Conflicted():
		return sRed.Render("U")
	case fc.Untracked():
		return sGreen.Render("?")
	case en.staged:
		ch = fc.X
	default:
		ch = fc.Y
	}
	s := string(ch)
	switch ch {
	case 'A':
		return sGreen.Render(s)
	case 'D':
		return sRed.Render(s)
	case 'R', 'C':
		return sCyan.Render(s)
	}
	return sYellow.Render(s)
}

func (c *commit) filesView(w, h int) string {
	var lines []string
	if c.err != nil && len(c.entries) == 0 {
		return sErr.Render(errText(c.err))
	}
	if len(c.entries) == 0 {
		return sDim.Render("working tree clean")
	}
	section := ""
	sel := -1
	for i, en := range c.entries {
		s := "Unstaged"
		if en.staged {
			s = "Staged"
		}
		if s != section {
			section = s
			col := sYellow
			if en.staged {
				col = sGreen
			}
			lines = append(lines, col.Bold(true).Render(s))
		}
		name := en.fc.Path
		if en.fc.OrigPath != "" {
			name = en.fc.OrigPath + " → " + en.fc.Path
		}
		if i == c.fcur {
			sel = len(lines)
			if c.focus == focusFiles {
				lines = append(lines, selRow(" "+string(codeChar(en))+" "+name, w))
			} else {
				lines = append(lines, sMarkRow.Render(pad(" "+string(codeChar(en))+" "+name, w)))
			}
		} else {
			lines = append(lines, " "+fileCode(en)+" "+truncate(name, w-3))
		}
	}
	if sel >= c.fscroll+h {
		c.fscroll = sel - h + 1
	}
	if sel < c.fscroll {
		c.fscroll = sel
	}
	if c.fscroll > 0 && sel < 3 {
		c.fscroll = 0
	}
	end := c.fscroll + h
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[c.fscroll:end], "\n")
}

func codeChar(en fileEntry) byte {
	switch {
	case en.fc.Conflicted():
		return 'U'
	case en.fc.Untracked():
		return '?'
	case en.staged:
		return en.fc.X
	}
	return en.fc.Y
}

func (c *commit) diffView(w, h int) string {
	en := c.current()
	if en == nil {
		return sDim.Render("nothing to show")
	}
	if c.diffErr != nil {
		return sErr.Render(errText(c.diffErr))
	}
	if c.diff == nil {
		return ""
	}
	title := sDim.Render("unstaged changes")
	if en.staged {
		title = sGreen.Render("staged changes") + sDim.Render(" (space unstages)")
	}
	if c.diff.Binary {
		return title + "\n\n" + sDim.Render("binary file — stage it as a whole from the file list")
	}
	if len(c.rows) == 0 {
		return title + "\n\n" + sDim.Render("no textual changes (mode change or empty file)")
	}
	h-- // title line
	if c.dcur < c.dscroll+2 {
		c.dscroll = c.dcur - 2
	}
	if c.dcur >= c.dscroll+h-2 {
		c.dscroll = c.dcur - h + 3
	}
	if c.dscroll > len(c.rows)-h {
		c.dscroll = len(c.rows) - h
	}
	if c.dscroll < 0 {
		c.dscroll = 0
	}
	inRange := func(i int) bool {
		if c.anchor < 0 {
			return false
		}
		a, b := c.anchor, c.dcur
		if a > b {
			a, b = b, a
		}
		return i >= a && i <= b
	}
	out := []string{title}
	for i := c.dscroll; i < len(c.rows) && i < c.dscroll+h; i++ {
		r := c.rows[i]
		text := strings.ReplaceAll(r.text, "\t", "    ")
		if r.header {
			out = append(out, sCyan.Render(truncate(text, w)))
			continue
		}
		line := string(r.kind) + text
		if r.kind == patch.NoEOL {
			line = "\\" + text
		}
		var st lipgloss.Style
		switch r.kind {
		case patch.Add:
			st = sGreen
		case patch.Del:
			st = sRed
		default:
			st = sDim
		}
		focused := c.focus == focusDiff
		switch {
		case i == c.dcur && focused:
			out = append(out, st.Background(colSel).Bold(true).Render(pad("▌"+line, w)))
		case inRange(i) && focused:
			out = append(out, st.Background(colMark).Render(pad(" "+line, w)))
		default:
			out = append(out, st.Render(truncate(" "+line, w)))
		}
	}
	return strings.Join(out, "\n")
}
