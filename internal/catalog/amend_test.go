package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const commented = `# my catalog
agents = ['claude-code', 'codex']   # who gets what

[[packs]]
name = 'craft'                                 # the pack
description = 'Code craft'
skills = ['ponytail']

[[skills]]                                     # a whole repository
url = 'https://github.com/dietrichgebert/ponytail.git'

[[skills]]
name = 'wondel'                                # optional
url = 'https://github.com/wondelai/skills.git'
only = ['clean-code']   # trailing

[[skills]]
url = 'https://github.com/acme/tools.git'
only = [
  'a',   # the one I use
  { name = 'b', enabled = false },
]
ref = 'v1'

[[mcps]]
command = ['npx', '-y', 'simctl-mcp']          # local
`

// amended loads the commented catalog, changes it and amends its file.
func amended(t *testing.T, change func(c *Catalog)) (string, error) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(file, []byte(commented), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	change(c)
	err = c.Amend(file)
	data, _ := os.ReadFile(file)
	if _, statErr := os.Stat(file + ".tmp"); !os.IsNotExist(statErr) {
		t.Error("the temporary file is gone")
	}
	return string(data), err
}

func TestAmendAppendsNewEntries(t *testing.T) {
	got, err := amended(t, func(c *Catalog) {
		if _, err := c.AddSource(&Source{URL: "https://github.com/bonkey/skills.git", Skills: []string{"pr"}}); err != nil {
			t.Fatal(err)
		}
		c.MCPs["exa"] = &MCP{URL: "https://mcp.exa.ai/mcp"}
	})
	want := commented + "\n[[skills]]\nurl = 'https://github.com/bonkey/skills.git'\nonly = ['pr']\n\n[[mcps]]\nurl = 'https://mcp.exa.ai/mcp'\n"
	if err != nil || got != want {
		t.Fatalf("%v\n%s", err, got)
	}
}

func TestAmendReplacesAChangedOnlyList(t *testing.T) {
	got, err := amended(t, func(c *Catalog) { c.SetSkillFlag("wondel", "top-design", true) })
	want := strings.Replace(commented, "only = ['clean-code']   # trailing", "only = ['clean-code', 'top-design']   # trailing", 1)
	if err != nil || got != want {
		t.Fatalf("%v\n%s", err, got)
	}
}

func TestAmendRemovesAnOnlyListThatTakesAll(t *testing.T) {
	got, err := amended(t, func(c *Catalog) { c.Sources["wondel"].TakeAll() })
	want := strings.Replace(commented, "only = ['clean-code']   # trailing\n", "# trailing\n", 1)
	if err != nil || got != want {
		t.Fatalf("the comment after the list stays:\n%v\n%s", err, got)
	}
}

func TestAmendRefusesToLoseTheCommentsOfAList(t *testing.T) {
	got, err := amended(t, func(c *Catalog) { c.Sources["tools"].TakeAll() })
	if err == nil || !strings.Contains(err.Error(), "skills:tools") || !strings.Contains(err.Error(), "only = [{name = 'b', enabled = false}]") || got != commented {
		t.Fatalf("%v\n%s", err, got)
	}
	got, err = amended(t, func(c *Catalog) { c.Sources["tools"].Skills, c.Sources["tools"].Disabled = nil, nil })
	if err == nil || !strings.Contains(err.Error(), "skills:tools") || got != commented {
		t.Fatalf("%v\n%s", err, got)
	}
}

func TestAmendRefusesWhatItCannotWrite(t *testing.T) {
	got, err := amended(t, func(c *Catalog) { c.Packs["craft"].Description = "changed" })
	if err == nil || got != commented {
		t.Fatalf("a change amend does not write leaves the file as it is: %v\n%s", err, got)
	}
}

func TestAmendWritesANewFileWhole(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", ".skillet.toml")
	c := New()
	c.MCPs["exa"] = &MCP{URL: "https://mcp.exa.ai/mcp"}
	if err := c.Amend(file); err != nil {
		t.Fatal(err)
	}
	want, _ := c.Encode()
	if got, _ := os.ReadFile(file); string(got) != string(want) {
		t.Fatalf("got\n%s", got)
	}
}
