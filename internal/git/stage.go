package git

import (
	"strings"
)

// FileChange is one entry from `git status`.
type FileChange struct {
	Path     string
	OrigPath string // for renames
	X, Y     byte   // index and worktree status codes
}

func (f FileChange) Untracked() bool { return f.X == '?' }
func (f FileChange) Conflicted() bool {
	return f.X == 'U' || f.Y == 'U' || (f.X == 'A' && f.Y == 'A') || (f.X == 'D' && f.Y == 'D')
}
func (f FileChange) HasStaged() bool { return f.X != ' ' && f.X != '?' && !f.Conflicted() }
func (f FileChange) HasUnstaged() bool {
	return f.Y != ' ' || f.Untracked() || f.Conflicted()
}

// Changes lists changed files (porcelain v1, NUL separated).
func Changes(dir string) ([]FileChange, error) {
	out, err := RunRaw(dir, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var res []FileChange
	parts := strings.Split(out, "\x00")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if len(p) < 4 {
			continue
		}
		fc := FileChange{X: p[0], Y: p[1], Path: p[3:]}
		if fc.X == 'R' || fc.X == 'C' {
			if i+1 < len(parts) {
				fc.OrigPath = parts[i+1]
				i++
			}
		}
		res = append(res, fc)
	}
	return res, nil
}

var diffArgs = []string{"diff", "--no-color", "--no-ext-diff", "--src-prefix=a/", "--dst-prefix=b/", "-U3"}

// Diff returns the diff of a single file. staged selects the index diff.
func Diff(dir string, f FileChange, staged bool) (string, error) {
	if !staged && f.Untracked() {
		out, err := RunRaw(dir, nil, append(append([]string{}, diffArgs...), "--no-index", "--", "/dev/null", f.Path)...)
		if err != nil {
			if ge, ok := err.(*Error); ok && ge.Code == 1 {
				return out, nil // --no-index exits 1 when files differ
			}
			return out, err
		}
		return out, nil
	}
	args := append([]string{}, diffArgs...)
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", f.Path)
	if staged && f.OrigPath != "" {
		args = append(args, f.OrigPath)
	}
	return RunRaw(dir, nil, args...)
}

// StageFile stages the whole file (including deletions).
func StageFile(dir, path string) error {
	_, err := Run(dir, "add", "-A", "--", path)
	return err
}

// UnstageFile removes the file's changes from the index.
func UnstageFile(dir string, f FileChange) error {
	args := []string{"reset", "-q", "--", f.Path}
	if f.OrigPath != "" {
		args = append(args, f.OrigPath)
	}
	if _, err := Run(dir, args...); err != nil {
		// unborn branch: fall back to removing from the index
		_, err2 := Run(dir, "rm", "--cached", "-q", "--", f.Path)
		if err2 != nil {
			return err
		}
	}
	return nil
}

// IntentToAdd marks an untracked file with `git add -N` so it can be
// partially staged.
func IntentToAdd(dir, path string) error {
	_, err := Run(dir, "add", "-N", "--", path)
	return err
}

// ApplyCached applies a patch to the index.
func ApplyCached(dir, patch string, reverse bool) error {
	args := []string{"apply", "--cached", "--recount", "--whitespace=nowarn"}
	if reverse {
		args = append(args, "-R")
	}
	args = append(args, "-")
	_, err := RunRaw(dir, []byte(patch), args...)
	return err
}

// Commit creates a commit with the given message.
func Commit(dir, msg string, amend bool) (string, error) {
	args := []string{"commit", "-F", "-", "--cleanup=strip"}
	if amend {
		args = append(args, "--amend")
	}
	out, err := RunRaw(dir, []byte(msg), args...)
	return strings.TrimSpace(out), err
}

// LastCommitMessage returns the full message of HEAD.
func LastCommitMessage(dir string) string {
	out, _ := Run(dir, "log", "-1", "--format=%B")
	return strings.TrimSpace(out)
}
