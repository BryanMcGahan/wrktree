// Package tui implements the interactive terminal interface.
package tui

import (
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/worktree"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// env is shared by all screens.
type env struct {
	m      *worktree.Manager
	width  int
	height int
	// cdPath is written to the shell integration when the app exits.
	cdPath string
	// fallbackCD is used if the user removed the worktree they started in.
	fallbackCD string
}

// screen is one view in the navigation stack.
type screen interface {
	Init() tea.Cmd
	Update(tea.Msg) (screen, tea.Cmd)
	View() string
}

type (
	pushMsg    struct{ s screen }
	replaceMsg struct{ s screen }
	// popMsg removes the top screen and optionally delivers then to the new top.
	popMsg    struct{ then tea.Msg }
	statusMsg struct {
		text string
		err  bool
	}
	clearStatusMsg struct{ id int }
	quitMsg        struct{ cd string }
	// refreshMsg asks the list screen to reload.
	refreshMsg struct{ selectPath string }
)

func push(s screen) tea.Cmd    { return func() tea.Msg { return pushMsg{s} } }
func replace(s screen) tea.Cmd { return func() tea.Msg { return replaceMsg{s} } }
func pop(then tea.Msg) tea.Cmd { return func() tea.Msg { return popMsg{then} } }
func status(text string) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: text} }
}
func statusErr(err error) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: errText(err), err: true} }
}
func msgCmd(m tea.Msg) tea.Cmd { return func() tea.Msg { return m } }

type app struct {
	env      *env
	stack    []screen
	status   string
	statusE  bool
	statusID int
}

func (a *app) Init() tea.Cmd { return a.stack[len(a.stack)-1].Init() }

func (a *app) top() screen { return a.stack[len(a.stack)-1] }

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.env.width, a.env.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
	case pushMsg:
		a.stack = append(a.stack, msg.s)
		return a, msg.s.Init()
	case replaceMsg:
		a.stack[len(a.stack)-1] = msg.s
		return a, msg.s.Init()
	case popMsg:
		if len(a.stack) <= 1 {
			return a, tea.Quit
		}
		a.stack = a.stack[:len(a.stack)-1]
		if msg.then != nil {
			return a, msgCmd(msg.then)
		}
		return a, nil
	case statusMsg:
		a.status, a.statusE = msg.text, msg.err
		a.statusID++
		id := a.statusID
		d := 4 * time.Second
		if msg.err {
			d = 8 * time.Second
		}
		return a, tea.Tick(d, func(time.Time) tea.Msg { return clearStatusMsg{id} })
	case clearStatusMsg:
		if msg.id == a.statusID {
			a.status = ""
		}
		return a, nil
	case quitMsg:
		a.env.cdPath = msg.cd
		return a, tea.Quit
	}
	// Keys go to the top screen only. Other messages (async results, ticks)
	// are broadcast so screens below a dialog still receive their data.
	if _, ok := msg.(tea.KeyMsg); ok {
		s, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = s
		return a, cmd
	}
	var cmds []tea.Cmd
	for i := range a.stack {
		s, cmd := a.stack[i].Update(msg)
		a.stack[i] = s
		cmds = append(cmds, cmd)
	}
	return a, tea.Batch(cmds...)
}

func (a *app) View() string {
	if a.env.width == 0 {
		return ""
	}
	body := a.top().View()
	var st string
	if a.status != "" {
		if a.statusE {
			st = sErr.Render("✗ ") + sRed.Render(truncate(a.status, a.env.width-3))
		} else {
			st = sOK.Render("✓ ") + truncate(a.status, a.env.width-3)
		}
	}
	h := a.env.height - 1
	body = clampLines(body, h)
	if n := h - lipgloss.Height(body); n > 0 {
		body += strings.Repeat("\n", n)
	}
	return body + "\n" + st
}

// Result is what the TUI returns to the CLI.
type Result struct {
	CDPath string
}

func runEnv(e *env, first screen) (Result, error) {
	a := &app{env: e, stack: []screen{first}}
	p := tea.NewProgram(a, tea.WithAltScreen(), tea.WithOutput(os.Stderr))
	if _, err := p.Run(); err != nil {
		return Result{}, err
	}
	cd := e.cdPath
	if cd == "" {
		cd = e.fallbackCD
	}
	return Result{CDPath: cd}, nil
}

// Run starts the full TUI on the worktree list.
func Run(m *worktree.Manager) (Result, error) {
	e := &env{m: m}
	return runEnv(e, newList(e))
}

// RunCommit opens the commit screen for a worktree directly.
func RunCommit(m *worktree.Manager, dir string) error {
	e := &env{m: m}
	_, err := runEnv(e, newCommit(e, dir, true))
	return err
}

// editorCmd builds a command that opens the configured editor on args.
func editorCmd(e *env, args ...string) *exec.Cmd {
	argv := append(e.m.Cfg.EditorCommand(), args...)
	cmd := exec.Command(argv[0], argv[1:]...)
	return cmd
}

func errText(err error) string {
	return strings.Join(strings.Fields(git.ErrMessage(err)), " ")
}
