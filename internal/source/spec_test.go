package source

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestParseSpec(t *testing.T) {
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		in   string
		want Spec
	}{
		{"bonkey/skills", Spec{URL: "https://github.com/bonkey/skills.git", Repo: true}},
		{"bonkey/skills@captains-log", Spec{URL: "https://github.com/bonkey/skills.git", Skill: "captains-log", Repo: true}},
		{"bonkey/skills/skills/pr", Spec{URL: "https://github.com/bonkey/skills.git", Path: "skills/pr", Repo: true}},
		{"bonkey/skills#v2", Spec{URL: "https://github.com/bonkey/skills.git", Ref: "v2", Repo: true}},
		{"bonkey/skills#v2@pr", Spec{URL: "https://github.com/bonkey/skills.git", Ref: "v2", Skill: "pr", Repo: true}},
		{"github:bonkey/skills", Spec{URL: "https://github.com/bonkey/skills.git", Repo: true}},
		{"https://github.com/bonkey/skills", Spec{URL: "https://github.com/bonkey/skills.git", Repo: true}},
		{"https://github.com/bonkey/skills.git#main", Spec{URL: "https://github.com/bonkey/skills.git", Ref: "main", Repo: true}},
		{"https://github.com/bonkey/skills/tree/main", Spec{URL: "https://github.com/bonkey/skills.git", Ref: "main", Repo: true}},
		{"https://github.com/bonkey/skills/tree/main/skills/pr/", Spec{URL: "https://github.com/bonkey/skills.git", Ref: "main", Path: "skills/pr", Repo: true}},
		{"https://gitlab.com/group/sub/repo", Spec{URL: "https://gitlab.com/group/sub/repo.git", Repo: true}},
		{"gitlab:group/repo", Spec{URL: "https://gitlab.com/group/repo.git", Repo: true}},
		{"https://git.example.com/group/repo/-/tree/dev/skills", Spec{URL: "https://git.example.com/group/repo.git", Ref: "dev", Path: "skills", Repo: true}},
		{"git@github.com:bonkey/skills.git", Spec{URL: "git@github.com:bonkey/skills.git", Repo: true}},
		{"ssh://git@host/path/repo.git#dev", Spec{URL: "ssh://git@host/path/repo.git", Ref: "dev", Repo: true}},
		{"https://git.example.com/repo.git", Spec{URL: "https://git.example.com/repo.git", Repo: true}},
		{"https://mcp.tavily.com/mcp/?key=a#b", Spec{URL: "https://mcp.tavily.com/mcp/?key=a#b"}},
		{"./local", Spec{URL: filepath.Join(cwd, "local")}},
		{"/abs/repo", Spec{URL: "/abs/repo"}},
	}
	for _, tt := range tests {
		got, err := ParseSpec(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("ParseSpec(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []string{"nonsense", "", "bonkey/skills/../etc"} {
		if got, err := ParseSpec(bad); err == nil {
			t.Errorf("ParseSpec(%q) = %+v; want an error", bad, got)
		}
	}
}

func TestIsRepo(t *testing.T) {
	if !IsRepo(remote(t)) {
		t.Error("a git repository is a repository")
	}
	if IsRepo(t.TempDir()) {
		t.Error("a plain directory is not a repository")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer server.Close()
	if IsRepo(server.URL + "/mcp") {
		t.Error("a server that answers every request is not a repository")
	}
}

func TestSameRepo(t *testing.T) {
	same := [][2]string{
		{"https://github.com/bonkey/skills.git", "https://github.com/bonkey/skills"},
		{"https://github.com/Bonkey/Skills/", "git@github.com:bonkey/skills.git"},
		{"ssh://git@github.com/bonkey/skills.git", "https://github.com/bonkey/skills.git"},
	}
	for _, pair := range same {
		if !SameRepo(pair[0], pair[1]) {
			t.Errorf("%s and %s are one repository", pair[0], pair[1])
		}
	}
	if SameRepo("https://github.com/bonkey/skills.git", "https://github.com/bonkey/skills-private.git") {
		t.Error("different repositories")
	}
}
