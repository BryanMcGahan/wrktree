package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/merge"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	mConfig = iota
	mRunning
	mConflict
	mDone
)

type (
	previewMsg struct {
		key string
		p   *merge.Preview
	}
	mergeDoneMsg struct {
		res *merge.Result
		err error
	}
	targetPickedMsg struct{ target string }
	pushDoneMsg     struct{ err error }
	squashMsgMsg    struct {
		text string
		err  error
	}
)

type mergeScreen struct {
	e        *env
	state    int
	src      git.Worktree
	target   string
	strat    int
	preview  *merge.Preview
	squash   string
	res      *merge.Result
	conflict *merge.Conflict
	pending  *merge.State
	ccur     int
	err      error
	spin     spinner.Model
	pushing  bool
	pushed   bool
}

func newMerge(e *env, src git.Worktree) *mergeScreen {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = sAccent
	return &mergeScreen{e: e, src: src, target: e.m.BaseBranch(), spin: sp}
}

func newMergeResume(e *env) *mergeScreen {
	ms := newMerge(e, git.Worktree{})
	ms.pending = merge.Pending(e.m)
	if ms.pending != nil {
		ms.src = git.Worktree{Branch: ms.pending.Source, Path: ms.pending.SourcePath}
		ms.target = ms.pending.Target
		for i, s := range merge.Strategies {
			if s == ms.pending.Strategy {
				ms.strat = i
			}
		}
		ms.state = mConflict
		ms.conflict = &merge.Conflict{Dir: ms.pending.Dir, Files: merge.Conflicts(ms.pending), Op: ms.pending.Strategy}
	}
	return ms
}

func (ms *mergeScreen) strategy() merge.Strategy { return merge.Strategies[ms.strat] }

func (ms *mergeScreen) request() merge.Request {
	r := merge.Request{Source: ms.src.Branch, SourcePath: ms.src.Path, Target: ms.target, Strategy: ms.strategy()}
	if r.Strategy == merge.Squash {
		r.Message = ms.squash
	}
	return r
}

func (ms *mergeScreen) key() string { return ms.target + "|" + string(ms.strategy()) }

func (ms *mergeScreen) loadPreview() tea.Cmd {
	m, r, key := ms.e.m, ms.request(), ms.key()
	ms.preview = nil
	return func() tea.Msg { return previewMsg{key: key, p: merge.GetPreview(m, r)} }
}

func (ms *mergeScreen) Init() tea.Cmd {
	if ms.state == mConflict {
		return nil
	}
	if ms.pending == nil && merge.Pending(ms.e.m) != nil {
		return replace(newMergeResume(ms.e))
	}
	return tea.Batch(ms.spin.Tick, ms.loadPreview())
}

func (ms *mergeScreen) pickTarget() tea.Cmd {
	m := ms.e.m
	src := ms.src.Branch
	load := func() ([]pickItem, error) {
		dir := m.Dir()
		if r := m.Remote(); r != "" {
			_, _ = git.Run(dir, "fetch", "--prune", r)
		}
		var items []pickItem
		for _, b := range git.LocalBranches(dir) {
			if b != src {
				items = append(items, pickItem{title: b, desc: "local", value: b})
			}
		}
		for _, b := range git.RemoteBranches(dir) {
			items = append(items, pickItem{title: b, desc: "remote", value: b})
		}
		return items, nil
	}
	p := newPicker(ms.e, "Merge "+src+" into…", load, func(it pickItem) tea.Cmd {
		return msgCmd(targetPickedMsg{it.value.(string)})
	})
	return push(p)
}

func (ms *mergeScreen) run() tea.Cmd {
	if ms.preview == nil || len(ms.preview.Problems) > 0 {
		return nil
	}
	ms.state, ms.err = mRunning, nil
	m, r := ms.e.m, ms.request()
	return tea.Batch(ms.spin.Tick, func() tea.Msg {
		res, err := merge.Run(m, r)
		return mergeDoneMsg{res, err}
	})
}

func (ms *mergeScreen) editSquash() tea.Cmd {
	msg := ms.squash
	if msg == "" {
		msg = merge.DefaultSquashMessage(ms.e.m, ms.src.Branch, ms.target)
	}
	f, err := os.CreateTemp("", "wrk-SQUASH_MSG-*")
	if err != nil {
		return statusErr(err)
	}
	f.WriteString(msg)
	f.Close()
	name := f.Name()
	return tea.ExecProcess(editorCmd(ms.e, name), func(err error) tea.Msg {
		defer os.Remove(name)
		data, rerr := os.ReadFile(name)
		if err == nil {
			err = rerr
		}
		return squashMsgMsg{text: string(data), err: err}
	})
}

