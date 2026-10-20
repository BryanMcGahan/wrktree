// Package testutil creates throwaway git repositories for tests.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Repo creates a repo named "app" with one commit on main and a bare "origin"
// remote. It returns the main worktree path.
func Repo(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	origin := filepath.Join(root, "origin.git")
	dir := filepath.Join(root, "app")
	Git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	Git(t, root, "init", "-q", "-b", "main", dir)
	for _, kv := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "Test"}, {"commit.gpgsign", "false"}} {
		Git(t, dir, "config", kv[0], kv[1])
	}
	Write(t, dir, "README.md", "hello\n")
	Git(t, dir, "add", ".")
	Git(t, dir, "commit", "-q", "-m", "initial")
	Git(t, dir, "remote", "add", "origin", origin)
	Git(t, dir, "push", "-q", "-u", "origin", "main")
	return dir
}

// Git runs git and fails the test on error.
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// Write writes a file relative to dir.
func Write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Commit writes a file and commits it.
func Commit(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	Write(t, dir, name, content)
	Git(t, dir, "add", name)
	Git(t, dir, "commit", "-q", "-m", msg)
}
