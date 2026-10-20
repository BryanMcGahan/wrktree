package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/hooks"
	"github.com/BryanMcGahan/wrktree/internal/merge"
	"github.com/BryanMcGahan/wrktree/internal/remote"
	"github.com/BryanMcGahan/wrktree/internal/tui"
	"github.com/BryanMcGahan/wrktree/internal/worktree"
	"github.com/spf13/cobra"
)

var execCommand = exec.Command

func commitCmd() *cobra.Command {
	var msg string
	var amend, all bool
	cmd := &cobra.Command{
		Use:               "commit [worktree]",
		Aliases:           []string{"ci"},
		Short:             "Open the commit interface (stage files, hunks or lines), or commit with -m",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorktrees,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := manager()
			if err != nil {
				return err
			}
			w, err := resolve(m, args, "Commit in which worktree?", true, func(w git.Worktree) bool { return !w.Bare && !w.Prunable })
			if err != nil {
				return err
			}
			if msg == "" {
				if all {
					if _, err := git.Run(w.Path, "add", "-A"); err != nil {
						return err
					}
				}
				return tui.RunCommit(m, w.Path)
			}
			if all {
				if _, err := git.Run(w.Path, "add", "-A"); err != nil {
					return err
				}
			}
			out, err := git.Commit(w.Path, msg, amend)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, green.Render("✓ ")+strings.SplitN(out, "\n", 2)[0])
			return nil
		},
	}
	cmd.Flags().StringVarP(&msg, "message", "m", "", "commit staged changes with this message (skips the interface)")
	cmd.Flags().BoolVar(&amend, "amend", false, "amend the previous commit (with -m)")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "stage all changes first")
	return cmd
}

func mergeCmd() *cobra.Command {
	var into, strategy, msg string
	var cont, abort, push, remove, yes bool
	cmd := &cobra.Command{
		Use:   "merge [source]",
		Short: "Merge a worktree's branch into the base (or another) branch locally",
		Long: "Merge a branch into the base branch (or --into another local or remote branch)\n" +
			"without leaving your current directory.\n\n" +
			"Strategies: merge (--no-ff merge commit), ff (fast-forward only), squash,\n" +
			"rebase (rebase source onto target, then fast-forward).\n\n" +
			"If the target is checked out in a worktree the merge runs there; otherwise a\n" +
			"temporary worktree is used. On conflicts, resolve them and run\n" +
			"`wrk merge --continue`, or `wrk merge --abort`.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorktrees,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := manager()
			if err != nil {
				return err
			}
			switch {
			case abort:
				if err := merge.Abort(m); err != nil {
					return err
				}
				fmt.Fprintln(os.Stderr, green.Render("✓ ")+"merge aborted")
				return nil
			case cont:
				res, err := merge.Continue(m)
				return finishMerge(m, res, err, nil, push, remove, yes)
			}
			if st := merge.Pending(m); st != nil {
				return fmt.Errorf("a merge of %s into %s is in progress in %s; use --continue or --abort", st.Source, st.Target, st.Dir)
			}
			s, err := merge.ParseStrategy(strategy)
			if err != nil {
				return err
			}
			w, err := resolve(m, args, "Merge which worktree?", true, func(w git.Worktree) bool { return w.Branch != "" })
			if err != nil {
				return err
			}
			if w.Branch == "" {
				return errors.New("worktree has no branch (detached HEAD)")
			}
			if into == "" {
				into = m.BaseBranch()
			}
			req := merge.Request{Source: w.Branch, SourcePath: w.Path, Target: into, Strategy: s, Message: msg}
			p := merge.GetPreview(m, req)
			fmt.Fprintf(os.Stderr, "%s %s → %s (%s)\n", bold.Render("merge"), bold.Render(w.Branch), bold.Render(into), s.Label())
			for _, c := range p.Commits {
				fmt.Fprintln(os.Stderr, "  "+dim.Render(c))
			}
			for _, wn := range p.Warnings {
				fmt.Fprintln(os.Stderr, yellow.Render("! "+wn))
			}
			if len(p.Problems) > 0 {
				return errors.New(p.Problems[0])
			}
			if !yes && interactive() && !confirm("Proceed?", true) {
				return tui.ErrCancelled
			}
			res, err := merge.Run(m, req)
			return finishMerge(m, res, err, w, push, remove, yes)
		},
	}
	f := cmd.Flags()
	f.StringVar(&into, "into", "", "target branch, local or remote e.g. origin/release (default: base branch)")
	f.StringVarP(&strategy, "strategy", "s", "merge", "merge | ff | squash | rebase")
	f.StringVarP(&msg, "message", "m", "", "commit message (merge commit or squash)")
	f.BoolVar(&cont, "continue", false, "continue after resolving conflicts")
	f.BoolVar(&abort, "abort", false, "abort a merge stopped on conflicts")
	f.BoolVar(&push, "push", false, "push the target branch afterwards")
	f.BoolVar(&remove, "remove", false, "remove the source worktree afterwards (branch kept)")
	f.BoolVarP(&yes, "yes", "y", false, "don't ask questions")
	_ = cmd.RegisterFlagCompletionFunc("into", completeBranches)
	return cmd
}

