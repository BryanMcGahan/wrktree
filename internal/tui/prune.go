package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/worktree"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type (
	pruneLoadedMsg struct {
		c   []worktree.PruneCandidate
		err error
	}
	pruneDoneMsg struct {
		removed []string
		failed  []string
	}
)

var staleSteps = []time.Duration{0, 14 * 24 * time.Hour, 30 * 24 * time.Hour, 90 * 24 * time.Hour}

type pruneScreen struct {
	e       *env
	cands   []worktree.PruneCandidate
	checked map[int]bool
	cur     int
	stale   int
	loading bool
	busy    bool
	done    *pruneDoneMsg
	err     error
	spin    spinner.Model
}

func newPrune(e *env) *pruneScreen {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = sAccent
	return &pruneScreen{e: e, checked: map[int]bool{}, spin: sp, stale: 0}
}

func (p *pruneScreen) load() tea.Cmd {
	p.loading = true
	m, stale := p.e.m, staleSteps[p.stale]
	return tea.Batch(p.spin.Tick, func() tea.Msg {
		c, err := m.PruneCandidates(worktree.PruneOptions{Merged: true, Stale: stale})
		return pruneLoadedMsg{c, err}
	})
}

func (p *pruneScreen) Init() tea.Cmd { return p.load() }

func (p *pruneScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case pruneLoadedMsg:
		p.loading = false
		p.cands, p.err = msg.c, msg.err
		p.checked = map[int]bool{}
		for i, c := range p.cands {
			// Pre-select merged and missing; stale ones need an explicit choice.
			p.checked[i] = !strings.HasPrefix(c.Reason, "no commits")
		}
		p.cur = 0
		return p, nil
	case pruneDoneMsg:
		p.busy = false
		p.done = &msg
		return p, p.load()
	case spinner.TickMsg:
		if !p.loading && !p.busy {
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
		case "up", "k":
			if p.cur > 0 {
				p.cur--
			}
		case "down", "j":
			if p.cur < len(p.cands)-1 {
				p.cur++
			}
		case " ":
			p.checked[p.cur] = !p.checked[p.cur]
		case "a":
			all := true
			for i := range p.cands {
				all = all && p.checked[i]
			}
			for i := range p.cands {
				p.checked[i] = !all
			}
		case "s":
			p.stale = (p.stale + 1) % len(staleSteps)
			return p, p.load()
		case "enter":
			return p, p.confirm()
		}
	}
	return p, nil
}

func (p *pruneScreen) selected() []worktree.PruneCandidate {
	var res []worktree.PruneCandidate
	for i, c := range p.cands {
		if p.checked[i] {
			res = append(res, c)
		}
	}
	return res
}

func (p *pruneScreen) confirm() tea.Cmd {
	sel := p.selected()
	if len(sel) == 0 {
		return nil
	}
	var names []string
	for _, c := range sel {
		names = append(names, "  "+c.Worktree.Name())
	}
	m := p.e.m
	run := func() tea.Cmd {
		p.busy = true
		return tea.Batch(p.spin.Tick, func() tea.Msg {
			var done pruneDoneMsg
			for _, c := range sel {
				if c.Worktree.Prunable {
					continue // handled by git worktree prune below
				}
				if err := m.Remove(c.Worktree.Path, worktree.RemoveOptions{}); err != nil {
					done.failed = append(done.failed, c.Worktree.Name()+": "+git.ErrMessage(err))
				} else {
					done.removed = append(done.removed, c.Worktree.Name())
				}
			}
			for _, c := range sel {
				if c.Worktree.Prunable {
					done.removed = append(done.removed, c.Worktree.Name())
				}
			}
			if err := m.PruneMetadata(); err != nil {
				done.failed = append(done.failed, "git worktree prune: "+git.ErrMessage(err))
			}
			return done
		})
	}
	return push(newConfirm(p.e, fmt.Sprintf("Remove %d worktree(s)?", len(sel)),
		strings.Join(names, "\n")+"\n\n"+sDim.Render("Uses `git worktree remove` (no force): worktrees with changes are skipped. Branches are kept."),
		option{label: "Remove", key: "y", danger: true, run: run},
		option{label: "Cancel", key: "n"},
	))
}

func (p *pruneScreen) View() string {
	e := p.e
	w := e.width - 2
	staleLabel := "off"
	if d := staleSteps[p.stale]; d > 0 {
		staleLabel = worktree.HumanDuration(d)
	}
	right := ""
	if p.loading || p.busy {
		right = p.spin.View() + " working"
	}
	header := headerBar(e.width, "Prune worktrees", "merged into "+e.m.BaseBranch()+"  ·  missing  ·  stale: "+staleLabel, right)
	var b strings.Builder
	if p.done != nil {
		b.WriteString("\n " + section("Last run", w) + "\n")
		for _, r := range p.done.removed {
			b.WriteString("   " + sOK.Render("✓ removed "+r) + "\n")
		}
		for _, f := range p.done.failed {
			b.WriteString("   " + sYellow.Render("! skipped "+f) + "\n")
		}
	}
	selected := len(p.selected())
	b.WriteString("\n " + section(fmt.Sprintf("Candidates  %d selected of %d", selected, len(p.cands)), w) + "\n")
	switch {
	case p.loading:
		b.WriteString("   " + p.spin.View() + " finding candidates…\n")
	case p.err != nil:
		b.WriteString("   " + sErr.Render(errText(p.err)) + "\n")
	case len(p.cands) == 0:
		b.WriteString("   " + sDim.Render("Nothing to prune.") + "\n")
	}
	nameW := 12
	for _, c := range p.cands {
		nameW = max(nameW, lipgloss.Width(c.Worktree.Name())+2)
	}
	nameW = min(nameW, w/2)
	for i, c := range p.cands {
		box := "[ ]"
		if p.checked[i] {
			box = "[x]"
		}
		if i == p.cur {
			b.WriteString(" " + selRow("▸ "+box+" "+pad(c.Worktree.Name(), nameW)+c.Reason, w) + "\n")
		} else {
			mark := sDim.Render(box)
			if p.checked[i] {
				mark = sAccent.Render(box)
			}
			b.WriteString("   " + mark + " " + pad(c.Worktree.Name(), nameW) + sDim.Render(c.Reason) + "\n")
		}
	}
	if p.busy {
		b.WriteString("\n   " + p.spin.View() + " removing…\n")
	}
	return frame(e, header, b.String(), help("space", "toggle", "a", "all", "s", "stale threshold", "enter", "remove selected", "esc", "back"))
}
