package remote

import "testing"

func TestPRURL(t *testing.T) {
	cases := []struct{ remote, branch, base, want string }{
		{"git@github.com:acme/app.git", "feat/x", "main", "https://github.com/acme/app/compare/main...feat/x?expand=1"},
		{"https://github.com/acme/app", "fix", "release/1.0", "https://github.com/acme/app/compare/release/1.0...fix?expand=1"},
		{"ssh://git@github.example.com:2222/acme/app.git", "b", "main", "https://github.example.com/acme/app/compare/main...b?expand=1"},
		{"git@gitlab.com:group/sub/app.git", "feat/x", "main", "https://gitlab.com/group/sub/app/-/merge_requests/new?merge_request%5Bsource_branch%5D=feat%2Fx&merge_request%5Btarget_branch%5D=main"},
		{"git@bitbucket.org:acme/app.git", "b", "main", "https://bitbucket.org/acme/app/pull-requests/new?source=b&dest=main"},
		{"git@ssh.dev.azure.com:v3/org/proj/app", "b", "main", "https://dev.azure.com/org/proj/_git/app/pullrequestcreate?sourceRef=b&targetRef=main"},
		{"https://org@dev.azure.com/org/proj/_git/app", "b", "main", "https://dev.azure.com/org/proj/_git/app/pullrequestcreate?sourceRef=b&targetRef=main"},
	}
	for _, c := range cases {
		in, err := Parse(c.remote)
		if err != nil {
			t.Fatalf("%s: %v", c.remote, err)
		}
		got, err := in.PRURL(c.branch, c.base, "")
		if err != nil {
			t.Fatalf("%s: %v", c.remote, err)
		}
		if got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.remote, got, c.want)
		}
	}
}

func TestPRURLTemplateAndUnknown(t *testing.T) {
	in, _ := Parse("git@git.example.com:team/app.git")
	if _, err := in.PRURL("b", "main", ""); err == nil {
		t.Fatal("expected error for unknown host")
	}
	got, _ := in.PRURL("feat/x", "main", "{web}/pulls/new?from={branch}&to={base}")
	if got != "https://git.example.com/team/app/pulls/new?from=feat%2Fx&to=main" {
		t.Fatal(got)
	}
}
