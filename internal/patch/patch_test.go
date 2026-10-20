package patch_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/patch"
)

func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		run(t, dir, args...)
	}
	return dir
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitFile(t *testing.T, dir, name, content string) {
	write(t, dir, name, content)
	run(t, dir, "add", name)
	run(t, dir, "commit", "-q", "-m", "add "+name)
}

func index(t *testing.T, dir, name string) string {
	t.Helper()
	cmd := exec.Command("git", "show", ":"+name)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("show :%s: %v", name, err)
	}
	return string(out)
}

func lines(n int, prefix string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString(prefix)
		b.WriteString(string(rune('a' + i - 1)))
		b.WriteString("\n")
	}
	return b.String()
}

// selectWhere selects change lines whose text satisfies pred.
func selectWhere(f *patch.File, pred func(patch.Line) bool) patch.Selection {
	sel := patch.Selection{}
	for hi, h := range f.Hunks {
		for li, l := range h.Lines {
			if l.IsChange() && pred(l) {
				sel[patch.Pos{Hunk: hi, Line: li}] = true
			}
		}
	}
	return sel
}

func stage(t *testing.T, dir, name string, staged bool, pred func(patch.Line) bool) {
	t.Helper()
	fc := git.FileChange{Path: name, X: 'M', Y: 'M'}
	d, err := git.Diff(dir, fc, staged)
	if err != nil {
		t.Fatal(err)
	}
	f, err := patch.Parse(d)
	if err != nil {
		t.Fatal(err)
	}
	p := f.Build(selectWhere(f, pred), staged)
	if p == "" {
		t.Fatal("empty patch")
	}
	if err := git.ApplyCached(dir, p, staged); err != nil {
		t.Fatalf("apply failed: %v\npatch:\n%s", err, p)
	}
}

func text(s string) func(patch.Line) bool {
	return func(l patch.Line) bool { return l.Text == s }
}

func TestStageLinesInSecondHunk(t *testing.T) {
	dir := repo(t)
	orig := lines(20, "line ")
	commitFile(t, dir, "f.txt", orig)
	mod := strings.Replace(orig, "line b\n", "line b\nNEW1\n", 1)
	mod = strings.Replace(mod, "line r\n", "CHANGED r\n", 1)
	write(t, dir, "f.txt", mod)

	stage(t, dir, "f.txt", false, func(l patch.Line) bool { return strings.Contains(l.Text, " r") })
	want := strings.Replace(orig, "line r\n", "CHANGED r\n", 1)
	if got := index(t, dir, "f.txt"); got != want {
		t.Fatalf("index mismatch:\n%s", got)
	}
}

func TestStageSingleAddAmongMany(t *testing.T) {
	dir := repo(t)
	commitFile(t, dir, "f.txt", "a\nb\nc\n")
	write(t, dir, "f.txt", "a\nx1\nx2\nx3\nb\nc\n")
	stage(t, dir, "f.txt", false, text("x2"))
	if got := index(t, dir, "f.txt"); got != "a\nx2\nb\nc\n" {
		t.Fatalf("got %q", got)
	}
}

func TestStageOnlyDeletion(t *testing.T) {
	dir := repo(t)
	commitFile(t, dir, "f.txt", "a\nb\nc\nd\n")
	write(t, dir, "f.txt", "a\nB\nd\n") // b->B, c deleted
	stage(t, dir, "f.txt", false, func(l patch.Line) bool { return l.Kind == patch.Del && l.Text == "c" })
	if got := index(t, dir, "f.txt"); got != "a\nb\nd\n" {
		t.Fatalf("got %q", got)
	}
}

func TestStagePartOfUntrackedFile(t *testing.T) {
	dir := repo(t)
	commitFile(t, dir, "base.txt", "x\n")
	write(t, dir, "new.txt", "1\n2\n3\n4\n5\n")
	if err := git.IntentToAdd(dir, "new.txt"); err != nil {
		t.Fatal(err)
	}
	stage(t, dir, "new.txt", false, func(l patch.Line) bool { return l.Text == "1" || l.Text == "2" || l.Text == "4" })
	if got := index(t, dir, "new.txt"); got != "1\n2\n4\n" {
		t.Fatalf("got %q", got)
	}
}

