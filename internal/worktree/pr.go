package worktree

import (
	"errors"
	"fmt"

	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/BryanMcGahan/wrktree/internal/remote"
)

// PushState describes whether a branch needs pushing before opening a PR.
type PushState struct {
	Remote      string
	Upstream    string
	Unpushed    int
	NeedsPush   bool
	NoUpstream  bool
	Uncommitted bool
}

// PushStatus inspects branch in the worktree at path.
func (m *Manager) PushStatus(path, branch string) PushState {
	s := PushState{Remote: m.Remote()}
	s.Upstream = git.Upstream(path, branch)
	if s.Upstream == "" {
		s.NoUpstream = true
		s.NeedsPush = true
		s.Unpushed = git.Count(path, branch)
		if s.Remote != "" && git.RefExists(path, "refs/remotes/"+s.Remote+"/"+branch) {
			s.Unpushed = git.Count(path, s.Remote+"/"+branch+".."+branch)
		}
	} else {
		s.Unpushed = git.Count(path, s.Upstream+".."+branch)
		s.NeedsPush = s.Unpushed > 0
	}
	s.Uncommitted = !git.IsClean(path)
	return s
}

// Push pushes branch, setting upstream if it has none.
func (m *Manager) Push(path, branch string) error {
	r := m.Remote()
	if r == "" {
		return errors.New("no git remote configured")
	}
	if git.Upstream(path, branch) == "" {
		_, err := git.Run(path, "push", "-u", r, branch)
		return err
	}
	_, err := git.Run(path, "push", r, branch)
	return err
}

// BaseName strips a remote prefix ("origin/main" -> "main").
func (m *Manager) BaseName(ref string) string {
	if _, b, ok := git.SplitRemoteBranch(m.Dir(), ref); ok && !git.LocalBranchExists(m.Dir(), ref) {
		return b
	}
	return ref
}

// PRURL builds the web URL for opening a PR from branch into base.
func (m *Manager) PRURL(branch, base string) (string, error) {
	r := m.Remote()
	if r == "" {
		return "", errors.New("no git remote configured")
	}
	raw, err := git.Run(m.Dir(), "remote", "get-url", r)
	if err != nil {
		return "", err
	}
	info, err := remote.Parse(raw)
	if err != nil {
		return "", err
	}
	if base == "" {
		base = m.BaseBranch()
	}
	base = m.BaseName(base)
	if base == branch {
		return "", fmt.Errorf("branch and base are both %q", base)
	}
	return info.PRURL(branch, base, m.Cfg.PRURL)
}
