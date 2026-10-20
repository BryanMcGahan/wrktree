// Package cli defines the wrk command-line interface.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/hooks"
	"github.com/BryanMcGahan/wrktree/internal/tui"
	"github.com/BryanMcGahan/wrktree/internal/worktree"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// Version is set at build time.
var Version = "dev"

var (
	green  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#56D364"})
	red    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#FF7B72"})
	yellow = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#E3B341"})
	dim    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#8C959F", Dark: "#6E7681"})
	bold   = lipgloss.NewStyle().Bold(true)
)

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	root := &cobra.Command{
		Use:   "wrk",
		Short: "Simple git worktree management",
		Long: "wrk makes git worktrees easy: create, delete, search and switch between them,\n" +
			"commit with line-level staging, merge locally and open pull requests.\n\n" +
			"Run without arguments to open the interactive interface.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := manager()
			if err != nil {
				return err
			}
			res, err := tui.Run(m)
			if err != nil {
				return err
			}
			if res.CDPath != "" {
				writeCD(res.CDPath, true)
			}
			return nil
		},
	}
	root.AddCommand(
		lsCmd(), newCmd(), rmCmd(), switchCmd(), openCmd(), pruneCmd(),
		commitCmd(), mergeCmd(), prCmd(), initCmd(), shellInitCmd(), configCmd(),
	)
	if err := root.Execute(); err != nil {
		if errors.Is(err, tui.ErrCancelled) {
			return 130
		}
		fmt.Fprintln(os.Stderr, red.Render("wrk: ")+git.ErrMessage(err))
		return 1
	}
	return 0
}

func manager() (*worktree.Manager, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return worktree.Open(wd)
}

// writeCD hands a directory to the shell integration. Without it, the path is
// printed to stdout when print is true so `cd "$(wrk switch)"` works.
func writeCD(path string, print bool) {
	if f := os.Getenv("WRK_CD_FILE"); f != "" {
		if err := os.WriteFile(f, []byte(path), 0o600); err == nil {
			return
		}
	}
	if print {
		fmt.Println(path)
		if isatty.IsTerminal(os.Stdout.Fd()) {
			fmt.Fprintln(os.Stderr, dim.Render("tip: add `eval \"$(wrk shell-init zsh)\"` to your shell rc so wrk can cd for you"))
		}
	}
}

func interactive() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

var stdin = bufio.NewReader(os.Stdin)

// ask prompts with single-letter choices; the first is the default.
func ask(question string, choices ...string) string {
	var labels []string
	for i, c := range choices {
		l := "[" + c[:1] + "]" + c[1:]
		if i == 0 {
			l = bold.Render(l)
		}
		labels = append(labels, l)
	}
	fmt.Fprintf(os.Stderr, "%s %s ", question, strings.Join(labels, " / "))
	line, _ := stdin.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return choices[0]
	}
	for _, c := range choices {
		if strings.HasPrefix(c, line) || line[:1] == c[:1] {
			return c
		}
	}
	return ""
}

func confirm(question string, def bool) bool {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	fmt.Fprintf(os.Stderr, "%s %s ", question, hint)
	line, _ := stdin.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return def
	}
	return line == "y" || line == "yes"
}

// runHookPlan asks every time before running hooks (unless yes is set).
// It returns false if the user aborted.
func runHookPlan(plan hooks.Plan, what string, yes bool) (bool, error) {
	if plan.Empty() {
		return true, nil
	}
	if !yes {
		if !interactive() {
			fmt.Fprintf(os.Stderr, "%s skipping %s hooks (no terminal; pass --yes to run them)\n", yellow.Render("!"), what)
			return true, nil
		}
		fmt.Fprintf(os.Stderr, "%s hooks:\n%s\n", bold.Render(what), plan.Summary())
		switch ask("Run them?", "run", "skip", "abort") {
		case "skip":
			return true, nil
		case "abort", "":
			return false, nil
		}
	}
	if err := plan.ApplyFiles(); err != nil {
		return true, err
	}
	if err := plan.RunCommands(os.Stderr, os.Stderr); err != nil {
		return true, fmt.Errorf("%s hook failed: %w", what, err)
	}
	return true, nil
}

// resolve finds a worktree from an argument, the current directory, or a picker.
func resolve(m *worktree.Manager, args []string, title string, preferCurrent bool, include func(git.Worktree) bool) (*git.Worktree, error) {
	if len(args) > 0 {
		return m.Find(args[0])
	}
	if preferCurrent {
		if w := m.Current(); w != nil && (include == nil || include(*w)) {
			return w, nil
		}
	}
	if !interactive() {
		return nil, errors.New("specify a worktree")
	}
	return tui.PickWorktree(m, title, "", include)
}

func completeWorktrees(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	m, err := manager()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	wts, _ := m.List()
	var res []string
	for _, w := range wts {
		if w.Branch != "" {
			res = append(res, w.Branch)
		}
	}
	return res, cobra.ShellCompDirectiveNoFileComp
}

func completeBranches(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	m, err := manager()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return append(git.LocalBranches(m.Dir()), git.RemoteBranches(m.Dir())...), cobra.ShellCompDirectiveNoFileComp
}
