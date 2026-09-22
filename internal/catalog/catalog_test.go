package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func sample() *Catalog {
	c := New()
	c.Sources["skills"] = &Source{URL: "https://github.com/acme/skills.git", Skills: []string{"a", "b", "c"}}
	c.Sources["own"] = &Source{URL: "https://github.com/me/own.git", Skills: []string{"pr"}}
	c.Packs["acme"] = &Pack{Description: "Acme things", Skills: []string{"a", "b"}}
	c.Packs["mixed"] = &Pack{Description: "Mixed", Skills: []string{"b", "pr"}}
	return c
}

func TestLoadMissingFileGivesEmptyCatalog(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Agents, []string{"claude-code"}) || len(c.Sources) != 0 {
		t.Fatalf("unexpected default catalog: %+v", c)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "config.toml")
	c := sample()
	c.Packs["mixed"].Enabled = flag(false)
	c.Sources["own"].Enabled = flag(false)
	c.Sources["skills"].Disabled = []string{"b"}
	if err := c.Save(file); err != nil {
		t.Fatal(err)
	}
	got, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, c) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, c)
	}
	raw, _ := os.ReadFile(file)
	for _, banned := range []string{"path =", "note ="} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("catalog file contains %q:\n%s", banned, raw)
		}
	}
}

