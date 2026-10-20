// Package merge merges worktree branches locally using several strategies.
package merge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/worktree"
)

type Strategy string

const (
	MergeCommit Strategy = "merge"
	FastForward Strategy = "ff"
	Squash      Strategy = "squash"
	Rebase      Strategy = "rebase"
)

// Strategies lists all strategies in display order.
var Strategies = []Strategy{MergeCommit, FastForward, Squash, Rebase}

func (s Strategy) Label() string {
	switch s {
	case MergeCommit:
		return "Merge commit (--no-ff)"
	case FastForward:
		return "Fast-forward only"
	case Squash:
		return "Squash"
	case Rebase:
		return "Rebase, then fast-forward"
	}
	return string(s)
}

// ParseStrategy accepts strategy names and a few aliases.
func ParseStrategy(s string) (Strategy, error) {
	switch strings.ToLower(s) {
	case "merge", "no-ff", "noff", "":
		return MergeCommit, nil
	case "ff", "ff-only", "fast-forward":
		return FastForward, nil
	case "squash":
		return Squash, nil
	case "rebase":
		return Rebase, nil
	}
	return "", fmt.Errorf("unknown strategy %q (merge, ff, squash, rebase)", s)
}

// Request describes a merge of Source into Target.
type Request struct {
	Source     string // branch being merged
	SourcePath string // worktree containing Source
	Target     string // branch (local or remote-tracking, e.g. origin/release)
	Strategy   Strategy
	Message    string // commit message for squash (optional for merge)
}

// Preview summarises what a merge would do.
type Preview struct {
	LocalTarget string
	TargetPath  string // worktree where Target is checked out ("" if none)
	Commits     []string
	Diffstat    string
	Problems    []string // blocking issues
	Warnings    []string
	CanFF       bool
}

// Conflict is returned when a merge or rebase stops on conflicts.
type Conflict struct {
	Dir   string
	Files []string
	Op    Strategy
}

func (c *Conflict) Error() string {
	return fmt.Sprintf("conflicts in %d file(s) in %s", len(c.Files), c.Dir)
}

// localTarget maps "origin/x" to "x" when it names a remote-tracking branch.
func localTarget(dir, target string) (local, remoteRef string) {
	if git.LocalBranchExists(dir, target) {
		return target, ""
	}
	if _, b, ok := git.SplitRemoteBranch(dir, target); ok && git.RefExists(dir, "refs/remotes/"+target) {
		return b, target
	}
	return target, ""
}

func findWorktree(m *worktree.Manager, branch string) string {
	wts, _ := m.List()
	for _, w := range wts {
		if w.Branch == branch && !w.Bare {
			return w.Path
		}
	}
	return ""
}

// GetPreview inspects the repository without changing anything.
func GetPreview(m *worktree.Manager, r Request) *Preview {
	dir := m.Dir()
	p := &Preview{}
	local, remoteRef := localTarget(dir, r.Target)
	p.LocalTarget = local
	targetRef := local
	if !git.LocalBranchExists(dir, local) {
		if remoteRef == "" {
			if rm := m.Remote(); rm != "" && git.RefExists(dir, "refs/remotes/"+rm+"/"+local) {
				remoteRef = rm + "/" + local
			}
		}
		if remoteRef == "" {
			p.Problems = append(p.Problems, fmt.Sprintf("target branch %q not found", r.Target))
			return p
		}
		targetRef = remoteRef
		p.Warnings = append(p.Warnings, fmt.Sprintf("local branch %s will be created from %s", local, remoteRef))
	} else if remoteRef != "" {
		if n := git.Count(dir, local+".."+remoteRef); n > 0 {
			p.Warnings = append(p.Warnings, fmt.Sprintf("local %s is %d commit(s) behind %s", local, n, remoteRef))
		}
	}
	if local == r.Source {
		p.Problems = append(p.Problems, "cannot merge a branch into itself")
		return p
	}
	p.TargetPath = findWorktree(m, local)
	rng := targetRef + ".." + r.Source
	if out, err := git.Run(dir, "log", "--oneline", "--no-decorate", rng); err == nil && out != "" {
		p.Commits = strings.Split(out, "\n")
	}
	if len(p.Commits) == 0 {
		p.Problems = append(p.Problems, fmt.Sprintf("%s is already up to date with %s", targetRef, r.Source))
	}
	p.Diffstat, _ = git.Run(dir, "diff", "--stat", "--no-color", targetRef+"..."+r.Source)
	p.CanFF = git.Succeeds(dir, "merge-base", "--is-ancestor", targetRef, r.Source)
	if r.Strategy == FastForward && !p.CanFF {
		p.Problems = append(p.Problems, fmt.Sprintf("%s has diverged from %s; fast-forward is not possible", r.Source, targetRef))
	}
	if p.TargetPath != "" && !git.IsClean(p.TargetPath) {
		p.Problems = append(p.Problems, fmt.Sprintf("target worktree %s has uncommitted changes", p.TargetPath))
	}
	if r.SourcePath != "" && !git.IsClean(r.SourcePath) {
		if r.Strategy == Rebase {
			p.Problems = append(p.Problems, "source worktree has uncommitted changes; commit or stash before rebasing")
		} else {
			p.Warnings = append(p.Warnings, "source worktree has uncommitted changes (they will not be merged)")
		}
	}
	if r.Strategy == Rebase && r.SourcePath == "" {
		p.Problems = append(p.Problems, "rebase requires the source branch to be checked out in a worktree")
	}
	return p
}

