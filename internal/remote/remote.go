// Package remote turns git remote URLs into web URLs for creating pull requests.
package remote

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

// Info is a parsed remote.
type Info struct {
	Host  string // e.g. github.com
	Path  string // e.g. owner/repo (may contain subgroups)
	Web   string // e.g. https://github.com/owner/repo
	Owner string
	Repo  string
	Kind  string // github, gitlab, bitbucket, azure, unknown
}

// Parse understands scp-style, ssh://, git:// and http(s):// remote URLs.
func Parse(raw string) (*Info, error) {
	raw = strings.TrimSpace(raw)
	var host, path string
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		host = u.Hostname()
		path = u.Path
		if u.Scheme == "http" || u.Scheme == "https" {
			if p := u.Port(); p != "" {
				host = host + ":" + p
			}
		}
	case strings.Contains(raw, ":"):
		// scp-like: [user@]host:path
		hp, p, _ := strings.Cut(raw, ":")
		if at := strings.LastIndex(hp, "@"); at >= 0 {
			hp = hp[at+1:]
		}
		host, path = hp, p
	default:
		return nil, fmt.Errorf("unrecognised remote URL %q", raw)
	}
	path = strings.Trim(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/")
	if host == "" || path == "" {
		return nil, fmt.Errorf("unrecognised remote URL %q", raw)
	}
	in := &Info{Host: host, Path: path}
	lh := strings.ToLower(host)
	switch {
	case strings.Contains(lh, "dev.azure.com") || strings.Contains(lh, "visualstudio.com"):
		in.Kind = "azure"
		parts := strings.Split(path, "/")
		// ssh: v3/org/project/repo ; https: org/project/_git/repo
		if len(parts) == 4 && parts[0] == "v3" {
			in.Web = fmt.Sprintf("https://dev.azure.com/%s/%s/_git/%s", parts[1], parts[2], parts[3])
			in.Owner, in.Repo = parts[1], parts[3]
		} else {
			in.Web = "https://" + strings.TrimPrefix(lh, "ssh.") + "/" + path
			in.Owner, in.Repo = parts[0], parts[len(parts)-1]
		}
		return in, nil
	case strings.Contains(lh, "github"):
		in.Kind = "github"
	case strings.Contains(lh, "gitlab"):
		in.Kind = "gitlab"
	case strings.Contains(lh, "bitbucket"):
		in.Kind = "bitbucket"
	default:
		in.Kind = "unknown"
	}
	webHost := strings.TrimPrefix(host, "ssh.")
	in.Web = "https://" + webHost + "/" + path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		in.Owner, in.Repo = path[:i], path[i+1:]
	} else {
		in.Repo = path
	}
	return in, nil
}

// PRURL builds the "create pull request" URL for branch -> base. If template is
// non-empty it is used instead of the built-in host patterns.
func (in *Info) PRURL(branch, base, template string) (string, error) {
	q := url.QueryEscape
	if template != "" {
		return strings.NewReplacer(
			"{web}", in.Web, "{owner}", in.Owner, "{repo}", in.Repo,
			"{branch}", q(branch), "{base}", q(base),
		).Replace(template), nil
	}
	switch in.Kind {
	case "github":
		return fmt.Sprintf("%s/compare/%s...%s?expand=1", in.Web, pathEscape(base), pathEscape(branch)), nil
	case "gitlab":
		return fmt.Sprintf("%s/-/merge_requests/new?merge_request%%5Bsource_branch%%5D=%s&merge_request%%5Btarget_branch%%5D=%s",
			in.Web, q(branch), q(base)), nil
	case "bitbucket":
		return fmt.Sprintf("%s/pull-requests/new?source=%s&dest=%s", in.Web, q(branch), q(base)), nil
	case "azure":
		return fmt.Sprintf("%s/pullrequestcreate?sourceRef=%s&targetRef=%s", in.Web, q(branch), q(base)), nil
	}
	return "", fmt.Errorf("don't know how to create PRs on %s; set pr_url in .wrk.toml", in.Host)
}

func pathEscape(s string) string {
	parts := strings.Split(s, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// OpenBrowser opens a URL with the platform's default handler.
func OpenBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}
