package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOverlayListsBelongToTheLocalFile(t *testing.T) {
	if _, err := Parse([]byte("enabled = ['@p']\n")); err == nil || !strings.Contains(err.Error(), "config.local.toml") {
		t.Errorf("config.toml rejects the lists: %v", err)
	}
	file := filepath.Join(t.TempDir(), "config.local.toml")
	if over, err := LoadOverlay(file); over != nil || err != nil {
		t.Errorf("a missing overlay is nil: %v %v", over, err)
	}
	os.WriteFile(file, []byte("enabled = ['@p']\ndisabled = ['a']\n"), 0o644)
	over, err := LoadOverlay(file)
	if err != nil || !reflect.DeepEqual(over.Enabled, []string{"@p"}) || !reflect.DeepEqual(over.Disabled, []string{"a"}) || over.Agents != nil {
		t.Fatalf("the overlay holds the lists and no agents: %+v %v", over, err)
	}
	os.WriteFile(file, []byte("gist = 'x'\n"), 0o644)
	if _, err := LoadOverlay(file); err == nil || !strings.Contains(err.Error(), "config.toml") {
		t.Errorf("the overlay rejects gist and includes: %v", err)
	}
}

func TestOverlayReplacesAndAdds(t *testing.T) {
	base, err := Parse([]byte(`
agents = ['codex']

[[packs]]
name = "p"
description = "P"
skills = ["a"]

[[skills]]
url = "https://x/acme/skills.git"
only = ["a"]

[[mcps]]
url = "https://mcp.exa.ai/mcp"

[[secrets]]
account = "a"
vault = "v"
item = "i"
`))
	if err != nil {
		t.Fatal(err)
	}
	over, err := LoadOverlay(writeTemp(t, `
[[packs]]
name = "q"
description = "Q"
skills = ["a"]

[[mcps]]
name = "exa"
url = "https://proxy.example/exa"

[[secrets]]
account = "b"
vault = "v"
item = "j"
`))
	if err != nil {
		t.Fatal(err)
	}
	c, err := Overlay(base, over)
	if err != nil {
		t.Fatal(err)
	}
	if c.MCPs["exa"].URL != "https://proxy.example/exa" || len(c.MCPs) != 1 {
		t.Errorf("a same-named server is replaced: %+v", c.MCPs)
	}
	if c.Packs["q"] == nil || c.Packs["p"] == nil {
		t.Errorf("a new pack is added: %v", c.PackNames())
	}
	if len(c.Secrets) != 2 || !reflect.DeepEqual(c.Agents, []string{"codex"}) {
		t.Errorf("secrets are appended and agents kept: %+v %v", c.Secrets, c.Agents)
	}
	if base.MCPs["exa"].URL != "https://mcp.exa.ai/mcp" {
		t.Error("the base is untouched")
	}
	over.Agents = []string{"zed"}
	if c, _ = Overlay(base, over); !reflect.DeepEqual(c.Agents, []string{"zed"}) {
		t.Errorf("agents listed in the overlay win: %v", c.Agents)
	}
}

func TestOverrideFlipsFlags(t *testing.T) {
	c, err := Parse([]byte(`
[[packs]]
name = "p"
description = "P"
enabled = false
skills = ["a"]

[[skills]]
url = "https://x/acme/skills.git"
only = ["a", "b", { name = "c", enabled = false }]

[[mcps]]
url = "https://mcp.exa.ai/mcp"
enabled = false
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Override([]string{"@p", "mcp:exa", "c", "b"}, []string{"b"}); err != nil {
		t.Fatal(err)
	}
	got := c.Declared(everything)
	want := Set{Skills: []string{"a", "c"}, MCPs: []string{"exa"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("enabled switches on, disabled wins for b:\n got %+v\nwant %+v", got, want)
	}
	if err := c.Override(nil, []string{"@nope"}); err == nil || !strings.Contains(err.Error(), `unknown pack "nope"`) {
		t.Errorf("an unknown name is an error: %v", err)
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.local.toml")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}