func (ms *mergeScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case previewMsg:
		if msg.key == ms.key() {
			ms.preview = msg.p
		}
		return ms, nil
	case targetPickedMsg:
		ms.target = msg.target
		return ms, ms.loadPreview()
	case squashMsgMsg:
		if msg.err != nil {
			return ms, statusErr(msg.err)
		}
		ms.squash = msg.text
		return ms, nil
	case mergeDoneMsg:
		var c *merge.Conflict
		switch {
		case errors.As(msg.err, &c):
			ms.state, ms.conflict, ms.ccur = mConflict, c, 0
			ms.pending = merge.Pending(ms.e.m)
		case msg.err != nil:
			ms.err = msg.err
			if ms.pending != nil {
				ms.state = mConflict
				ms.refreshConflicts()
			} else {
				ms.state = mConfig
				return ms, ms.loadPreview()
			}
		default:
			ms.state, ms.res, ms.pending = mDone, msg.res, nil
		}
		return ms, nil
	case pushDoneMsg:
		ms.pushing = false
		if msg.err != nil {
			ms.err = msg.err
			return ms, nil
		}
		ms.pushed = true
		return ms, status("Pushed " + ms.res.Target)
	case editorDoneMsg:
		ms.refreshConflicts()
		return ms, nil
	case spinner.TickMsg:
		if ms.state != mRunning && !ms.pushing && ms.preview != nil {
			return ms, nil
		}
		var cmd tea.Cmd
		ms.spin, cmd = ms.spin.Update(msg)
		return ms, cmd
	case tea.KeyMsg:
		switch ms.state {
		case mConfig:
			return ms.keysConfig(msg)
		case mConflict:
			return ms.keysConflict(msg)
		case mDone:
			return ms.keysDone(msg)
		}
	}
	return ms, nil
}

func (ms *mergeScreen) keysConfig(k tea.KeyMsg) (screen, tea.Cmd) {
	ms.err = nil
	switch k.String() {
	case "esc", "q":
		return ms, pop(nil)
	case "left", "h", "shift+tab":
		ms.strat = (ms.strat + len(merge.Strategies) - 1) % len(merge.Strategies)
		return ms, ms.loadPreview()
	case "right", "l", "tab", "s":
		ms.strat = (ms.strat + 1) % len(merge.Strategies)
		return ms, ms.loadPreview()
	case "1", "2", "3", "4":
		ms.strat = int(k.String()[0] - '1')
		return ms, ms.loadPreview()
	case "t":
		return ms, ms.pickTarget()
	case "e":
		if ms.strategy() == merge.Squash {
			return ms, ms.editSquash()
		}
	case "enter":
		return ms, ms.run()
	}
	return ms, nil
}

func (ms *mergeScreen) refreshConflicts() {
	if ms.pending == nil {
		ms.pending = merge.Pending(ms.e.m)
	}
	if ms.pending != nil {
		ms.conflict = &merge.Conflict{Dir: ms.pending.Dir, Files: merge.Conflicts(ms.pending), Op: ms.pending.Strategy}
	}
	if ms.conflict != nil && ms.ccur >= len(ms.conflict.Files) {
		ms.ccur = len(ms.conflict.Files) - 1
	}
	if ms.ccur < 0 {
		ms.ccur = 0
	}
}

