package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var everything = func(string) bool { return true }

func TestFlagsRoundTrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	c, err := Parse([]byte(`
[[packs]]
name = "p"
description = "P"
enabled = false
skills = ["a"]

[[skills]]
url = "https://x/acme/skills.git"
only = ["a", { name = "b", enabled = false }, { name = "c", enabled = true }]

[[skills]]
url = "https://x/acme/all.git"
enabled = false
only = [{ name = "z", enabled = false }]

[[mcps]]
url = "https://mcp.exa.ai/mcp"
enabled = false
`))
	if err != nil {
		t.Fatal(err)
	}
	src := c.Sources["skills"]
	if !reflect.DeepEqual(src.Skills, []string{"a", "b", "c"}) || !reflect.DeepEqual(src.Disabled, []string{"b"}) || !src.On() {
		t.Fatalf("listed source: %+v", src)
	}
	if all := c.Sources["all"]; !all.takesAll() || all.On() || all.SkillOn("z") {
		t.Fatalf("a source whose only holds nothing that is on takes all: %+v", all)
	}
	if c.Packs["p"].On() || c.MCPs["exa"].On() {
		t.Error("flags on packs and servers")
	}
	if err := c.Save(file); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	for _, want := range []string{"only = ['a', {name = 'b', enabled = false}, 'c']", "enabled = false\nonly = [{name = 'z', enabled = false}]", "[[mcps]]\nurl = 'https://mcp.exa.ai/mcp'\nenabled = false"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("saved file lacks %q:\n%s", want, raw)
		}
	}
	got, err := Load(file)
	if err != nil || !reflect.DeepEqual(got, c) {
		t.Fatalf("round trip: %v\n got %+v\nwant %+v", err, got, c)
	}
	for name, text := range map[string]string{
		"a number":     "[[skills]]\nurl = \"https://x/a/b.git\"\nonly = [1]\n",
		"no name":      "[[skills]]\nurl = \"https://x/a/b.git\"\nonly = [{ enabled = false }]\n",
		"no flag":      "[[skills]]\nurl = \"https://x/a/b.git\"\nonly = [{ name = \"x\" }]\n",
		"wrong type":   "[[skills]]\nurl = \"https://x/a/b.git\"\nonly = [{ name = \"x\", enabled = \"no\" }]\n",
		"pack flag":    "[[packs]]\nname = \"p\"\ndescription = \"P\"\nenabled = \"no\"\n",
		"pack name":    "[[packs]]\ndescription = \"P\"\n",
		"pack twice":   "[[packs]]\nname = \"p\"\ndescription = \"P\"\n\n[[packs]]\nname = \"p\"\ndescription = \"Q\"\n",
		"packs table":  "[packs.p]\ndescription = \"P\"\n",
		"source flag":  "[[skills]]\nurl = \"https://x/a/b.git\"\nenabled = 1\n",
		"server flag":  "[[mcps]]\nurl = \"https://x.io\"\nenabled = \"off\"\n",
		"only strings": "[[skills]]\nurl = \"https://x/a/b.git\"\nonly = \"x\"\n",
	} {
		if _, err := Parse([]byte(text)); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
}

