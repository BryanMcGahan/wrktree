package tui

import (
	"github.com/BryanMcGahan/wrktree/internal/hooks"
	"github.com/BryanMcGahan/wrktree/internal/worktree"
	tea "github.com/charmbracelet/bubbletea"
)

type (
	createdMsg   struct{ res *worktree.CreateResult }
	hooksDoneMsg struct {
		what string
		err  error
		then tea.Cmd
	}
)

func hookSource(e *env) string {
	if e.m.Repo.Bare && e.m.Repo.Current != "" {
		return e.m.Repo.Current
	}
	return e.m.Repo.MainRoot
}

func preRemovePlan(e *env, path string) hooks.Plan {
	return hooks.PreRemove(e.m.Cfg, path)
}

// postCreateHooks asks (every time) whether to run the configured hooks.
func postCreateHooks(e *env, path string) tea.Cmd {
	plan := hooks.PostCreate(e.m.Cfg, hookSource(e), path)
	if plan.Empty() {
		return nil
	}
	return push(newConfirm(e, "Run post-create hooks?", plan.Summary()+"\n\n"+sDim.Render("in "+path),
		option{label: "Run", key: "r", run: func() tea.Cmd { return runHooks(plan, "post_create", nil) }},
		option{label: "Skip", key: "s"},
	))
}

// runHooks applies file steps, then hands the terminal to the shell commands.
// The output stays visible until the user presses enter.
func runHooks(plan hooks.Plan, what string, then tea.Cmd) tea.Cmd {
	if err := plan.ApplyFiles(); err != nil {
		return msgCmd(hooksDoneMsg{what: what, err: err})
	}
	if len(plan.Commands) == 0 {
		return msgCmd(hooksDoneMsg{what: what, then: then})
	}
	cmd := plan.Command()
	script := cmd.Args[len(cmd.Args)-1]
	cmd.Args[len(cmd.Args)-1] = "(\n" + script + "\n); rc=$?; printf '\\n\\033[1m[wrk] " + what +
		" hooks finished (exit %d). Press enter to return.\\033[0m' $rc; read _; exit $rc"
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return hooksDoneMsg{what: what, err: err}
		}
		return hooksDoneMsg{what: what, then: then}
	})
}
