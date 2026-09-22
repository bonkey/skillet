package catalog

import (
	"reflect"
	"strings"
	"testing"
)

func TestRepoParts(t *testing.T) {
	tests := map[string][2]string{
		"https://github.com/acme/skills.git":  {"acme", "skills"},
		"https://github.com/acme/skills":      {"acme", "skills"},
		"https://github.com/acme/skills.git/": {"acme", "skills"},
		"git@github.com:acme/skills.git":      {"acme", "skills"},
		"ssh://git@host:7999/acme/skills.git": {"acme", "skills"},
		"/tmp/remotes/acme/skills":            {"acme", "skills"},
	}
	for url, want := range tests {
		owner, repo, ok := RepoParts(url)
		if !ok || owner != want[0] || repo != want[1] {
			t.Errorf("%s: got %s/%s %v, want %s/%s", url, owner, repo, ok, want[0], want[1])
		}
	}
	if _, _, ok := RepoParts("nothing"); ok {
		t.Error("a single segment is not a repository")
	}
}

func TestSourceNames(t *testing.T) {
	plannotator := &Source{URL: "https://github.com/backnotprop/plannotator.git"}
	bonkey := &Source{URL: "https://github.com/bonkey/skills.git"}
	wondel := &Source{URL: "https://github.com/wondelai/skills.git"}
	named := &Source{Name: "wondel", URL: "https://github.com/wondelai/skills.git"}

	got, err := sourceNames([]*Source{plannotator, bonkey})
	if err != nil || !reflect.DeepEqual(got, []string{"plannotator", "skills"}) {
		t.Errorf("bare names: %v %v", got, err)
	}
	got, err = sourceNames([]*Source{plannotator, bonkey, wondel})
	if err != nil || !reflect.DeepEqual(got, []string{"plannotator", "bonkey-skills", "wondelai-skills"}) {
		t.Errorf("both colliding sources get owner-repo: %v %v", got, err)
	}
	got, err = sourceNames([]*Source{wondel, bonkey, plannotator})
	if err != nil || !reflect.DeepEqual(got, []string{"wondelai-skills", "bonkey-skills", "plannotator"}) {
		t.Errorf("the order of the sources does not matter: %v %v", got, err)
	}
	got, err = sourceNames([]*Source{bonkey, named})
	if err != nil || !reflect.DeepEqual(got, []string{"skills", "wondel"}) {
		t.Errorf("an explicit name takes part in no collision: %v %v", got, err)
	}
	if _, err := sourceNames([]*Source{bonkey, {Name: "skills", URL: "https://x/y/z.git"}}); err == nil || !strings.Contains(err.Error(), "set name") {
		t.Errorf("an explicit name that clashes with a derived one is an error: %v", err)
	}
	if _, err := sourceNames([]*Source{{URL: "nowhere"}}); err == nil || !strings.Contains(err.Error(), "set name") {
		t.Errorf("a url without a repository name is an error: %v", err)
	}
}

func TestMCPNames(t *testing.T) {
	tests := []struct {
		def  MCP
		want string
	}{
		{MCP{Command: []string{"npx", "-y", "@mobilenext/mobile-mcp@latest"}}, "mobile-mcp"},
		{MCP{Command: []string{"npx", "shadcn@latest", "mcp"}}, "shadcn"},
		{MCP{Command: []string{"agentsview", "mcp"}}, "agentsview"},
		{MCP{Command: []string{"/usr/local/bin/agentsview"}}, "agentsview"},
		{MCP{Command: []string{"uvx", "mcp-server-fetch"}}, "mcp-server-fetch"},
		{MCP{Command: []string{"uvx", "--from", "git+https://x/y/z", "pkg[extra]==1.0"}}, "pkg"},
		{MCP{Command: []string{"uv", "run", "--with", "dep", "server"}}, "server"},
		{MCP{Command: []string{"uv", "sync"}}, "uv"},
		{MCP{Command: []string{"docker", "run", "-i", "mcp-server:latest"}}, "mcp-server"},
		{MCP{Command: []string{"docker", "run", "-i", "--rm", "-e", "KEY", "ghcr.io/github/github-mcp-server:latest"}}, "github-mcp-server"},
		{MCP{Command: []string{"python", "-m", "mcp_server"}}, "mcp_server"},
		{MCP{Command: []string{"node", "--no-warnings", "/srv/servers/notes.js"}}, "notes"},
		{MCP{Command: []string{"node"}}, "node"},
		{MCP{URL: "https://mcp.exa.ai/mcp?exaApiKey=${EXA_API_KEY}"}, "exa"},
		{MCP{URL: "https://mcp.deepwiki.com/mcp"}, "deepwiki"},
		{MCP{URL: "https://mcp.linear.app/mcp"}, "linear"},
		{MCP{URL: "http://localhost:3000/mcp"}, "localhost"},
		{MCP{Name: "given", URL: "https://mcp.linear.app/mcp"}, "given"},
	}
	for _, tt := range tests {
		got, err := tt.def.name()
		if err != nil || got != tt.want {
			t.Errorf("%v %q: got %q, %v; want %q", tt.def.Command, tt.def.URL, got, err, tt.want)
		}
	}
	for _, def := range []MCP{{Command: []string{"npx", "-y"}}, {URL: "not a url"}, {}} {
		if _, err := def.name(); err == nil || !strings.Contains(err.Error(), "set name") {
			t.Errorf("%+v: expected an error asking for a name, got %v", def, err)
		}
	}
}

