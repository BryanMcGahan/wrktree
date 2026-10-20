package worktree

import (
	"encoding/json"
	"os/exec"
	"strings"
)

func runGH(dir string, args ...string) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", err
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// PR is an open pull request as reported by gh.
type PR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Head   string `json:"headRefName"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
}

// HasGH reports whether the gh CLI is installed.
func HasGH() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// ListPRs returns open PRs via gh, or nil if gh is unavailable.
func ListPRs(dir string) ([]PR, error) {
	out, err := runGH(dir, "pr", "list", "--limit", "100", "--json", "number,title,headRefName,author")
	if err != nil {
		return nil, err
	}
	var prs []PR
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &prs); err != nil {
		return nil, err
	}
	return prs, nil
}
