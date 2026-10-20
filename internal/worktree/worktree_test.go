package worktree_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/testutil"
	"github.com/BryanMcGahan/wrktree/internal/worktree"
)

func TestCreateListRemove(t *testing.T) {
	dir := testutil.Repo(t)
	m, err := worktree.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.Create(worktree.CreateOptions{Branch: "feat/login"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(dir), "app-worktrees", "feat-login")
	if res.Path != want {
		t.Fatalf("path = %s, want %s", res.Path, want)
	}
	wts, _ := m.List()
	if len(wts) != 2 || wts[1].Branch != "feat/login" {
		t.Fatalf("unexpected worktrees: %+v", wts)
	}

	// Discovery from inside the new worktree finds the same main root.
	m2, err := worktree.Open(res.Path)
	if err != nil || m2.Repo.MainRoot != dir || m2.Repo.Name != "app" {
		t.Fatalf("discover from worktree: %+v %v", m2, err)
	}

	// Dirty worktrees are refused like `git worktree remove`.
	testutil.Write(t, res.Path, "scratch.txt", "x")
	if err := m.Remove(res.Path, worktree.RemoveOptions{}); err == nil {
		t.Fatal("expected dirty removal to fail")
	}
	if err := m.Remove(res.Path, worktree.RemoveOptions{Force: 1}); err != nil {
		t.Fatal(err)
	}
	if git.PathExists(res.Path) {
		t.Fatal("worktree dir still exists")
	}
	// Branch is kept.
	if !git.LocalBranchExists(dir, "feat/login") {
		t.Fatal("branch should be kept after removal")
	}
}

func TestCreateTracksRemoteBranch(t *testing.T) {
	dir := testutil.Repo(t)
	testutil.Git(t, dir, "checkout", "-q", "-b", "shared")
	testutil.Commit(t, dir, "s.txt", "s\n", "shared work")
	testutil.Git(t, dir, "push", "-q", "origin", "shared")
	testutil.Git(t, dir, "checkout", "-q", "main")
	testutil.Git(t, dir, "branch", "-q", "-D", "shared")

	m, _ := worktree.Open(dir)
	res, err := m.Create(worktree.CreateOptions{Branch: "origin/shared"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "shared" || !strings.Contains(res.How, "tracking origin/shared") {
		t.Fatalf("unexpected result %+v", res)
	}
	if up := git.Upstream(res.Path, "shared"); up != "origin/shared" {
		t.Fatalf("upstream = %q", up)
	}
}

func TestConfigOverridesPath(t *testing.T) {
	dir := testutil.Repo(t)
	testutil.Write(t, dir, ".wrk.toml", `path = ".worktrees/{branch_slug}"`+"\n")
	testutil.Write(t, dir, ".wrk.local.toml", `base_branch = "main"`+"\n")
	m, err := worktree.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.PathFor("a/b"); got != filepath.Join(dir, ".worktrees", "a-b") {
		t.Fatal(got)
	}
	if m.BaseBranch() != "main" {
		t.Fatal(m.BaseBranch())
	}
}

func TestFindAndSearch(t *testing.T) {
	dir := testutil.Repo(t)
	m, _ := worktree.Open(dir)
	for _, b := range []string{"feature-alpha", "feature-beta", "bugfix"} {
		if _, err := m.Create(worktree.CreateOptions{Branch: b}); err != nil {
			t.Fatal(err)
		}
	}
	w, err := m.Find("bugfix")
	if err != nil || w.Branch != "bugfix" {
		t.Fatalf("find exact: %v %v", w, err)
	}
	if _, err := m.Find("feature"); err == nil {
		t.Fatal("expected ambiguity")
	}
	w, err = m.Find("ftalph")
	if err != nil || w.Branch != "feature-alpha" {
		t.Fatalf("fuzzy: %v %v", w, err)
	}
}

func TestPruneMerged(t *testing.T) {
	dir := testutil.Repo(t)
	m, _ := worktree.Open(dir)
	a, _ := m.Create(worktree.CreateOptions{Branch: "merged-one"})
	testutil.Commit(t, a.Path, "a.txt", "a\n", "a")
	b, _ := m.Create(worktree.CreateOptions{Branch: "open-one"})
	testutil.Commit(t, b.Path, "b.txt", "b\n", "b")
	testutil.Git(t, dir, "merge", "-q", "--no-ff", "-m", "merge", "merged-one")
	gone, _ := m.Create(worktree.CreateOptions{Branch: "gone"})
	os.RemoveAll(gone.Path)

	cands, err := m.PruneCandidates(worktree.PruneOptions{Merged: true})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range cands {
		got[c.Worktree.Branch] = c.Reason
	}
	if len(got) != 2 || got["merged-one"] == "" || got["gone"] != "directory missing" {
		t.Fatalf("candidates: %v", got)
	}
}

func TestPRURL(t *testing.T) {
	dir := testutil.Repo(t)
	testutil.Git(t, dir, "remote", "set-url", "origin", "git@github.com:acme/app.git")
	m, _ := worktree.Open(dir)
	u, err := m.PRURL("feat/x", "origin/release")
	if err != nil {
		t.Fatal(err)
	}
	if u != "https://github.com/acme/app/compare/release...feat/x?expand=1" {
		t.Fatal(u)
	}
}