func TestParseNamesAndTypesServers(t *testing.T) {
	c, err := Parse([]byte(`
[[mcps]]
command = ["npx", "-y", "simctl-mcp"]
environment = { HOME = "${HOME}" }

[[mcps]]
url = "https://mcp.exa.ai/mcp"

[[mcps]]
name = "search"
url = "https://mcp.tavily.com/mcp"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.MCPNames(); !reflect.DeepEqual(got, []string{"exa", "search", "simctl-mcp"}) {
		t.Fatalf("names: %v", got)
	}
	if c.MCPs["simctl-mcp"].Type != "local" || c.MCPs["exa"].Type != "remote" || c.MCPs["simctl-mcp"].Environment["HOME"] != "${HOME}" {
		t.Errorf("types and environment: %+v %+v", c.MCPs["simctl-mcp"], c.MCPs["exa"])
	}
	bad := map[string]string{
		"two derive the same name": "[[mcps]]\nurl = \"https://mcp.exa.ai/a\"\n[[mcps]]\nurl = \"https://exa.ai/b\"\n",
		"command and url":          "[[mcps]]\ncommand = [\"x\"]\nurl = \"https://x.io\"\n",
		"neither":                  "[[mcps]]\nname = \"x\"\n",
		"odd transport":            "[[mcps]]\nurl = \"https://x.io\"\ntransport = \"carrier-pigeon\"\n",
		"two sources named alike":  "[[skills]]\nurl = \"https://a/x/y.git\"\n[[skills]]\nname = \"y\"\nurl = \"https://b/z/w.git\"\n",
	}
	for name, text := range bad {
		if _, err := Parse([]byte(text)); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
}

func TestPackMembersNameSourcesFirst(t *testing.T) {
	c := New()
	c.Sources["skillet"] = &Source{URL: "https://github.com/bonkey/skillet.git", Skills: []string{"skillet", "other"}}
	c.Sources["tools"] = &Source{URL: "https://github.com/bonkey/tools.git", Skills: []string{"lint"}}
	if err := c.CreatePack("p", "P", []string{"skillet"}); err != nil {
		t.Fatal(err)
	}
	if got := c.PackSkills("p"); !reflect.DeepEqual(got, []string{"other", "skillet"}) {
		t.Errorf("a source name means all of its skills: %v", got)
	}
	if err := c.CreatePack("q", "Q", []string{"skillet@skillet", "lint"}); err != nil {
		t.Fatal(err)
	}
	if got := c.Packs["q"].Skills; !reflect.DeepEqual(got, []string{"lint@tools", "skillet@skillet"}) {
		t.Errorf("name@source is always a skill: %v", got)
	}
	if got := c.PackSkills("q"); !reflect.DeepEqual(got, []string{"lint", "skillet"}) {
		t.Errorf("pack q: %v", got)
	}
	// A skill added next to the source that provides it is already covered.
	if err := c.PackAdd("p", []string{"other@skillet", "lint"}); err != nil {
		t.Fatal(err)
	}
	if got := c.Packs["p"].Skills; !reflect.DeepEqual(got, []string{"lint@tools", "skillet"}) {
		t.Errorf("the source member stays and covers its skills: %v", got)
	}
}
