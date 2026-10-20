// Package worktree implements high-level worktree operations.
package worktree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BryanMcGahan/wrktree/internal/config"
	"github.com/BryanMcGahan/wrktree/internal/git"
	"github.com/sahilm/fuzzy"
)

// Manager ties a repository and its configuration together.
type Manager struct {
	Repo *git.Repo
	Cfg  *config.Config
}

// Open discovers the repository containing dir and loads its configuration.
func Open(dir string) (*Manager, error) {
	repo, err := git.Discover(dir)
	if err != nil {
		return nil, err
	}
	cfgRoot := repo.MainRoot
	if repo.Bare && repo.Current != "" {
		cfgRoot = repo.Current
	}
	cfg, err := config.Load(cfgRoot)
	if err != nil {
		return nil, err
	}
	return &Manager{Repo: repo, Cfg: cfg}, nil
}

// Dir is where repository-wide git commands run.
func (m *Manager) Dir() string { return m.Repo.Dir() }

// Remote returns the configured or default remote name.
func (m *Manager) Remote() string {
	if m.Cfg.Remote != "" {
		return m.Cfg.Remote
	}
	return git.DefaultRemote(m.Dir())
}

// BaseBranch returns the configured base branch, or a detected default.
func (m *Manager) BaseBranch() string {
	if m.Cfg.BaseBranch != "" {
		return m.Cfg.BaseBranch
	}
	dir := m.Dir()
	if r := m.Remote(); r != "" {
		if out, err := git.Run(dir, "symbolic-ref", "--short", "refs/remotes/"+r+"/HEAD"); err == nil {
			return strings.TrimPrefix(out, r+"/")
		}
	}
	for _, b := range []string{"main", "master", "trunk", "develop"} {
		if git.LocalBranchExists(dir, b) {
			return b
		}
	}
	if out, err := git.Run(m.Repo.MainRoot, "symbolic-ref", "--short", "HEAD"); err == nil {
		return out
	}
	return "main"
}

// List returns all worktrees.
func (m *Manager) List() ([]git.Worktree, error) {
	return git.ListWorktrees(m.Dir())
}

var slugRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Slug turns a branch name into a single safe path component.
func Slug(branch string) string {
	s := slugRe.ReplaceAllString(strings.ReplaceAll(branch, "/", "-"), "-")
	return strings.Trim(s, "-.")
}

// PathFor computes the worktree directory for a branch using the config template.
func (m *Manager) PathFor(branch string) string {
	home, _ := os.UserHomeDir()
	p := m.Cfg.Path
	if p == "" {
		p = config.DefaultPath
	}
	p = strings.NewReplacer(
		"{repo}", m.Repo.Name,
		"{branch_slug}", Slug(branch),
		"{branch}", branch,
		"{home}", home,
	).Replace(p)
	if strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[2:])
	}
	if !filepath.IsAbs(p) {
		root := m.Repo.MainRoot
		if m.Repo.Bare {
			root = filepath.Dir(root) // place siblings next to a bare repo, not inside
		}
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p)
}

// CreateOptions configures worktree creation.
type CreateOptions struct {
	Branch string // branch name to check out or create
	From   string // start point for a new branch (default: base branch)
	Path   string // override path
	PR     int    // create from a pull request number
	Fetch  bool   // fetch the remote before creating
}

// CreateResult describes what Create did.
type CreateResult struct {
	Path   string
	Branch string
	How    string // human-readable description
}