func TestDeclaredFollowsTheFlags(t *testing.T) {
	c := sample() // skills: a, b, c from "skills"; pr from "own"; packs acme (a, b) and mixed (b, pr)
	c.MCPs["exa"] = &MCP{URL: "https://mcp.exa.ai/mcp"}
	c.MCPs["held"] = &MCP{URL: "https://mcp.held.io/mcp"}
	c.Packs["acme"].MCPs = []string{"held"}
	declared := func() (skills, servers []string) {
		set := c.Declared(everything)
		return set.Skills, set.MCPs
	}
	check := func(what string, skills, servers []string) {
		t.Helper()
		gotSkills, gotServers := declared()
		if !reflect.DeepEqual(gotSkills, skills) || !reflect.DeepEqual(gotServers, servers) {
			t.Errorf("%s: skills %v, servers %v", what, gotSkills, gotServers)
		}
	}
	check("everything is on by default", []string{"a", "b", "c", "pr"}, []string{"exa", "held"})

	c.Packs["acme"].Enabled = flag(false)
	check("a pack off takes its members, unless another pack holds them", []string{"b", "c", "pr"}, []string{"exa"})

	c.Packs["mixed"].Enabled = flag(false)
	check("every pack that holds b is off", []string{"c"}, []string{"exa"})

	c.Packs["acme"].Enabled, c.Packs["mixed"].Enabled = nil, nil
	c.Sources["skills"].Enabled = flag(false)
	check("a source off wins over the packs", []string{"pr"}, []string{"exa", "held"})

	c.Sources["skills"].Enabled = nil
	c.Sources["skills"].Disabled = []string{"a"}
	c.MCPs["exa"].Enabled = flag(false)
	check("a skill's and a server's own flags", []string{"b", "c", "pr"}, []string{"held"})

	// Origins: what another catalog defines only counts where asked for.
	c.Sources["skills"].Disabled = nil
	c.MCPs["exa"].Enabled = nil
	c.SourceOrigin, c.PackOrigin, c.MCPOrigin = map[string]string{"own": "g1"}, map[string]string{"mixed": "g1"}, map[string]string{"exa": "g1"}
	local := c.Declared(func(origin string) bool { return origin == "" })
	if !reflect.DeepEqual(local.Skills, []string{"a", "b", "c"}) || !reflect.DeepEqual(local.MCPs, []string{"held"}) {
		t.Errorf("local only: %+v", local)
	}
	theirs := c.Declared(func(origin string) bool { return origin == "g1" })
	if !reflect.DeepEqual(theirs.Skills, []string{"b", "pr"}) || !reflect.DeepEqual(theirs.MCPs, []string{"exa"}) {
		t.Errorf("their pack holds b too: %+v", theirs)
	}
}

func TestSetFlags(t *testing.T) {
	c := sample()
	c.MCPs["exa"] = &MCP{URL: "https://mcp.exa.ai/mcp"}
	c.Sources["all"] = &Source{URL: "https://x/acme/all.git"}
	for _, name := range []string{"@acme", "mcp:exa", "skills:own"} {
		if err := c.SetFlag(name, false); err != nil {
			t.Fatal(err)
		}
	}
	if c.Packs["acme"].On() || c.MCPs["exa"].On() || c.Sources["own"].On() {
		t.Error("flags should be off")
	}
	for _, name := range []string{"@acme", "mcp:exa", "skills:own"} {
		if err := c.SetFlag(name, true); err != nil {
			t.Fatal(err)
		}
	}
	if c.Packs["acme"].Enabled != nil || c.MCPs["exa"].Enabled != nil || c.Sources["own"].Enabled != nil {
		t.Error("on is the absence of the flag")
	}
	for _, name := range []string{"@ghost", "mcp:ghost", "skills:ghost", "a"} {
		if err := c.SetFlag(name, false); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}

	// A listed source flips the entry; a skill the list lacks is appended.
	c.SetSkillFlag("skills", "b", false)
	if src := c.Sources["skills"]; !reflect.DeepEqual(src.Disabled, []string{"b"}) || !reflect.DeepEqual(src.Skills, []string{"a", "b", "c"}) {
		t.Errorf("after disabling b: %+v", src)
	}
	c.SetSkillFlag("skills", "b", true)
	c.SetSkillFlag("skills", "d", true)
	if src := c.Sources["skills"]; len(src.Disabled) != 0 || !reflect.DeepEqual(src.Skills, []string{"a", "b", "c", "d"}) {
		t.Errorf("after enabling b and d: %+v", src)
	}
	for _, skill := range []string{"a", "b", "c"} {
		c.SetSkillFlag("skills", skill, false)
	}
	if err := c.SetSkillFlag("skills", "d", false); err == nil || !strings.Contains(err.Error(), "skills:skills") {
		t.Errorf("the last listed skill that is on stays on, or the list would take all: %v", err)
	}
	if src := c.Sources["skills"]; src.takesAll() || !reflect.DeepEqual(src.Disabled, []string{"a", "b", "c"}) {
		t.Errorf("after the refusal: %+v", src)
	}

	// A source that takes all keeps taking all.
	c.SetSkillFlag("all", "z", false)
	if src := c.Sources["all"]; !src.takesAll() || src.SkillOn("z") || !reflect.DeepEqual(src.Skills, []string{"z"}) {
		t.Errorf("after disabling z: %+v", src)
	}
	c.SetSkillFlag("all", "z", true)
	if src := c.Sources["all"]; !src.takesAll() || len(src.Skills) != 0 || len(src.Disabled) != 0 {
		t.Errorf("after enabling z again: %+v", src)
	}
	if err := c.SetSkillFlag("ghost", "z", true); err == nil {
		t.Error("an unknown source should be rejected")
	}
}
