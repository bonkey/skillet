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
	return amendedText(t, commented, change)
}

// amendedText loads a catalog from text, changes it and amends its file.
func amendedText(t *testing.T, text string, change func(c *Catalog)) (string, error) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
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

func TestAmendReplacesAChangedURL(t *testing.T) {
	got, err := amended(t, func(c *Catalog) {
		c.Sources["wondel"].URL = "https://github.com/wondelai/agent-skills.git"
		c.SetSkillFlag("wondel", "top-design", true)
	})
	want := strings.Replace(commented, "url = 'https://github.com/wondelai/skills.git'\nonly = ['clean-code']   # trailing",
		"url = 'https://github.com/wondelai/agent-skills.git'\nonly = ['clean-code', 'top-design']   # trailing", 1)
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

func TestAmendCutsRemovedEntries(t *testing.T) {
	got, err := amended(t, func(c *Catalog) { delete(c.Sources, "wondel") })
	want := strings.Replace(commented, "[[skills]]\nname = 'wondel'                                # optional\n"+
		"url = 'https://github.com/wondelai/skills.git'\nonly = ['clean-code']   # trailing\n\n", "", 1)
	if err != nil || got != want {
		t.Fatalf("an entry in the middle goes with its blank line:\n%v\n%s", err, got)
	}

	got, err = amended(t, func(c *Catalog) {
		delete(c.Sources, "ponytail")
		delete(c.MCPs, "simctl-mcp")
	})
	want = strings.Replace(commented, "[[skills]]                                     # a whole repository\n"+
		"url = 'https://github.com/dietrichgebert/ponytail.git'\n\n", "", 1)
	want = strings.Replace(want, "\n[[mcps]]\ncommand = ['npx', '-y', 'simctl-mcp']          # local\n", "", 1)
	if err != nil || got != want {
		t.Fatalf("the first source and the last entry go:\n%v\n%s", err, got)
	}
}

func TestAmendCutsAnEntryWithItsCommentsAndSubtables(t *testing.T) {
	text := `agents = ['claude-code']

# search
[[mcps]]
url = 'https://mcp.exa.ai/mcp'

[mcps.headers]
X-Team = 'docs'
# about simctl
[[mcps]]
command = ['npx', '-y', 'simctl-mcp']
`
	got, err := amendedText(t, text, func(c *Catalog) { delete(c.MCPs, "exa") })
	want := "agents = ['claude-code']\n\n# about simctl\n[[mcps]]\ncommand = ['npx', '-y', 'simctl-mcp']\n"
	if err != nil || got != want {
		t.Fatalf("the comment above an entry goes with it, the next one's stays:\n%v\n%s", err, got)
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

func TestAmendRefusesAHeaderItCannotPlace(t *testing.T) {
	text := "[[skills]]\nurl = 'https://github.com/acme/tools.git'\n\n[\"a\\u0062\"]\nx = 1\n"
	got, err := amendedText(t, text, func(c *Catalog) { delete(c.Sources, "tools") })
	if err == nil || !strings.Contains(err.Error(), "the header of [ab] cannot be amended") || got != text {
		t.Fatalf("an escaped key leaves the file as it is: %v\n%s", err, got)
	}
}