func finishMerge(m *worktree.Manager, res *merge.Result, err error, src *git.Worktree, push, remove, yes bool) error {
	var c *merge.Conflict
	if errors.As(err, &c) {
		fmt.Fprintln(os.Stderr, yellow.Render("⚠ conflicts in "+c.Dir+":"))
		for _, f := range c.Files {
			fmt.Fprintln(os.Stderr, "  "+red.Render("U ")+f)
		}
		fmt.Fprintln(os.Stderr, "Resolve them (and `git add`), then run "+bold.Render("wrk merge --continue")+" or "+bold.Render("wrk merge --abort")+".")
		return errors.New("merge stopped on conflicts")
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, green.Render("✓ ")+res.Summary)
	if res.HasRemote && (push || (!yes && interactive() && confirm("Push "+res.Target+"?", false))) {
		if err := merge.PushTarget(m, res.Target); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, green.Render("✓ ")+"pushed "+res.Target)
	}
	if src != nil && !src.Main && (remove || (!yes && interactive() && confirm("Remove worktree "+src.Name()+"? (branch kept)", false))) {
		if ok, err := runHookPlan(hooks.PreRemove(m.Cfg, src.Path), "pre_remove", yes); err != nil || !ok {
			return err
		}
		if err := m.Remove(src.Path, worktree.RemoveOptions{}); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, green.Render("✓ ")+"removed "+src.Path)
		if cur := m.Repo.Current; cur == src.Path || strings.HasPrefix(cur, src.Path+"/") {
			writeCD(m.Repo.MainRoot, false)
		}
	}
	return nil
}

func prCmd() *cobra.Command {
	var base string
	var printOnly, noPush bool
	cmd := &cobra.Command{
		Use:               "pr [worktree]",
		Short:             "Push the branch and open the pull request page in your browser",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorktrees,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := manager()
			if err != nil {
				return err
			}
			w, err := resolve(m, args, "Open a PR for which worktree?", true, func(w git.Worktree) bool { return w.Branch != "" })
			if err != nil {
				return err
			}
			if w.Branch == "" {
				return errors.New("worktree has no branch (detached HEAD)")
			}
			url, err := m.PRURL(w.Branch, base)
			if err != nil {
				return err
			}
			if printOnly {
				fmt.Println(url)
				return nil
			}
			ps := m.PushStatus(w.Path, w.Branch)
			if ps.Uncommitted {
				fmt.Fprintln(os.Stderr, yellow.Render("! worktree has uncommitted changes (not included in the PR)"))
			}
			if ps.NeedsPush && !noPush {
				what := fmt.Sprintf("%d unpushed commit(s)", ps.Unpushed)
				if ps.NoUpstream {
					what = "branch is not on " + ps.Remote + " yet"
				}
				fmt.Fprintf(os.Stderr, "%s — pushing %s\n", what, w.Branch)
				if err := m.Push(w.Path, w.Branch); err != nil {
					return err
				}
			}
			fmt.Fprintln(os.Stderr, dim.Render(url))
			return remote.OpenBrowser(url)
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "base branch for the PR, e.g. main or origin/release (default: base branch)")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the URL instead of opening it")
	cmd.Flags().BoolVar(&noPush, "no-push", false, "don't push before opening")
	_ = cmd.RegisterFlagCompletionFunc("base", completeBranches)
	return cmd
}

const shPosix = `# wrk shell integration: lets wrk change your directory.
wrk() {
  local __wrk_cd __wrk_rc
  __wrk_cd="$(mktemp "${TMPDIR:-/tmp}/wrk-cd.XXXXXX")" || return
  WRK_CD_FILE="$__wrk_cd" command wrk "$@"
  __wrk_rc=$?
  if [ -s "$__wrk_cd" ]; then
    cd -- "$(cat "$__wrk_cd")" || __wrk_rc=$?
  fi
  rm -f -- "$__wrk_cd"
  return $__wrk_rc
}
`

const shFish = `# wrk shell integration: lets wrk change your directory.
function wrk
    set -l __wrk_cd (mktemp)
    WRK_CD_FILE=$__wrk_cd command wrk $argv
    set -l __wrk_rc $status
    if test -s $__wrk_cd
        cd (cat $__wrk_cd)
    end
    rm -f $__wrk_cd
    return $__wrk_rc
end
`

func shellInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "shell-init <zsh|bash|fish>",
		Short:     "Print shell integration so wrk can cd (eval it in your shell rc)",
		Long:      "Add one of these to your shell rc:\n\n  eval \"$(wrk shell-init zsh)\"\n  eval \"$(wrk shell-init bash)\"\n  wrk shell-init fish | source",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"zsh", "bash", "fish"},
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			switch args[0] {
			case "zsh":
				fmt.Print(shPosix)
				fmt.Println("if (( $+functions[compdef] )); then")
				_ = root.GenZshCompletion(os.Stdout)
				fmt.Println("fi")
			case "bash":
				fmt.Print(shPosix)
				_ = root.GenBashCompletionV2(os.Stdout, true)
			case "fish":
				fmt.Print(shFish)
				_ = root.GenFishCompletion(os.Stdout, true)
			default:
				return fmt.Errorf("unsupported shell %q (zsh, bash, fish)", args[0])
			}
			return nil
		},
	}
}
