// Package hooks copies files and runs commands around worktree lifecycle events.
package hooks

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BryanMcGahan/wrktree/internal/config"
)

// Plan is the set of actions to run after creating a worktree.
type Plan struct {
	Src      string // main worktree to copy from
	Dst      string // new worktree
	Copy     []string
	Symlink  []string
	Commands []string
}

// PostCreate builds the post-create plan. Glob patterns are expanded against src.
func PostCreate(cfg *config.Config, src, dst string) Plan {
	return Plan{
		Src:      src,
		Dst:      dst,
		Copy:     expand(src, cfg.Hooks.Copy),
		Symlink:  expand(src, cfg.Hooks.Symlink),
		Commands: cfg.Hooks.PostCreate,
	}
}

// PreRemove builds the pre-remove plan (commands only).
func PreRemove(cfg *config.Config, wt string) Plan {
	return Plan{Dst: wt, Commands: cfg.Hooks.PreRemove}
}

func expand(root string, patterns []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range patterns {
		matches, _ := filepath.Glob(filepath.Join(root, p))
		for _, m := range matches {
			rel, err := filepath.Rel(root, m)
			if err != nil || seen[rel] {
				continue
			}
			seen[rel] = true
			out = append(out, rel)
		}
	}
	return out
}

// Empty reports whether there is nothing to do.
func (p Plan) Empty() bool {
	return len(p.Copy)+len(p.Symlink)+len(p.Commands) == 0
}

// Summary describes the plan for a confirmation prompt.
func (p Plan) Summary() string {
	var b strings.Builder
	for _, f := range p.Copy {
		fmt.Fprintf(&b, "  copy     %s\n", f)
	}
	for _, f := range p.Symlink {
		fmt.Fprintf(&b, "  symlink  %s\n", f)
	}
	for _, c := range p.Commands {
		fmt.Fprintf(&b, "  run      %s\n", c)
	}
	return strings.TrimRight(b.String(), "\n")
}

// ApplyFiles performs the copy and symlink steps.
func (p Plan) ApplyFiles() error {
	for _, rel := range p.Copy {
		if err := copyPath(filepath.Join(p.Src, rel), filepath.Join(p.Dst, rel)); err != nil {
			return fmt.Errorf("copy %s: %w", rel, err)
		}
	}
	for _, rel := range p.Symlink {
		dst := filepath.Join(p.Dst, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		_ = os.RemoveAll(dst)
		if err := os.Symlink(filepath.Join(p.Src, rel), dst); err != nil {
			return fmt.Errorf("symlink %s: %w", rel, err)
		}
	}
	return nil
}

// Script returns the commands joined into a single shell script that stops on
// the first failure.
func (p Plan) Script() string {
	var b strings.Builder
	b.WriteString("set -e\n")
	for _, c := range p.Commands {
		fmt.Fprintf(&b, "printf '\\033[2m$ %%s\\033[0m\\n' %s\n%s\n", shellQuote(c), c)
	}
	return b.String()
}

// Command returns an exec.Cmd running the plan's commands in Dst.
func (p Plan) Command() *exec.Cmd {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	cmd := exec.Command(sh, "-c", p.Script())
	cmd.Dir = p.Dst
	return cmd
}

// RunCommands runs the commands attached to the given streams.
func (p Plan) RunCommands(stdout, stderr io.Writer) error {
	if len(p.Commands) == 0 {
		return nil
	}
	cmd := p.Command()
	cmd.Stdin = os.Stdin
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func copyPath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.IsDir():
		return filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, path)
			target := filepath.Join(dst, rel)
			if fi.IsDir() {
				return os.MkdirAll(target, fi.Mode().Perm())
			}
			return copyPath(path, target)
		})
	case info.Mode()&os.ModeSymlink != 0:
		link, err := os.Readlink(src)
		if err != nil {
			return err
		}
		_ = os.Remove(dst)
		return os.Symlink(link, dst)
	default:
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
}
