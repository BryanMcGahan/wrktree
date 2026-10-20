package tui

import (
	"fmt"
	"strings"

	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/remote"
	"github.com/BryanMcGahan/wrktree/internal/worktree"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type (
	prInfoMsg struct {
		ps  worktree.PushState
		url string
		err error
	}
	prOpenedMsg struct {
		url string
		err error
	}
	basePickedMsg struct{ base string }
)

type prScreen struct {
	e      *env
	wt     git.Worktree
	base   string
	info   *prInfoMsg
	busy   bool
	opened string
	err    error
	spin   spinner.Model
}

func newPR(e *env, wt git.Worktree) *prScreen {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = sAccent
	return &prScreen{e: e, wt: wt, base: e.m.BaseBranch(), spin: sp}
}

func (p *prScreen) load() tea.Cmd {
	m, wt, base := p.e.m, p.wt, p.base
	p.info = nil
	return func() tea.Msg {
		ps := m.PushStatus(wt.Path, wt.Branch)
		url, err := m.PRURL(wt.Branch, base)
		return prInfoMsg{ps: ps, url: url, err: err}
	}
}

func (p *prScreen) Init() tea.Cmd { return tea.Batch(p.spin.Tick, p.load()) }

func (p *prScreen) open() tea.Cmd {
	if p.info == nil || p.info.err != nil || p.busy {
		return nil
	}
	p.busy, p.err = true, nil
	m, wt, url, needPush := p.e.m, p.wt, p.info.url, p.info.ps.NeedsPush
	return tea.Batch(p.spin.Tick, func() tea.Msg {
		if needPush {
			if err := m.Push(wt.Path, wt.Branch); err != nil {
				return prOpenedMsg{err: err}
			}
		}
		return prOpenedMsg{url: url, err: remote.OpenBrowser(url)}
	})
}

func (p *prScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case prInfoMsg:
		p.info = &msg
		return p, nil
	case basePickedMsg:
		p.base = p.e.m.BaseName(msg.base)
		return p, p.load()
	case prOpenedMsg:
		p.busy = false
		if msg.err != nil {
			p.err = msg.err
			return p, p.load()
		}
		p.opened = msg.url
		return p, tea.Batch(p.load(), status("Opened pull request page in browser"))
	case spinner.TickMsg:
		if !p.busy && p.info != nil {
			return p, nil
		}
		var cmd tea.Cmd
		p.spin, cmd = p.spin.Update(msg)
		return p, cmd
	case tea.KeyMsg:
		if p.busy {
			return p, nil
		}
		switch msg.String() {
		case "esc", "q":
			return p, pop(refreshMsg{})
		case "enter", "o":
			return p, p.open()
		case "t", "b":
			return p, p.pickBase()
		}
	}
	return p, nil
}

func (p *prScreen) pickBase() tea.Cmd {
	m := p.e.m
	branch := p.wt.Branch
	load := func() ([]pickItem, error) {
		dir := m.Dir()
		if r := m.Remote(); r != "" {
			_, _ = git.Run(dir, "fetch", "--prune", r)
		}
		var items []pickItem
		for _, b := range git.RemoteBranches(dir) {
			if _, name, ok := git.SplitRemoteBranch(dir, b); ok && name != branch {
				items = append(items, pickItem{title: b, value: b})
			}
		}
		return items, nil
	}
	return push(newPicker(p.e, "Pull request base for "+branch, load, func(it pickItem) tea.Cmd {
		return msgCmd(basePickedMsg{it.value.(string)})
	}))
}

func (p *prScreen) View() string {
	var b strings.Builder
	b.WriteString(sTitle.Render("Pull request") + sDim.Render(" · ") + sBold.Render(p.wt.Branch) + sDim.Render(" → ") + sCyan.Render(p.base) + "\n\n")
	b.WriteString(sDim.Render("base     ") + sCyan.Render(p.base) + sDim.Render("   (t to choose another remote branch)") + "\n")
	in := p.info
	if in == nil {
		b.WriteString("\n" + p.spin.View() + " checking branch…\n")
		return padBox(b.String())
	}
	ps := in.ps
	remoteName := ps.Remote
	if remoteName == "" {
		remoteName = "(none)"
	}
	b.WriteString(sDim.Render("remote   ") + remoteName + "\n")
	switch {
	case ps.NoUpstream:
		b.WriteString(sDim.Render("push     ") + sYellow.Render(fmt.Sprintf("not on remote yet — will run git push -u %s %s", ps.Remote, p.wt.Branch)) + "\n")
	case ps.NeedsPush:
		b.WriteString(sDim.Render("push     ") + sYellow.Render(fmt.Sprintf("%d unpushed commit(s) — will push to %s", ps.Unpushed, ps.Upstream)) + "\n")
	default:
		b.WriteString(sDim.Render("push     ") + sGreen.Render("up to date with "+ps.Upstream) + "\n")
	}
	if ps.Uncommitted {
		b.WriteString(sYellow.Render("! worktree has uncommitted changes (they won't be in the PR)") + "\n")
	}
	if in.err != nil {
		b.WriteString("\n" + sErr.Render(errText(in.err)) + "\n")
	} else {
		b.WriteString("\n" + sDim.Render("url      ") + truncate(in.url, p.e.width-12) + "\n")
	}
	if p.err != nil {
		b.WriteString("\n" + sErr.Render(errText(p.err)) + "\n")
	}
	if p.busy {
		b.WriteString("\n" + p.spin.View() + " pushing and opening…\n")
	} else if p.opened != "" {
		b.WriteString("\n" + sOK.Render("✓ opened in browser") + "\n")
	}
	action := "open in browser"
	if ps.NeedsPush {
		action = "push & open in browser"
	}
	b.WriteString("\n" + help("enter", action, "t", "change base", "esc", "back"))
	return padBox(b.String())
}
