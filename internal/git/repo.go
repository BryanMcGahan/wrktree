package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Repo describes the repository that the current directory belongs to.
type Repo struct {
	CommonDir string // absolute path to the shared .git directory
	MainRoot  string // path of the main worktree (or bare repo dir)
	Current   string // top level of the worktree we were invoked from ("" if bare)
	Name      string // repository name
	Bare      bool
}

// ErrNotRepo is returned when not inside a git repository.
var ErrNotRepo = errors.New("not inside a git repository")

// Discover finds the repository containing dir.
func Discover(dir string) (*Repo, error) {
	common, err := Run(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, ErrNotRepo
	}
	r := &Repo{CommonDir: filepath.Clean(common)}
	if top, err := Run(dir, "rev-parse", "--show-toplevel"); err == nil {
		r.Current = filepath.Clean(top)
	}
	wts, err := ListWorktrees(dir)
	if err != nil {
		return nil, err
	}
	if len(wts) > 0 {
		r.MainRoot = wts[0].Path
		r.Bare = wts[0].Bare
	} else {
		r.MainRoot = filepath.Dir(r.CommonDir)
	}
	name := filepath.Base(r.MainRoot)
	if r.Bare {
		name = strings.TrimSuffix(name, ".git")
		if name == "" || name == "." {
			name = filepath.Base(filepath.Dir(r.MainRoot))
		}
	}
	r.Name = name
	return r, nil
}

// Dir returns a directory where repository-wide git commands can run.
func (r *Repo) Dir() string {
	if r.Current != "" {
		return r.Current
	}
	return r.MainRoot
}

// Worktree is an entry from `git worktree list`.
type Worktree struct {
	Path       string
	HEAD       string
	Branch     string // short branch name, "" if detached
	Bare       bool
	Detached   bool
	Locked     bool
	LockReason string
	Prunable   bool
	Main       bool
}

// Name returns a short display name for the worktree.
func (w Worktree) Name() string {
	if w.Branch != "" {
		return w.Branch
	}
	if w.Bare {
		return "(bare)"
	}
	if len(w.HEAD) >= 7 {
		return "(detached " + w.HEAD[:7] + ")"
	}
	return filepath.Base(w.Path)
}

// ListWorktrees parses `git worktree list --porcelain`.
func ListWorktrees(dir string) ([]Worktree, error) {
	out, err := Run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktrees(out), nil
}

func parseWorktrees(out string) []Worktree {
	var wts []Worktree
	var cur *Worktree
	flush := func() {
		if cur != nil {
			wts = append(wts, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			flush()
			continue
		}
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			flush()
			cur = &Worktree{Path: filepath.Clean(val)}
		case "HEAD":
			if cur != nil {
				cur.HEAD = val
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(val, "refs/heads/")
			}
		case "bare":
			if cur != nil {
				cur.Bare = true
			}
		case "detached":
			if cur != nil {
				cur.Detached = true
			}
		case "locked":
			if cur != nil {
				cur.Locked = true
				cur.LockReason = val
			}
		case "prunable":
			if cur != nil {
				cur.Prunable = true
			}
		}
	}
	flush()
	if len(wts) > 0 {
		wts[0].Main = true
	}
	return wts
}

// Status summarizes the state of a worktree.
type Status struct {
	Staged     int
	Unstaged   int
	Untracked  int
	Conflicts  int
	Ahead      int
	Behind     int
	Upstream   string
	CommitTime time.Time
	Subject    string
	Err        error
}

// Dirty reports whether the worktree has any changes.
func (s Status) Dirty() bool {
	return s.Staged+s.Unstaged+s.Untracked+s.Conflicts > 0
}

// GetStatus collects status information for the worktree at path.
func GetStatus(path string) Status {
	var s Status
	out, err := Run(path, "status", "--porcelain=v2", "--branch")
	if err != nil {
		s.Err = err
		return s
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.upstream "):
			s.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			var a, b int
			fmt.Sscanf(strings.TrimPrefix(line, "# branch.ab "), "+%d -%d", &a, &b)
			s.Ahead, s.Behind = a, b
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "):
			if len(line) >= 4 {
				if line[2] != '.' {
					s.Staged++
				}
				if line[3] != '.' {
					s.Unstaged++
				}
			}
		case strings.HasPrefix(line, "u "):
			s.Conflicts++
		case strings.HasPrefix(line, "? "):
			s.Untracked++
		}
	}
	if log, err := Run(path, "log", "-1", "--format=%ct%x00%s"); err == nil {
		ts, subj, _ := strings.Cut(log, "\x00")
		if n, err := strconv.ParseInt(ts, 10, 64); err == nil {
			s.CommitTime = time.Unix(n, 0)
		}
		s.Subject = subj
	}
	return s
}

// LocalBranchExists reports whether refs/heads/<name> exists.
func LocalBranchExists(dir, name string) bool {
	return Succeeds(dir, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
}

// RefExists reports whether a ref resolves to a commit.
func RefExists(dir, ref string) bool {
	return Succeeds(dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
}

// Remotes lists configured remotes.
func Remotes(dir string) []string {
	out, err := Run(dir, "remote")
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// DefaultRemote returns "origin" if present, otherwise the first remote.
func DefaultRemote(dir string) string {
	rs := Remotes(dir)
	for _, r := range rs {
		if r == "origin" {
			return r
		}
	}
	if len(rs) > 0 {
		return rs[0]
	}
	return ""
}

// RemoteBranches lists remote-tracking branches like "origin/main".
func RemoteBranches(dir string) []string {
	out, err := Run(dir, "for-each-ref", "--format=%(refname:short)%00%(symref)", "refs/remotes")
	if err != nil || out == "" {
		return nil
	}
	var res []string
	for _, line := range strings.Split(out, "\n") {
		name, sym, _ := strings.Cut(line, "\x00")
		if sym != "" || !strings.Contains(name, "/") {
			continue // skip origin/HEAD
		}
		res = append(res, name)
	}
	sort.Strings(res)
	return res
}

// LocalBranches lists local branch names.
func LocalBranches(dir string) []string {
	out, err := Run(dir, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// SplitRemoteBranch splits "origin/feat/x" into ("origin", "feat/x") if the
// prefix is a configured remote.
func SplitRemoteBranch(dir, ref string) (remote, branch string, ok bool) {
	for _, r := range Remotes(dir) {
		if strings.HasPrefix(ref, r+"/") {
			return r, strings.TrimPrefix(ref, r+"/"), true
		}
	}
	return "", "", false
}

// Upstream returns the upstream of a branch, or "".
func Upstream(dir, branch string) string {
	out, err := Run(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", branch+"@{upstream}")
	if err != nil {
		return ""
	}
	return out
}

// Count returns the number of commits in a revision range.
func Count(dir, rng string) int {
	out, err := Run(dir, "rev-list", "--count", rng)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(out)
	return n
}

// IsClean reports whether the worktree has no tracked changes.
func IsClean(path string) bool {
	out, err := Run(path, "status", "--porcelain", "--untracked-files=no")
	return err == nil && out == ""
}

// GitPath resolves a path inside the worktree's git dir (e.g. MERGE_HEAD).
func GitPath(wt, name string) string {
	p, err := Run(wt, "rev-parse", "--path-format=absolute", "--git-path", name)
	if err != nil {
		return ""
	}
	return p
}

// PathExists reports whether a path exists.
func PathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
