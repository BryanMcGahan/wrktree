package tui

import (
	"errors"
	"os"
	"strings"

	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/worktree"
	tea "github.com/charmbracelet/bubbletea"
)

// ErrCancelled is returned when the user dismisses a picker.
var ErrCancelled = errors.New("cancelled")

// PickWorktree shows a fuzzy picker of worktrees and returns the choice.
// include filters which worktrees are offered (nil = all).
func PickWorktree(m *worktree.Manager, title, query string, include func(git.Worktree) bool) (*git.Worktree, error) {
	wts, err := m.List()
	if err != nil {
		return nil, err
	}
	var items []pickItem
	home, _ := os.UserHomeDir()
	for _, w := range wts {
		if include != nil && !include(w) {
			continue
		}
		p := w.Path
		if home != "" && strings.HasPrefix(p, home) {
			p = "~" + strings.TrimPrefix(p, home)
		}
		items = append(items, pickItem{title: w.Name(), desc: p, value: w})
	}
	if len(items) == 0 {
		return nil, errors.New("no matching worktrees")
	}
	var chosen *git.Worktree
	e := &env{m: m}
	p := newPicker(e, title, func() ([]pickItem, error) { return items, nil }, func(it pickItem) tea.Cmd {
		w := it.value.(git.Worktree)
		chosen = &w
		return tea.Quit
	})
	p.input.SetValue(query)
	if _, err := runEnv(e, p); err != nil {
		return nil, err
	}
	if chosen == nil {
		return nil, ErrCancelled
	}
	return chosen, nil
}