func TestResolve(t *testing.T) {
	c := sample()
	tests := []struct {
		name string
		set  Set
		want []string
	}{
		{"empty", Set{}, nil},
		{"pack", Set{Packs: []string{"acme"}}, []string{"a", "b"}},
		{"pack and skill", Set{Packs: []string{"acme"}, Skills: []string{"pr"}}, []string{"a", "b", "pr"}},
		{"overlapping packs", Set{Packs: []string{"acme", "mixed"}}, []string{"a", "b", "pr"}},
		{"unknown names are dropped", Set{Packs: []string{"gone"}, Skills: []string{"ghost", "c"}}, []string{"c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.Resolve(tt.set); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnableDisable(t *testing.T) {
	c := sample()
	var s Set

	if err := c.Enable(&s, "@acme", "pr"); err != nil {
		t.Fatal(err)
	}
	if got := c.Resolve(s); !reflect.DeepEqual(got, []string{"a", "b", "pr"}) {
		t.Fatalf("after enable: %v", got)
	}

	// A skill a pack of the set provides is not recorded again.
	if err := c.Enable(&s, "b"); err != nil || !reflect.DeepEqual(s.Skills, []string{"pr@own"}) {
		t.Fatalf("after enable b: %+v %v", s, err)
	}

	// Disabling a pack switches off all its skills, also individually enabled ones.
	if err := c.Enable(&s, "a"); err != nil {
		t.Fatal(err)
	}
	if err := c.Disable(&s, "@acme"); err != nil {
		t.Fatal(err)
	}
	if got := c.Resolve(s); !reflect.DeepEqual(got, []string{"pr"}) || len(s.Packs) != 0 {
		t.Fatalf("after disable @acme: %+v -> %v", s, got)
	}

	// A whole source goes on and off by name.
	if err := c.Enable(&s, "skills:skills"); err != nil || !reflect.DeepEqual(c.Resolve(s), []string{"a", "b", "c", "pr"}) {
		t.Fatalf("after enable skills:skills: %+v %v", s, err)
	}
	if err := c.Disable(&s, "skills:skills"); err != nil || !reflect.DeepEqual(c.Resolve(s), []string{"pr"}) {
		t.Fatalf("after disable skills:skills: %+v %v", s, err)
	}
	if err := c.Enable(&s, "skills:ghost"); err == nil {
		t.Error("enabling an unknown source should fail")
	}

	if err := c.Enable(&s, "ghost"); err == nil {
		t.Error("enabling an unknown skill should fail")
	}
	if err := c.Enable(&s, "@ghost"); err == nil {
		t.Error("enabling an unknown pack should fail")
	}
}

func TestAddSkillsRejectsNameFromAnotherSource(t *testing.T) {
	c := sample()
	if _, err := c.AddSkills("https://x/other/repo.git", "", []string{"new", "pr"}); err == nil {
		t.Fatal("expected a collision error for pr")
	}
	if _, ok := c.Sources["repo"]; ok {
		t.Error("a failed add must not leave a source behind")
	}
	name, err := c.AddSkills("https://github.com/acme/skills.git", "", []string{"a", "d"})
	if err != nil || name != "skills" {
		t.Fatal(name, err)
	}
	if got := c.Sources["skills"].Skills; !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("got %v", got)
	}
}

func TestAddSkillsNamesTheNewSourceAndRenamesACollidingOne(t *testing.T) {
	c := sample()
	name, err := c.AddSkills("https://github.com/other/skills.git", "v1", []string{"x"})
	if err != nil || name != "other-skills" {
		t.Fatalf("new source: %q %v", name, err)
	}
	if _, ok := c.Sources["skills"]; ok || c.Sources["acme-skills"] == nil || c.Sources["acme-skills"].Ref != "" {
		t.Errorf("the existing source is renamed too: %v", sortedKeys(c.Sources))
	}
	if got, _ := c.SourceOf("a"); got != "acme-skills" {
		t.Errorf("skills follow their renamed source: %s", got)
	}
	names, err := c.NamesFor([]*Source{{URL: "https://github.com/acme/skills.git"}, {URL: "https://x/new/own.git"}, {URL: "https://x/new/thing.git"}})
	if err != nil || !reflect.DeepEqual(names, []string{"acme-skills", "new-own", "thing"}) {
		t.Errorf("NamesFor keeps known urls and names new ones as they will be: %v %v", names, err)
	}
}

func TestPackEditing(t *testing.T) {
	c := sample()
	if err := c.CreatePack("new", "", []string{"a"}); err == nil {
		t.Error("a pack needs a description")
	}
	if err := c.CreatePack("new", "New pack", []string{"ghost"}); err == nil {
		t.Error("a pack may only hold catalog skills")
	}
	if err := c.CreatePack("new", "New pack", []string{"c"}); err != nil {
		t.Fatal(err)
	}
	if err := c.CreatePack("new", "Again", nil); err == nil {
		t.Error("creating an existing pack should fail")
	}
	if err := c.PackAdd("new", []string{"a", "c"}); err != nil {
		t.Fatal(err)
	}
	if got := c.Packs["new"].Skills; !reflect.DeepEqual(got, []string{"a@skills", "c@skills"}) {
		t.Errorf("got %v", got)
	}
}

func TestMerge(t *testing.T) {
	local := sample()

	first := New()
	first.Sources["skills"] = &Source{URL: "ignored", Ref: "v9", Skills: []string{"a", "z"}}
	first.Sources["repo"] = &Source{URL: "https://x/other/repo.git", Ref: "v2", Skills: []string{"pr", "new"}}
	first.Packs["acme"] = &Pack{Description: "Their idea of acme", Skills: []string{"z"}}
	first.Packs["theirs"] = &Pack{Description: "Theirs", Skills: []string{"new", "z"}}

	second := New()
	second.Sources["late"] = &Source{URL: "https://x/late/late.git", Skills: []string{"new", "late"}}

	merged, err := Merge(local, []string{"g1", "g2"}, []*Catalog{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if src := merged.Sources["skills"]; src.Ref != "" || !reflect.DeepEqual(src.Skills, []string{"a", "b", "c", "z"}) {
		t.Errorf("the local source keeps its ref and gains skills: %+v", src)
	}
	if got, _ := merged.SourceOf("pr"); got != "own" {
		t.Errorf("a local skill keeps its source: %s", got)
	}
	if got, _ := merged.SourceOf("new"); got != "repo" {
		t.Errorf("the earlier include wins: %s", got)
	}
	if merged.Packs["acme"].Description != "Acme things" || merged.PackOrigin["theirs"] != "g1" {
		t.Errorf("packs: %+v %v", merged.Packs["acme"], merged.PackOrigin)
	}
	if merged.SkillOrigin["late"] != "g2" || merged.SkillOrigin["pr"] != "" {
		t.Errorf("origins: %v", merged.SkillOrigin)
	}
	if o := merged.SourceOrigin; o["late"] != "g2" || o["repo"] != "g1" || o["skills"] != "" {
		t.Errorf("source origins: %v", o)
	}
	all := func(string) bool { return true }
	if got := merged.Declared(all); !reflect.DeepEqual(got.Skills, []string{"a", "b", "c", "late", "new", "pr", "z"}) {
		t.Errorf("everything is on by default: %v", got)
	}
	notSecond := func(origin string) bool { return origin != "g2" }
	if got := merged.Declared(notSecond); !reflect.DeepEqual(got.Skills, []string{"a", "b", "c", "new", "pr", "z"}) {
		t.Errorf("a source left out declares nothing: %v", got)
	}
	if len(local.Sources["skills"].Skills) != 3 {
		t.Error("merging must not change the local catalog")
	}
}

func TestExampleCatalogEnablesTheSkilletSkill(t *testing.T) {
	c, err := Load(filepath.Join("..", "..", "examples", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Declared(func(string) bool { return true }); !reflect.DeepEqual(got.Skills, []string{"skillet"}) {
		t.Errorf("enabled: %v", got)
	}
	if _, err := os.Stat(filepath.Join("..", "..", "skills", "skillet", "SKILL.md")); err != nil {
		t.Errorf("the example points at a skill this repository must contain: %v", err)
	}
	if c.Packs["skillet"].Description == "" {
		t.Error("the example pack needs a description")
	}
}

func TestSavedFileIsTidy(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	c := sample()
	var many []string
	for i := range 20 {
		many = append(many, fmt.Sprintf("a-rather-long-skill-name-%02d", i))
	}
	c.Sources["repo"] = &Source{URL: "https://x/big/repo.git", Skills: many}
	c.MCPs["x"] = &MCP{Type: "local", Command: []string{"npx", "x"}, Environment: map[string]string{"K": "${K}"}}
	if err := c.Save(file); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	text := string(raw)
	for _, header := range []string{"[sources]\n", "[packs]\n", "[mcps]\n"} {
		if strings.Contains(text, header) {
			t.Errorf("an empty parent header %q should be dropped:\n%s", header, text)
		}
	}
	if !strings.Contains(text, "[[mcps]]\ncommand = ['npx', 'x']\nenvironment = {K = '${K}'}") {
		t.Errorf("a server is an array entry with its maps inline:\n%s", text)
	}
	if !strings.Contains(text, "[[skills]]\nurl = 'https://x/big/repo.git'\nonly = [\n  'a-rather-long-skill-name-00',\n") || !strings.Contains(text, "only = ['a', 'b', 'c']") {
		t.Errorf("long lists wrap, short ones stay on one line:\n%s", text)
	}
	got, err := Load(file)
	if err != nil || !reflect.DeepEqual(got, c) {
		t.Fatalf("the tidy file loads back unchanged: %v", err)
	}
}

func TestPackOfWholeSources(t *testing.T) {
	c := sample()
	if err := c.CreatePack("all-acme", "Everything from acme", []string{"skills", "pr"}); err != nil {
		t.Fatal(err)
	}
	if pack := c.Packs["all-acme"]; !reflect.DeepEqual(pack.Skills, []string{"pr@own", "skills"}) {
		t.Fatalf("a source is kept by name, not as a list of its skills: %+v", pack)
	}
	if got := c.PackSkills("all-acme"); !reflect.DeepEqual(got, []string{"a", "b", "c", "pr"}) {
		t.Fatalf("pack skills: %v", got)
	}

	var s Set
	c.Enable(&s, "@all-acme")
	c.AddSkills("https://github.com/acme/skills.git", "", []string{"d"})
	if got := c.Resolve(s); !reflect.DeepEqual(got, []string{"a", "b", "c", "d", "pr"}) {
		t.Errorf("a skill added to the source joins the pack: %v", got)
	}
	if !slices.Contains(c.PacksOf("d"), "all-acme") {
		t.Errorf("membership works through sources: %v", c.PacksOf("d"))
	}
}

func TestSourceWithoutSkillsTakesAllItOffers(t *testing.T) {
	c := sample()
	c.Sources["repo"] = &Source{URL: "https://x/whole/repo.git"}
	c.Sources["all"] = &Source{URL: "https://x/other/all.git"}
	merged, err := Merge(c, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !merged.Sources["repo"].All || merged.Sources["skills"].All {
		t.Fatalf("only a source without a skills list takes all: %+v", merged.Sources["repo"])
	}
	offered := map[string][]string{
		"repo":   {"x", "y", "a"}, // "a" belongs to skills, which names it
		"all":    {"y", "z"},      // "y" goes to all, which sorts first
		"skills": {"a", "b", "c", "unlisted"},
	}
	merged.ExpandAll(offered)
	if got := merged.Sources["repo"].Skills; !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("repo: %v", got)
	}
	if got := merged.Sources["all"].Skills; !reflect.DeepEqual(got, []string{"y", "z"}) {
		t.Errorf("all: %v", got)
	}
	if got := merged.Sources["skills"].Skills; !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("a source with a list keeps it: %v", got)
	}
	offered["repo"] = []string{"w"}
	merged.ExpandAll(offered)
	if got := merged.Sources["repo"].Skills; !reflect.DeepEqual(got, []string{"w"}) {
		t.Errorf("expanding again follows the clone: %v", got)
	}

	// Naming a skill of a source that takes all keeps it taking all.
	if _, err := c.AddSkills("https://x/whole/repo.git", "", []string{"x"}); err != nil || len(c.Sources["repo"].Skills) != 0 {
		t.Errorf("AddSkills: %v %+v", err, c.Sources["repo"])
	}
}