// Create adds a worktree. It reuses an existing local branch, tracks a matching
// remote branch, or creates a new branch from the start point.
func (m *Manager) Create(o CreateOptions) (*CreateResult, error) {
	dir := m.Dir()
	remote := m.Remote()
	if o.Fetch && remote != "" {
		if _, err := git.Run(dir, "fetch", "--prune", remote); err != nil {
			return nil, err
		}
	}
	if o.PR > 0 {
		return m.createFromPR(o)
	}
	branch := strings.TrimSpace(o.Branch)
	if branch == "" {
		return nil, errors.New("branch name required")
	}
	// Allow "origin/feature" to mean "track remote feature".
	if !git.LocalBranchExists(dir, branch) && o.From == "" {
		if r, b, ok := git.SplitRemoteBranch(dir, branch); ok && git.RefExists(dir, "refs/remotes/"+branch) {
			_ = r
			branch = b
		}
	}
	if err := checkBranchName(dir, branch); err != nil {
		return nil, err
	}
	path := o.Path
	if path == "" {
		path = m.PathFor(branch)
	}
	if git.PathExists(path) {
		if entries, _ := os.ReadDir(path); len(entries) > 0 {
			return nil, fmt.Errorf("path already exists and is not empty: %s", path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	var args []string
	var how string
	switch {
	case git.LocalBranchExists(dir, branch):
		if o.From != "" {
			return nil, fmt.Errorf("branch %q already exists; drop --from to check it out", branch)
		}
		args = []string{"worktree", "add", path, branch}
		how = "checked out existing branch " + branch
	case o.From == "" && remote != "" && git.RefExists(dir, "refs/remotes/"+remote+"/"+branch):
		args = []string{"worktree", "add", "--track", "-b", branch, path, remote + "/" + branch}
		how = "tracking " + remote + "/" + branch
	default:
		from := o.From
		if from == "" {
			from = m.resolveStart(m.BaseBranch())
		}
		if !git.RefExists(dir, from) {
			return nil, fmt.Errorf("start point %q not found", from)
		}
		args = []string{"worktree", "add", "--no-track", "-b", branch, path, from}
		how = "new branch " + branch + " from " + from
	}
	if _, err := git.Run(dir, args...); err != nil {
		return nil, err
	}
	return &CreateResult{Path: path, Branch: branch, How: how}, nil
}

// resolveStart picks the local base branch if it exists, else the remote one.
func (m *Manager) resolveStart(base string) string {
	dir := m.Dir()
	if git.LocalBranchExists(dir, base) {
		return base
	}
	if r := m.Remote(); r != "" && git.RefExists(dir, "refs/remotes/"+r+"/"+base) {
		return r + "/" + base
	}
	if git.RefExists(dir, base) {
		return base
	}
	return "HEAD"
}

func checkBranchName(dir, branch string) error {
	if _, err := git.Run(dir, "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("invalid branch name %q", branch)
	}
	return nil
}

func (m *Manager) createFromPR(o CreateOptions) (*CreateResult, error) {
	dir := m.Dir()
	remote := m.Remote()
	if remote == "" {
		return nil, errors.New("no git remote configured")
	}
	branch := o.Branch
	if branch == "" {
		branch = PRHeadBranch(dir, o.PR)
	}
	if branch == "" {
		branch = fmt.Sprintf("pr-%d", o.PR)
	}
	url, _ := git.Run(dir, "remote", "get-url", remote)
	refspecs := []string{fmt.Sprintf("pull/%d/head", o.PR), fmt.Sprintf("merge-requests/%d/head", o.PR)}
	if strings.Contains(url, "gitlab") {
		refspecs[0], refspecs[1] = refspecs[1], refspecs[0]
	}
	if git.LocalBranchExists(dir, branch) {
		return nil, fmt.Errorf("branch %q already exists; use `wrk new %s` to check it out", branch, branch)
	}
	var lastErr error
	fetched := false
	for _, rs := range refspecs {
		if _, err := git.Run(dir, "fetch", remote, rs+":"+branch); err == nil {
			fetched = true
			break
		} else {
			lastErr = err
		}
	}
	if !fetched {
		return nil, fmt.Errorf("could not fetch PR #%d: %s", o.PR, git.ErrMessage(lastErr))
	}
	path := o.Path
	if path == "" {
		path = m.PathFor(branch)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if _, err := git.Run(dir, "worktree", "add", path, branch); err != nil {
		return nil, err
	}
	return &CreateResult{Path: path, Branch: branch, How: fmt.Sprintf("PR #%d as %s", o.PR, branch)}, nil
}

// RemoveOptions mirrors `git worktree remove` flags.
type RemoveOptions struct {
	Force int // 1 = --force (dirty), 2 = --force --force (locked)
}

// Remove removes a worktree exactly like `git worktree remove`. The branch is kept.
func (m *Manager) Remove(path string, o RemoveOptions) error {
	args := []string{"worktree", "remove"}
	for i := 0; i < o.Force && i < 2; i++ {
		args = append(args, "--force")
	}
	args = append(args, path)
	// Run from the main worktree so removing the current directory works.
	dir := m.Repo.MainRoot
	if m.Repo.Bare {
		dir = m.Repo.CommonDir
	}
	_, err := git.Run(dir, args...)
	return err
}

// Find resolves a query (branch name, directory name or path) to a worktree.
func (m *Manager) Find(query string) (*git.Worktree, error) {
	wts, err := m.List()
	if err != nil {
		return nil, err
	}
	if abs, err := filepath.Abs(query); err == nil {
		for i := range wts {
			if wts[i].Path == abs {
				return &wts[i], nil
			}
		}
	}
	for i := range wts {
		if wts[i].Branch == query || filepath.Base(wts[i].Path) == query {
			return &wts[i], nil
		}
	}
	names := make([]string, len(wts))
	for i, w := range wts {
		names[i] = w.Name()
	}
	// Prefer substring matches; fall back to fuzzy matches. Either must be unique.
	var idx []int
	for i, n := range names {
		if strings.Contains(strings.ToLower(n), strings.ToLower(query)) {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		for _, mt := range fuzzy.Find(query, names) {
			idx = append(idx, mt.Index)
		}
	}
	switch len(idx) {
	case 0:
		return nil, fmt.Errorf("no worktree matches %q", query)
	case 1:
		return &wts[idx[0]], nil
	}
	var amb []string
	for _, i := range idx {
		amb = append(amb, names[i])
	}
	names = amb
	return nil, fmt.Errorf("%q is ambiguous: %s", query, strings.Join(names, ", "))
}

// Current returns the worktree containing the working directory, if any.
func (m *Manager) Current() *git.Worktree {
	if m.Repo.Current == "" {
		return nil
	}
	wts, err := m.List()
	if err != nil {
		return nil
	}
	for i := range wts {
		if wts[i].Path == m.Repo.Current {
			return &wts[i]
		}
	}
	return nil
}

// SearchKey is the text fuzzy search matches against.
func SearchKey(w git.Worktree) string {
	return w.Name() + " " + filepath.Base(w.Path)
}

// Search fuzzy-filters worktrees, best matches first.
func Search(wts []git.Worktree, query string) []git.Worktree {
	if query == "" {
		return wts
	}
	keys := make([]string, len(wts))
	for i, w := range wts {
		keys[i] = SearchKey(w)
	}
	var res []git.Worktree
	for _, mt := range fuzzy.Find(query, keys) {
		res = append(res, wts[mt.Index])
	}
	return res
}

// PruneCandidate is a worktree that prune would remove.
type PruneCandidate struct {
	Worktree git.Worktree
	Reason   string
}

// PruneOptions selects what to consider for pruning.
type PruneOptions struct {
	Merged bool
	Stale  time.Duration
	Into   string
}

// PruneCandidates finds worktrees whose branches are merged or stale.
// Missing directories are handled by `git worktree prune` and reported too.
func (m *Manager) PruneCandidates(o PruneOptions) ([]PruneCandidate, error) {
	wts, err := m.List()
	if err != nil {
		return nil, err
	}
	dir := m.Dir()
	into := o.Into
	if into == "" {
		into = m.resolveStart(m.BaseBranch())
	}
	merged := map[string]bool{}
	if o.Merged {
		out, err := git.Run(dir, "for-each-ref", "--merged", into, "--format=%(refname:short)", "refs/heads")
		if err == nil {
			for _, b := range strings.Split(out, "\n") {
				merged[b] = true
			}
		}
	}
	base := m.BaseBranch()
	var res []PruneCandidate
	for _, w := range wts {
		if w.Main || w.Bare {
			continue
		}
		switch {
		case w.Prunable:
			res = append(res, PruneCandidate{w, "directory missing"})
		case o.Merged && w.Branch != "" && w.Branch != base && merged[w.Branch] && hasOwnCommits(dir, w.Branch):
			res = append(res, PruneCandidate{w, "merged into " + into})
		case o.Stale > 0:
			st := git.GetStatus(w.Path)
			if !st.CommitTime.IsZero() && time.Since(st.CommitTime) > o.Stale {
				res = append(res, PruneCandidate{w, "no commits for " + HumanDuration(time.Since(st.CommitTime))})
			}
		}
	}
	sort.SliceStable(res, func(i, j int) bool { return res[i].Worktree.Path < res[j].Worktree.Path })
	return res, nil
}

// hasOwnCommits reports whether a branch has moved since it was created, so
// freshly created (and therefore trivially "merged") branches aren't pruned.
// Without a reflog we can't tell, so assume it has.
func hasOwnCommits(dir, branch string) bool {
	out, err := git.Run(dir, "reflog", "show", "--format=%H", "refs/heads/"+branch, "--")
	if err != nil || out == "" {
		return true
	}
	return strings.Count(out, "\n") >= 1
}

// PruneMetadata runs `git worktree prune`.
func (m *Manager) PruneMetadata() error {
	_, err := git.Run(m.Dir(), "worktree", "prune")
	return err
}

// HumanDuration formats durations like "3d", "5h", "2mo".
func HumanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/24/365))
	}
}

// ParseDuration accepts Go durations plus "d", "w", "mo" and "y" suffixes.
func ParseDuration(s string) (time.Duration, error) {
	units := []struct {
		suf string
		d   time.Duration
	}{{"mo", 30 * 24 * time.Hour}, {"y", 365 * 24 * time.Hour}, {"w", 7 * 24 * time.Hour}, {"d", 24 * time.Hour}}
	for _, u := range units {
		if strings.HasSuffix(s, u.suf) {
			var n int
			if _, err := fmt.Sscanf(strings.TrimSuffix(s, u.suf), "%d", &n); err != nil {
				return 0, fmt.Errorf("invalid duration %q", s)
			}
			return time.Duration(n) * u.d, nil
		}
	}
	return time.ParseDuration(s)
}

// PRHeadBranch asks the gh CLI for a PR's head branch name, if available.
func PRHeadBranch(dir string, pr int) string {
	out, err := runGH(dir, "pr", "view", fmt.Sprint(pr), "--json", "headRefName", "--jq", ".headRefName")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
