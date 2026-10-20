package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/BryanMcGahan/wrktree/internal/config"
	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/hooks"
	"github.com/BryanMcGahan/wrktree/internal/tui"
	"github.com/BryanMcGahan/wrktree/internal/worktree"
	"github.com/spf13/cobra"
)

func lsCmd() *cobra.Command {
	var asJSON, noStatus bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List worktrees with their status",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := manager()
			if err != nil {
				return err
			}
			wts, err := m.List()
			if err != nil {
				return err
			}
			sts := make([]git.Status, len(wts))
			if !noStatus {
				var wg sync.WaitGroup
				for i, w := range wts {
					if w.Bare || w.Prunable {
						continue
					}
					wg.Add(1)
					go func(i int, p string) {
						defer wg.Done()
						sts[i] = git.GetStatus(p)
					}(i, w.Path)
				}
				wg.Wait()
			}
			if asJSON {
				type out struct {
					Path      string `json:"path"`
					Branch    string `json:"branch,omitempty"`
					Head      string `json:"head"`
					Main      bool   `json:"main"`
					Locked    bool   `json:"locked"`
					Prunable  bool   `json:"prunable"`
					Dirty     bool   `json:"dirty"`
					Ahead     int    `json:"ahead"`
					Behind    int    `json:"behind"`
					Upstream  string `json:"upstream,omitempty"`
					Subject   string `json:"subject,omitempty"`
					Committed string `json:"committed,omitempty"`
				}
				var res []out
				for i, w := range wts {
					s := sts[i]
					o := out{Path: w.Path, Branch: w.Branch, Head: w.HEAD, Main: w.Main, Locked: w.Locked, Prunable: w.Prunable,
						Dirty: s.Dirty(), Ahead: s.Ahead, Behind: s.Behind, Upstream: s.Upstream, Subject: s.Subject}
					if !s.CommitTime.IsZero() {
						o.Committed = s.CommitTime.Format(time.RFC3339)
					}
					res = append(res, o)
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			cur := m.Repo.Current
			for i, w := range wts {
				mark := " "
				if w.Path == cur {
					mark = green.Render("*")
				}
				state := ""
				s := sts[i]
				switch {
				case w.Prunable:
					state = red.Render("missing")
				case w.Bare:
					state = dim.Render("bare")
				case noStatus:
				case s.Dirty():
					state = yellow.Render(fmt.Sprintf("dirty(+%d ~%d ?%d)", s.Staged, s.Unstaged, s.Untracked))
				default:
					state = green.Render("clean")
				}
				ab := ""
				if s.Ahead > 0 {
					ab += fmt.Sprintf("↑%d", s.Ahead)
				}
				if s.Behind > 0 {
					ab += fmt.Sprintf("↓%d", s.Behind)
				}
				fmt.Fprintf(tw, "%s %s\t%s\t%s\t%s\n", mark, bold.Render(w.Name()), state, ab, dim.Render(w.Path))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	cmd.Flags().BoolVar(&noStatus, "no-status", false, "skip status collection (faster)")
	return cmd
}

func newCmd() *cobra.Command {
	var o worktree.CreateOptions
	var yes, noHooks, noCD bool
	cmd := &cobra.Command{
		Use:     "new <branch>",
		Aliases: []string{"add", "create"},
		Short:   "Create a worktree for a new, existing or remote branch (or a PR)",
		Long: "Create a worktree.\n\n" +
			"  wrk new feat/login            new branch from the base branch\n" +
			"  wrk new feat/login --from dev new branch from dev\n" +
			"  wrk new existing-branch       check out an existing local branch\n" +
			"  wrk new origin/their-branch   track a remote branch\n" +
			"  wrk new --pr 123              check out pull request #123",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeBranches,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				o.Branch = args[0]
				if strings.HasPrefix(o.Branch, "#") && o.PR == 0 {
					fmt.Sscanf(o.Branch[1:], "%d", &o.PR)
					o.Branch = ""
				}
			}
			if o.Branch == "" && o.PR == 0 {
				return fmt.Errorf("branch name required")
			}
			m, err := manager()
			if err != nil {
				return err
			}
			res, err := m.Create(o)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "%s %s %s\n", green.Render("✓"), res.Path, dim.Render("("+res.How+")"))
			if !noHooks {
				src := m.Repo.MainRoot
				if m.Repo.Bare && m.Repo.Current != "" {
					src = m.Repo.Current
				}
				if _, err := runHookPlan(hooks.PostCreate(m.Cfg, src, res.Path), "post_create", yes); err != nil {
					fmt.Fprintln(os.Stderr, red.Render("✗ ")+err.Error())
				}
			}
			if !noCD {
				writeCD(res.Path, false)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.From, "from", "", "start point for a new branch (default: base branch)")
	f.StringVar(&o.Path, "path", "", "override the worktree directory")
	f.IntVar(&o.PR, "pr", 0, "create from a pull request number")
	f.BoolVar(&o.Fetch, "fetch", false, "fetch the remote first")
	f.BoolVarP(&yes, "yes", "y", false, "run hooks without asking")
	f.BoolVar(&noHooks, "no-hooks", false, "don't run hooks")
	f.BoolVar(&noCD, "no-cd", false, "don't cd into the new worktree (with shell integration)")
	return cmd
}

func rmCmd() *cobra.Command {
	var force int
	var yes, noHooks bool
	cmd := &cobra.Command{
		Use:     "rm [worktree]",
		Aliases: []string{"remove", "delete"},
		Short:   "Remove a worktree (like git worktree remove; the branch is kept)",
		Long: "Remove a worktree exactly like `git worktree remove`.\n\n" +
			"Refuses if the worktree has modified or untracked files (use --force) or is\n" +
			"locked (use --force twice). The branch is never deleted.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorktrees,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := manager()
			if err != nil {
				return err
			}
			w, err := resolve(m, args, "Remove which worktree?", false, func(w git.Worktree) bool { return !w.Main && !w.Bare })
			if err != nil {
				return err
			}
			if w.Main {
				return fmt.Errorf("'%s' is the main worktree and cannot be removed", w.Path)
			}
			if !noHooks {
				ok, err := runHookPlan(hooks.PreRemove(m.Cfg, w.Path), "pre_remove", yes)
				if err != nil {
					return err
				}
				if !ok {
					return tui.ErrCancelled
				}
			}
			if err := m.Remove(w.Path, worktree.RemoveOptions{Force: force}); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "%s removed %s %s\n", green.Render("✓"), w.Path, dim.Render("(branch "+w.Name()+" kept)"))
			if cur := m.Repo.Current; cur != "" && (cur == w.Path || strings.HasPrefix(cur, w.Path+string(filepath.Separator))) {
				writeCD(m.Repo.MainRoot, false)
			}
			return nil
		},
	}
	cmd.Flags().CountVarP(&force, "force", "f", "remove even if dirty; twice to remove a locked worktree")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "run pre_remove hooks without asking")
	cmd.Flags().BoolVar(&noHooks, "no-hooks", false, "don't run hooks")
	return cmd
}

func switchCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "switch [query]",
		Aliases:           []string{"cd", "sw"},
		Short:             "Fuzzy-pick a worktree and cd into it (prints the path without shell integration)",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorktrees,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := manager()
			if err != nil {
				return err
			}
			query := ""
			if len(args) > 0 {
				query = args[0]
				if w, err := m.Find(query); err == nil {
					writeCD(w.Path, true)
					return nil
				}
			}
			if !interactive() && os.Getenv("WRK_CD_FILE") == "" {
				return fmt.Errorf("no unique match for %q", query)
			}
			w, err := tui.PickWorktree(m, "Switch to worktree", query, func(w git.Worktree) bool { return !w.Bare && !w.Prunable })
			if err != nil {
				return err
			}
			writeCD(w.Path, true)
			return nil
		},
	}
}

func openCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "open [worktree]",
		Short:             "Open a worktree in your editor",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorktrees,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := manager()
			if err != nil {
				return err
			}
			w, err := resolve(m, args, "Open which worktree?", true, func(w git.Worktree) bool { return !w.Bare && !w.Prunable })
			if err != nil {
				return err
			}
			argv := append(m.Cfg.EditorCommand(), w.Path)
			c := execCommand(argv[0], argv[1:]...)
			c.Dir = w.Path
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			return c.Run()
		},
	}
}