// DefaultSquashMessage builds a message listing the squashed commits.
func DefaultSquashMessage(m *worktree.Manager, source, target string) string {
	dir := m.Dir()
	local, remoteRef := localTarget(dir, target)
	ref := local
	if !git.LocalBranchExists(dir, local) && remoteRef != "" {
		ref = remoteRef
	}
	body, _ := git.Run(dir, "log", "--reverse", "--format=* %s", ref+".."+source)
	return fmt.Sprintf("Squash merge %s\n\n%s\n", source, body)
}

// Result reports a successful merge.
type Result struct {
	Target     string
	TargetPath string
	Summary    string
	HasRemote  bool
}

// State is persisted while a merge is stopped on conflicts.
type State struct {
	Source     string   `json:"source"`
	SourcePath string   `json:"source_path"`
	Target     string   `json:"target"`
	Strategy   Strategy `json:"strategy"`
	Dir        string   `json:"dir"`
	Temp       bool     `json:"temp"`
	Message    string   `json:"message"`
}

func statePath(m *worktree.Manager) string {
	return filepath.Join(m.Repo.CommonDir, "wrk-merge.json")
}

// Pending returns the in-progress merge state, if any.
func Pending(m *worktree.Manager) *State {
	data, err := os.ReadFile(statePath(m))
	if err != nil {
		return nil
	}
	var s State
	if json.Unmarshal(data, &s) != nil {
		return nil
	}
	return &s
}

func saveState(m *worktree.Manager, s *State) error {
	data, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(statePath(m), data, 0o644)
}

func clearState(m *worktree.Manager) { _ = os.Remove(statePath(m)) }

// Run performs the merge. On conflicts it returns *Conflict and persists state
// so Continue or Abort can finish the job.
func Run(m *worktree.Manager, r Request) (*Result, error) {
	if Pending(m) != nil {
		return nil, errors.New("a merge is already in progress; continue or abort it first")
	}
	p := GetPreview(m, r)
	if len(p.Problems) > 0 {
		return nil, errors.New(p.Problems[0])
	}
	dir := m.Dir()
	local := p.LocalTarget
	if !git.LocalBranchExists(dir, local) {
		_, remoteRef := localTarget(dir, r.Target)
		if remoteRef == "" {
			remoteRef = m.Remote() + "/" + local
		}
		if _, err := git.Run(dir, "branch", "--track", local, remoteRef); err != nil {
			return nil, err
		}
	}
	st := &State{Source: r.Source, SourcePath: r.SourcePath, Target: local, Strategy: r.Strategy, Message: r.Message}

	switch r.Strategy {
	case FastForward:
		if err := fastForward(m, local, r.Source); err != nil {
			return nil, err
		}
	case Rebase:
		st.Dir = r.SourcePath
		if _, err := git.RunEnv(r.SourcePath, nil, []string{"GIT_EDITOR=true"}, "rebase", local); err != nil {
			return nil, conflictOr(m, st, err)
		}
		if err := fastForward(m, local, r.Source); err != nil {
			return nil, err
		}
	case MergeCommit, Squash:
		wd := p.TargetPath
		if wd == "" {
			tmp, err := tempWorktree(m, local)
			if err != nil {
				return nil, err
			}
			wd, st.Temp = tmp, true
		}
		st.Dir = wd
		var err error
		if r.Strategy == MergeCommit {
			args := []string{"merge", "--no-ff", "--no-edit"}
			if r.Message != "" {
				args = append(args, "-m", r.Message)
			}
			_, err = git.Run(wd, append(args, r.Source)...)
		} else {
			_, err = git.Run(wd, "merge", "--squash", r.Source)
			if err == nil {
				err = commitSquash(wd, st.Message, m, r)
			}
		}
		if err != nil {
			return nil, conflictOr(m, st, err)
		}
		if st.Temp {
			removeTemp(m, wd)
		}
	default:
		return nil, fmt.Errorf("unknown strategy %q", r.Strategy)
	}
	return result(m, local, r), nil
}

func result(m *worktree.Manager, local string, r Request) *Result {
	return &Result{
		Target:     local,
		TargetPath: findWorktree(m, local),
		Summary:    fmt.Sprintf("merged %s into %s (%s)", r.Source, local, r.Strategy.Label()),
		HasRemote:  m.Remote() != "",
	}
}

