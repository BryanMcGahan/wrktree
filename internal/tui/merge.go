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
	var b strings.Builder
	b.WriteString(sTitle.Render("Merge") + sDim.Render(" · ") + sBold.Render(ms.src.Branch) + sDim.Render(" → ") + sCyan.Render(ms.target) + "\n\n")
	switch ms.state {
	case mConfig:
		b.WriteString(ms.configView())
	case mRunning:
		b.WriteString(ms.spin.View() + " " + ms.strategy().Label() + "…\n")
	case mConflict:
		b.WriteString(ms.conflictView())
	case mDone:
		b.WriteString(ms.doneView())
	}
	if ms.err != nil {
		b.WriteString("\n" + sErr.Render(errText(ms.err)) + "\n")
	}
	return padBox(b.String())
}

func (ms *mergeScreen) configView() string {
	var b strings.Builder
	b.WriteString(sDim.Render("target   ") + sCyan.Render(ms.target) + sDim.Render("   (t to choose another local or remote branch)") + "\n\n")
	b.WriteString(sDim.Render("strategy") + "\n")
	for i, s := range merge.Strategies {
		mark := "  ○ "
		line := fmt.Sprintf("%d %s", i+1, s.Label())
		if i == ms.strat {
			mark = sAccent.Render("  ● ")
			line = sAccent.Bold(true).Render(line)
		}
		b.WriteString(mark + line + "\n")
	}
	if ms.strategy() == merge.Squash {
		msg := ms.squash
		if msg == "" {
			msg = "(default: list of squashed commits)"
		}
		b.WriteString("\n" + sDim.Render("message  ") + truncate(strings.SplitN(strings.TrimSpace(msg), "\n", 2)[0], ms.e.width-14) + sDim.Render("  (e to edit)") + "\n")
	}
	b.WriteString("\n")
	p := ms.preview
	if p == nil {
		b.WriteString(ms.spin.View() + " checking…\n")
		return b.String()
	}
	if p.TargetPath != "" {
		b.WriteString(sDim.Render("runs in  ") + p.TargetPath + "\n")
	} else if len(p.Problems) == 0 && ms.strategy() != merge.FastForward && ms.strategy() != merge.Rebase {
		b.WriteString(sDim.Render("runs in  ") + "a temporary worktree (" + p.LocalTarget + " is not checked out)\n")
	}
	for _, pr := range p.Problems {
		b.WriteString(sErr.Render("✗ ") + sRed.Render(pr) + "\n")
	}
	for _, w := range p.Warnings {
		b.WriteString(sYellow.Render("! "+w) + "\n")
	}
	if len(p.Commits) > 0 {
		b.WriteString("\n" + sBold.Render(fmt.Sprintf("%d commit(s)", len(p.Commits))) + "\n")
		maxC := ms.e.height - 24
		if maxC < 3 {
			maxC = 3
		}
		for i, c := range p.Commits {
			if i >= maxC {
				b.WriteString(sDim.Render(fmt.Sprintf("  … %d more", len(p.Commits)-maxC)) + "\n")
				break
			}
			sha, subj, _ := strings.Cut(c, " ")
			b.WriteString("  " + sYellow.Render(sha) + " " + truncate(subj, ms.e.width-14) + "\n")
		}
		if p.Diffstat != "" {
			lines := strings.Split(p.Diffstat, "\n")
			b.WriteString(sDim.Render(strings.TrimSpace(lines[len(lines)-1])) + "\n")
		}
	}
	b.WriteString("\n")
	if len(p.Problems) == 0 {
		b.WriteString(help("enter", "merge", "←/→", "strategy", "t", "target", "esc", "cancel"))
	} else {
		b.WriteString(help("←/→", "strategy", "t", "target", "esc", "cancel"))
	}
	return b.String()
}

func (ms *mergeScreen) conflictView() string {
	var b strings.Builder
	c := ms.conflict
	if c == nil {
		return sDim.Render("no merge in progress") + "\n"
	}
	op := "merge"
	if c.Op == merge.Rebase {
		op = "rebase"
	}
	b.WriteString(sYellow.Render(fmt.Sprintf("⚠ %s stopped on conflicts", op)) + sDim.Render(" in "+c.Dir) + "\n\n")
	if len(c.Files) == 0 {
		b.WriteString(sGreen.Render("All conflicts resolved.") + " Press " + sKey.Render("c") + " to continue.\n")
	}
	for i, f := range c.Files {
		if i == ms.ccur {
			b.WriteString(selRow("▸ U "+f, ms.e.width-4) + "\n")
		} else {
			b.WriteString("  " + sRed.Render("U") + " " + f + "\n")
		}
	}
	b.WriteString("\n" + help("o", "open in editor", "space", "mark resolved", "c", "continue", "A", "abort", "esc", "later"))
	return b.String()
}

func (ms *mergeScreen) doneView() string {
	var b strings.Builder
	b.WriteString(sOK.Render("✓ "+ms.res.Summary) + "\n\n")
	var keys []string
	if ms.res.HasRemote {
		switch {
		case ms.pushing:
			b.WriteString(ms.spin.View() + " pushing " + ms.res.Target + "…\n")
		case ms.pushed:
			b.WriteString(sOK.Render("✓ pushed "+ms.res.Target) + "\n")
		default:
			keys = append(keys, "p", "push "+ms.res.Target)
		}
	}
	if ms.src.Path != "" && !ms.src.Main {
		keys = append(keys, "d", "remove worktree "+ms.src.Branch)
	}
	keys = append(keys, "enter", "done")
	b.WriteString("\n" + help(keys...))
	return b.String()
}