func pruneCmd() *cobra.Command {
	var dry, yes bool
	var stale, into string
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove worktrees that are merged, stale or missing",
		Long: "Find worktrees whose branch is merged into the base branch, whose directory\n" +
			"is missing, or (with --stale) that have had no commits for a while, and\n" +
			"remove them with `git worktree remove`. Dirty worktrees are skipped and\n" +
			"branches are kept.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := manager()
			if err != nil {
				return err
			}
			o := worktree.PruneOptions{Merged: true, Into: into}
			if stale != "" {
				if o.Stale, err = worktree.ParseDuration(stale); err != nil {
					return err
				}
			}
			cands, err := m.PruneCandidates(o)
			if err != nil {
				return err
			}
			if len(cands) == 0 {
				fmt.Fprintln(os.Stderr, "Nothing to prune.")
				return m.PruneMetadata()
			}
			for _, c := range cands {
				fmt.Fprintf(os.Stderr, "  %s  %s\n", bold.Render(c.Worktree.Name()), dim.Render(c.Reason))
			}
			if dry {
				return nil
			}
			if !yes {
				if !interactive() {
					return fmt.Errorf("pass --yes to prune non-interactively")
				}
				if !confirm(fmt.Sprintf("Remove %d worktree(s)?", len(cands)), false) {
					return tui.ErrCancelled
				}
			}
			for _, c := range cands {
				if c.Worktree.Prunable {
					continue
				}
				if err := m.Remove(c.Worktree.Path, worktree.RemoveOptions{}); err != nil {
					fmt.Fprintf(os.Stderr, "%s skipped %s: %s\n", yellow.Render("!"), c.Worktree.Name(), git.ErrMessage(err))
				} else {
					fmt.Fprintf(os.Stderr, "%s removed %s\n", green.Render("✓"), c.Worktree.Name())
				}
			}
			return m.PruneMetadata()
		},
	}
	cmd.Flags().BoolVarP(&dry, "dry-run", "n", false, "only show what would be removed")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	cmd.Flags().StringVar(&stale, "stale", "", "also remove worktrees with no commits for this long (e.g. 30d, 2w)")
	cmd.Flags().StringVar(&into, "into", "", "branch to check merges against (default: base branch)")
	return cmd
}

func initCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a starter .wrk.toml in the repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := manager()
			if err != nil {
				return err
			}
			root := m.Repo.Current
			if root == "" {
				root = m.Repo.MainRoot
			}
			p := filepath.Join(root, config.ProjectFile)
			if git.PathExists(p) && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", p)
			}
			if err := os.WriteFile(p, []byte(config.Starter), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "%s wrote %s\n", green.Render("✓"), p)
			fmt.Fprintln(os.Stderr, dim.Render("  personal overrides go in "+config.LocalFile+" (add it to .gitignore)"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	return cmd
}

func configCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Show the effective configuration and where it came from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := manager()
			if err != nil {
				return err
			}
			c := m.Cfg
			fmt.Println(bold.Render("sources"))
			if len(c.Sources) == 0 {
				fmt.Println("  (defaults only)")
			}
			for _, s := range c.Sources {
				fmt.Println("  " + s)
			}
			fmt.Println(dim.Render("  global: " + config.GlobalPath()))
			fmt.Println()
			kv := func(k, v string) { fmt.Printf("%-13s %s\n", k, v) }
			kv("path", c.Path+dim.Render("  → "+m.PathFor("feat/example")))
			kv("base_branch", m.BaseBranch())
			kv("remote", m.Remote())
			kv("editor", strings.Join(c.EditorCommand(), " "))
			if c.PRURL != "" {
				kv("pr_url", c.PRURL)
			}
			kv("copy", strings.Join(c.Hooks.Copy, ", "))
			kv("symlink", strings.Join(c.Hooks.Symlink, ", "))
			kv("post_create", strings.Join(c.Hooks.PostCreate, " && "))
			kv("pre_remove", strings.Join(c.Hooks.PreRemove, " && "))
			return nil
		},
	}
}