func TestStagePartOfDeletedFile(t *testing.T) {
	dir := repo(t)
	commitFile(t, dir, "f.txt", "a\nb\nc\n")
	os.Remove(filepath.Join(dir, "f.txt"))
	stage(t, dir, "f.txt", false, text("b"))
	if got := index(t, dir, "f.txt"); got != "a\nc\n" {
		t.Fatalf("got %q", got)
	}
}

func TestStageAppendAfterNoNewline(t *testing.T) {
	dir := repo(t)
	commitFile(t, dir, "f.txt", "a\nb")
	write(t, dir, "f.txt", "a\nb\nc\nd")
	stage(t, dir, "f.txt", false, text("c"))
	if got := index(t, dir, "f.txt"); got != "a\nb\nc\n" {
		t.Fatalf("got %q", got)
	}
}

func TestStageRemoveLastLineNoNewline(t *testing.T) {
	dir := repo(t)
	commitFile(t, dir, "f.txt", "a\nb")
	write(t, dir, "f.txt", "a\nz")
	stage(t, dir, "f.txt", false, func(l patch.Line) bool { return l.Kind == patch.Del })
	if got := index(t, dir, "f.txt"); got != "a\n" {
		t.Fatalf("got %q", got)
	}
}

func TestUnstageSomeLines(t *testing.T) {
	dir := repo(t)
	orig := lines(20, "line ")
	commitFile(t, dir, "f.txt", orig)
	mod := strings.Replace(orig, "line b\n", "line b\nNEW1\nNEW2\n", 1)
	mod = strings.Replace(mod, "line q\n", "", 1)
	mod = strings.Replace(mod, "line s\n", "CHANGED s\n", 1)
	write(t, dir, "f.txt", mod)
	run(t, dir, "add", "f.txt")

	// Unstage NEW1 and the deletion of "line q".
	stage(t, dir, "f.txt", true, func(l patch.Line) bool { return l.Text == "NEW1" || l.Text == "line q" })
	want := strings.Replace(orig, "line b\n", "line b\nNEW2\n", 1)
	want = strings.Replace(want, "line s\n", "CHANGED s\n", 1)
	if got := index(t, dir, "f.txt"); got != want {
		t.Fatalf("index mismatch:\n%s", got)
	}
}

func TestUnstagePartOfNewFile(t *testing.T) {
	dir := repo(t)
	commitFile(t, dir, "base.txt", "x\n")
	write(t, dir, "new.txt", "1\n2\n3\n")
	run(t, dir, "add", "new.txt")
	stage(t, dir, "new.txt", true, text("2"))
	if got := index(t, dir, "new.txt"); got != "1\n3\n" {
		t.Fatalf("got %q", got)
	}
}

func TestStageThenUnstageRoundTrip(t *testing.T) {
	dir := repo(t)
	orig := lines(26, "")
	commitFile(t, dir, "f.txt", orig)
	mod := strings.NewReplacer("c\n", "C\n", "k\n", "", "x\n", "x\nX2\n").Replace(orig)
	write(t, dir, "f.txt", mod)
	stage(t, dir, "f.txt", false, func(patch.Line) bool { return true })
	if got := index(t, dir, "f.txt"); got != mod {
		t.Fatalf("full stage mismatch")
	}
	stage(t, dir, "f.txt", true, func(patch.Line) bool { return true })
	if got := index(t, dir, "f.txt"); got != orig {
		t.Fatalf("full unstage mismatch:\n%s", got)
	}
}

func TestParseBinary(t *testing.T) {
	f, err := patch.Parse("diff --git a/x b/x\nindex 1..2 100644\nBinary files a/x and b/x differ\n")
	if err != nil {
		t.Fatal(err)
	}
	if !f.Binary || f.Build(patch.Selection{{0, 0}: true}, false) != "" {
		t.Fatal("binary should not produce partial patches")
	}
}
