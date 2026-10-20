package merge_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/merge"
	"github.com/BryanMcGahan/wrktree/internal/testutil"
	"github.com/BryanMcGahan/wrktree/internal/worktree"
)

func setup(t *testing.T) (*worktree.Manager, string, string) {
	dir := testutil.Repo(t)
	m, err := worktree.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.Create(worktree.CreateOptions{Branch: "feat"})
	if err != nil {
		t.Fatal(err)
	}
	testutil.Commit(t, res.Path, "a.txt", "a\n", "add a")
	testutil.Commit(t, res.Path, "b.txt", "b\n", "add b")
	return m, dir, res.Path
}

func subjects(t *testing.T, dir, rng string) []string {
	out := strings.TrimSpace(testutil.Git(t, dir, "log", "--format=%s", rng))
	return strings.Split(out, "\n")
}

func TestStrategies(t *testing.T) {
	for _, s := range merge.Strategies {
		t.Run(string(s), func(t *testing.T) {
			m, dir, wt := setup(t)
			res, err := merge.Run(m, merge.Request{Source: "feat", SourcePath: wt, Target: "main", Strategy: s})
			if err != nil {
				t.Fatal(err)
			}
			if res.TargetPath != dir {
				t.Fatalf("target path %s", res.TargetPath)
			}
			got := subjects(t, dir, "origin/main..main")
			switch s {
			case merge.MergeCommit:
				if len(got) != 3 || !strings.HasPrefix(got[0], "Merge branch 'feat'") {
					t.Fatalf("%v", got)
				}
			case merge.FastForward, merge.Rebase:
				if len(got) != 2 || got[0] != "add b" {
					t.Fatalf("%v", got)
				}
			case merge.Squash:
				if len(got) != 1 || got[0] != "Squash merge feat" {
					t.Fatalf("%v", got)
				}
			}
			if !git.PathExists(dir + "/b.txt") {
				t.Fatal("main worktree not updated")
			}
		})
	}
}

func TestMergeIntoUncheckedOutRemoteBranch(t *testing.T) {
	m, dir, wt := setup(t)
	testutil.Git(t, dir, "push", "-q", "origin", "main:release")
	testutil.Git(t, dir, "fetch", "-q", "origin")
	res, err := merge.Run(m, merge.Request{Source: "feat", SourcePath: wt, Target: "origin/release", Strategy: merge.MergeCommit})
	if err != nil {
		t.Fatal(err)
	}
	if res.Target != "release" || res.TargetPath != "" {
		t.Fatalf("%+v", res)
	}
	if up := git.Upstream(dir, "release"); up != "origin/release" {
		t.Fatalf("upstream %q", up)
	}
	if n := git.Count(dir, "origin/release..release"); n != 3 {
		t.Fatalf("expected 3 new commits on release, got %d", n)
	}
	wts, _ := m.List()
	if len(wts) != 2 {
		t.Fatalf("temp worktree not cleaned up: %+v", wts)
	}
}

func TestConflictContinueAndAbort(t *testing.T) {
	for _, s := range []merge.Strategy{merge.MergeCommit, merge.Squash, merge.Rebase} {
		t.Run(string(s), func(t *testing.T) {
			m, dir, wt := setup(t)
			testutil.Commit(t, wt, "README.md", "feat\n", "feat readme")
			testutil.Commit(t, dir, "README.md", "main\n", "main readme")

			req := merge.Request{Source: "feat", SourcePath: wt, Target: "main", Strategy: s}
			_, err := merge.Run(m, req)
			var c *merge.Conflict
			if !errors.As(err, &c) || len(c.Files) != 1 || c.Files[0] != "README.md" {
				t.Fatalf("expected conflict, got %v", err)
			}
			if merge.Pending(m) == nil {
				t.Fatal("state not saved")
			}
			if _, err := merge.Continue(m); !errors.As(err, &c) {
				t.Fatalf("continue with conflicts should fail, got %v", err)
			}

			// Abort restores things.
			if err := merge.Abort(m); err != nil {
				t.Fatal(err)
			}
			if merge.Pending(m) != nil || !git.IsClean(dir) || !git.IsClean(wt) {
				t.Fatal("abort left a mess")
			}

			// Retry and resolve.
			if _, err := merge.Run(m, req); !errors.As(err, &c) {
				t.Fatalf("expected conflict again: %v", err)
			}
			testutil.Write(t, c.Dir, "README.md", "resolved\n")
			st := merge.Pending(m)
			if err := merge.MarkResolved(st, "README.md"); err != nil {
				t.Fatal(err)
			}
			res, err := merge.Continue(m)
			if err != nil {
				t.Fatal(err)
			}
			if res.Target != "main" || merge.Pending(m) != nil {
				t.Fatalf("%+v", res)
			}
			out := testutil.Git(t, dir, "show", "main:README.md")
			if out != "resolved\n" {
				t.Fatalf("README on main = %q", out)
			}
		})
	}
}

func TestPreviewProblems(t *testing.T) {
	m, dir, wt := setup(t)
	testutil.Commit(t, dir, "m.txt", "m\n", "diverge")
	p := merge.GetPreview(m, merge.Request{Source: "feat", SourcePath: wt, Target: "main", Strategy: merge.FastForward})
	if len(p.Problems) == 0 || !strings.Contains(p.Problems[0], "fast-forward") {
		t.Fatalf("%+v", p.Problems)
	}
	testutil.Write(t, dir, "README.md", "dirty\n")
	p = merge.GetPreview(m, merge.Request{Source: "feat", SourcePath: wt, Target: "main", Strategy: merge.MergeCommit})
	if len(p.Problems) == 0 || !strings.Contains(p.Problems[0], "uncommitted") {
		t.Fatalf("%+v", p.Problems)
	}
}