func (ms *mergeScreen) keysConflict(k tea.KeyMsg) (screen, tea.Cmd) {
	ms.err = nil
	files := []string{}
	if ms.conflict != nil {
		files = ms.conflict.Files
	}
	switch k.String() {
	case "esc", "q":
		return ms, pop(refreshMsg{})
	case "up", "k":
		if ms.ccur > 0 {
			ms.ccur--
		}
	case "down", "j":
		if ms.ccur < len(files)-1 {
			ms.ccur++
		}
	case "o", "enter":
		if len(files) > 0 {
			cmd := editorCmd(ms.e, filepath.Join(ms.conflict.Dir, files[ms.ccur]))
			cmd.Dir = ms.conflict.Dir
			return ms, tea.ExecProcess(cmd, func(err error) tea.Msg { return editorDoneMsg{err} })
		}
	case " ", "r":
		if len(files) > 0 && ms.pending != nil {
			if err := merge.MarkResolved(ms.pending, files[ms.ccur]); err != nil {
				ms.err = err
			}
			ms.refreshConflicts()
		}
	case "R":
		ms.refreshConflicts()
	case "c":
		ms.state = mRunning
		m := ms.e.m
		return ms, tea.Batch(ms.spin.Tick, func() tea.Msg {
			res, err := merge.Continue(m)
			return mergeDoneMsg{res, err}
		})
	case "A":
		e := ms.e
		return ms, push(newConfirm(e, "Abort merge?", "This runs git's abort and restores both branches to how they were.",
			option{label: "Abort merge", key: "y", danger: true, run: func() tea.Cmd {
				if err := merge.Abort(e.m); err != nil {
					return tea.Sequence(pop(refreshMsg{}), statusErr(err))
				}
				return tea.Sequence(pop(refreshMsg{}), status("Merge aborted"))
			}},
			option{label: "Keep resolving", key: "n"},
		))
	}
	return ms, nil
}

func (ms *mergeScreen) keysDone(k tea.KeyMsg) (screen, tea.Cmd) {
	switch k.String() {
	case "esc", "q", "enter":
		return ms, pop(refreshMsg{})
	case "p":
		if ms.res != nil && ms.res.HasRemote && !ms.pushing && !ms.pushed {
			ms.pushing, ms.err = true, nil
			m, t := ms.e.m, ms.res.Target
			return ms, tea.Batch(ms.spin.Tick, func() tea.Msg { return pushDoneMsg{merge.PushTarget(m, t)} })
		}
	case "d":
		if ms.src.Path != "" && !ms.src.Main {
			wts, _ := ms.e.m.List()
			for _, w := range wts {
				if w.Path == ms.src.Path {
					return ms, tea.Sequence(pop(refreshMsg{}), deleteFlow(ms.e, w))
				}
			}
		}
	}
	return ms, nil
}

func (ms *mergeScreen) View() string {
	e := ms.e
	w := e.width
	right := ""
	if ms.state == mRunning || ms.pushing {
		right = ms.spin.View() + " working"
	}
	header := headerBar(w, "Merge", ms.src.Branch+"  →  "+ms.target, right)
	var body, keys string
	switch ms.state {
	case mConfig:
		body, keys = ms.configView()
	case mRunning:
		body = "\n   " + ms.spin.View() + " " + ms.strategy().Label() + "…\n"
	case mConflict:
		body, keys = ms.conflictView()
	case mDone:
		body, keys = ms.doneView()
	}
	if ms.err != nil {
		body += "\n " + sErr.Render("✗ "+errText(ms.err)) + "\n"
	}
	return frame(e, header, body, keys)
}

func kv(label, value string) string {
	return "   " + sDim.Render(pad(label, 10)) + value + "\n"
}