func commitSquash(wd, msg string, m *worktree.Manager, r Request) error {
	if msg == "" {
		msg = DefaultSquashMessage(m, r.Source, r.Target)
	}
	_, err := git.RunRaw(wd, []byte(msg), "commit", "-F", "-")
	return err
}

// conflictOr converts a failed git command into *Conflict when there are
// unmerged paths, persisting state; otherwise it cleans up and returns err.
func conflictOr(m *worktree.Manager, st *State, err error) error {
	files := conflictedFiles(st.Dir)
	if len(files) == 0 {
		if st.Temp {
			_, _ = git.Run(st.Dir, "merge", "--abort")
			removeTemp(m, st.Dir)
		}
		if st.Strategy == Rebase && rebaseInProgress(st.Dir) {
			_ = saveState(m, st)
			return &Conflict{Dir: st.Dir, Op: st.Strategy}
		}
		return err
	}
	if serr := saveState(m, st); serr != nil {
		return serr
	}
	return &Conflict{Dir: st.Dir, Files: files, Op: st.Strategy}
}

func conflictedFiles(dir string) []string {
	out, err := git.Run(dir, "diff", "--name-only", "--diff-filter=U")
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func rebaseInProgress(dir string) bool {
	return git.PathExists(git.GitPath(dir, "rebase-merge")) || git.PathExists(git.GitPath(dir, "rebase-apply"))
}

// Conflicts returns the currently conflicted files of a pending merge.
func Conflicts(s *State) []string { return conflictedFiles(s.Dir) }

// MarkResolved stages a conflicted file.
func MarkResolved(s *State, file string) error {
	_, err := git.Run(s.Dir, "add", "--", file)
	return err
}

// Continue resumes a merge stopped on conflicts.
func Continue(m *worktree.Manager) (*Result, error) {
	st := Pending(m)
	if st == nil {
		return nil, errors.New("no merge in progress")
	}
	if files := conflictedFiles(st.Dir); len(files) > 0 {
		return nil, &Conflict{Dir: st.Dir, Files: files, Op: st.Strategy}
	}
	env := []string{"GIT_EDITOR=true"}
	switch st.Strategy {
	case MergeCommit:
		if _, err := git.RunEnv(st.Dir, nil, env, "commit", "--no-edit"); err != nil {
			return nil, err
		}
	case Squash:
		r := Request{Source: st.Source, Target: st.Target}
		if err := commitSquash(st.Dir, st.Message, m, r); err != nil {
			return nil, err
		}
	case Rebase:
		if _, err := git.RunEnv(st.Dir, nil, env, "rebase", "--continue"); err != nil {
			if files := conflictedFiles(st.Dir); len(files) > 0 {
				return nil, &Conflict{Dir: st.Dir, Files: files, Op: st.Strategy}
			}
			return nil, err
		}
		if err := fastForward(m, st.Target, st.Source); err != nil {
			clearState(m)
			return nil, err
		}
	}
	if st.Temp {
		removeTemp(m, st.Dir)
	}
	clearState(m)
	return result(m, st.Target, Request{Source: st.Source, Target: st.Target, Strategy: st.Strategy}), nil
}

// Abort cancels a merge stopped on conflicts.
func Abort(m *worktree.Manager) error {
	st := Pending(m)
	if st == nil {
		return errors.New("no merge in progress")
	}
	var err error
	switch st.Strategy {
	case MergeCommit:
		_, err = git.Run(st.Dir, "merge", "--abort")
	case Squash:
		_, err = git.Run(st.Dir, "reset", "--merge")
	case Rebase:
		_, err = git.Run(st.Dir, "rebase", "--abort")
	}
	if st.Temp {
		removeTemp(m, st.Dir)
	}
	clearState(m)
	return err
}

// fastForward moves target to source, which must be a descendant.
func fastForward(m *worktree.Manager, target, source string) error {
	if tw := findWorktree(m, target); tw != "" {
		_, err := git.Run(tw, "merge", "--ff-only", source)
		return err
	}
	_, err := git.Run(m.Dir(), "fetch", ".", "refs/heads/"+source+":refs/heads/"+target)
	return err
}

func tempWorktree(m *worktree.Manager, branch string) (string, error) {
	base, err := os.MkdirTemp("", "wrk-merge-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(base, worktree.Slug(branch))
	if _, err := git.Run(m.Dir(), "worktree", "add", "--quiet", path, branch); err != nil {
		os.RemoveAll(base)
		return "", err
	}
	return path, nil
}

func removeTemp(m *worktree.Manager, path string) {
	_, _ = git.Run(m.Repo.MainRoot, "worktree", "remove", "--force", path)
	_ = os.RemoveAll(filepath.Dir(path))
}

// PushTarget pushes the target branch to the remote.
func PushTarget(m *worktree.Manager, target string) error {
	dir := findWorktree(m, target)
	if dir == "" {
		dir = m.Dir()
	}
	return m.Push(dir, target)
}