func (ms *mergeScreen) configView() (string, string) {
	w := ms.e.width - 2
	var b strings.Builder
	b.WriteString("\n " + section("Branches", w) + "\n")
	b.WriteString(kv("source", sBold.Render(ms.src.Branch)))
	b.WriteString(kv("target", sCyan.Render(ms.target)+sDim.Render("   t to choose another local or remote branch")))
	p := ms.preview
	if p != nil && p.TargetPath != "" {
		b.WriteString(kv("runs in", shortPath(p.TargetPath)))
	} else if p != nil && len(p.Problems) == 0 && ms.strategy() != merge.FastForward && ms.strategy() != merge.Rebase {
		b.WriteString(kv("runs in", "a temporary worktree ("+p.LocalTarget+" is not checked out)"))
	}

	b.WriteString("\n " + section("Strategy", w) + "\n")
	for i, s := range merge.Strategies {
		line := fmt.Sprintf("%d  %s", i+1, s.Label())
		if i == ms.strat {
			b.WriteString("   " + sAccent.Render("●") + " " + sAccent.Bold(true).Render(line) + "\n")
		} else {
			b.WriteString("   " + sDim.Render("○") + " " + line + "\n")
		}
	}
	if ms.strategy() == merge.Squash {
		msg := ms.squash
		if msg == "" {
			msg = "(default: list of squashed commits)"
		}
		b.WriteString(kv("message", truncate(strings.SplitN(strings.TrimSpace(msg), "\n", 2)[0], w-26)+sDim.Render("   e to edit")))
	}

	b.WriteString("\n " + section("Preview", w) + "\n")
	if p == nil {
		b.WriteString("   " + ms.spin.View() + " checking…\n")
		return b.String(), help("←/→", "strategy", "t", "target", "esc", "cancel")
	}
	for _, pr := range p.Problems {
		b.WriteString("   " + sErr.Render("✗ ") + sRed.Render(pr) + "\n")
	}
	for _, wn := range p.Warnings {
		b.WriteString("   " + sYellow.Render("! "+wn) + "\n")
	}
	if len(p.Commits) > 0 {
		stat := ""
		if p.Diffstat != "" {
			lines := strings.Split(p.Diffstat, "\n")
			stat = sDim.Render("  ·  " + strings.TrimSpace(lines[len(lines)-1]))
		}
		b.WriteString("   " + sBold.Render(fmt.Sprintf("%d commit(s)", len(p.Commits))) + stat + "\n")
		maxC := max(ms.e.height-28, 3)
		for i, c := range p.Commits {
			if i >= maxC {
				b.WriteString(sDim.Render(fmt.Sprintf("     … %d more", len(p.Commits)-maxC)) + "\n")
				break
			}
			sha, subj, _ := strings.Cut(c, " ")
			b.WriteString("     " + sYellow.Render(sha) + " " + truncate(subj, w-16) + "\n")
		}
	}
	if len(p.Problems) == 0 {
		return b.String(), help("enter", "merge", "←/→ 1-4", "strategy", "t", "target", "esc", "cancel")
	}
	return b.String(), help("←/→ 1-4", "strategy", "t", "target", "esc", "cancel")
}

func (ms *mergeScreen) conflictView() (string, string) {
	w := ms.e.width - 2
	var b strings.Builder
	keys := help("o", "open in editor", "space", "mark resolved", "c", "continue", "A", "abort", "esc", "later")
	c := ms.conflict
	if c == nil {
		return "\n   " + sDim.Render("no merge in progress") + "\n", help("esc", "back")
	}
	op := "Merge"
	if c.Op == merge.Rebase {
		op = "Rebase"
	}
	b.WriteString("\n   " + sYellow.Bold(true).Render("⚠ "+op+" stopped on conflicts") + "\n")
	b.WriteString(kv("in", shortPath(c.Dir)))
	b.WriteString("\n " + section(fmt.Sprintf("Conflicted files  %d", len(c.Files)), w) + "\n")
	if len(c.Files) == 0 {
		b.WriteString("   " + sGreen.Render("✓ All conflicts resolved.") + " Press " + sKey.Render("c") + " to continue.\n")
	}
	for i, f := range c.Files {
		if i == ms.ccur {
			b.WriteString(" " + selRow("▸ U  "+f, w) + "\n")
		} else {
			b.WriteString("   " + sRed.Render("U") + "  " + f + "\n")
		}
	}
	return b.String(), keys
}

func (ms *mergeScreen) doneView() (string, string) {
	w := ms.e.width - 2
	var b strings.Builder
	b.WriteString("\n   " + sOK.Bold(true).Render("✓ "+ms.res.Summary) + "\n")
	b.WriteString("\n " + section("Next steps", w) + "\n")
	var keys []string
	if ms.res.HasRemote {
		switch {
		case ms.pushing:
			b.WriteString("   " + ms.spin.View() + " pushing " + ms.res.Target + "…\n")
		case ms.pushed:
			b.WriteString("   " + sOK.Render("✓ pushed "+ms.res.Target) + "\n")
		default:
			b.WriteString("   " + sKey.Render("p") + "  push " + ms.res.Target + "\n")
			keys = append(keys, "p", "push")
		}
	}
	if ms.src.Path != "" && !ms.src.Main {
		b.WriteString("   " + sKey.Render("d") + "  remove worktree " + ms.src.Branch + sDim.Render(" (branch kept)") + "\n")
		keys = append(keys, "d", "remove worktree")
	}
	b.WriteString("   " + sKey.Render("enter") + "  back to worktrees\n")
	keys = append(keys, "enter", "done")
	return b.String(), help(keys...)
}
