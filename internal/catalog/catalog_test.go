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
	c.Sources["acme/skills"] = &Source{URL: "https://github.com/acme/skills.git", Skills: []string{"a", "b", "c"}}
	c.Sources["me/own"] = &Source{URL: "https://github.com/me/own.git", Skills: []string{"pr"}}
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
	c.Enabled = Set{Packs: []string{"acme"}, Skills: []string{"pr"}, Except: []string{"b"}}
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
		{"except", Set{Packs: []string{"acme"}, Except: []string{"b"}}, []string{"a"}},
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

	// A skill enabled through a pack is switched off with an exception.
	if err := c.Disable(&s, "b"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Except, []string{"b@acme/skills"}) || !reflect.DeepEqual(c.Resolve(s), []string{"a", "pr"}) {
		t.Fatalf("after disable b: %+v", s)
	}

	// Enabling it again only drops the exception.
	if err := c.Enable(&s, "b"); err != nil {
		t.Fatal(err)
	}
	if len(s.Except) != 0 || !reflect.DeepEqual(s.Skills, []string{"pr@me/own"}) {
		t.Fatalf("after re-enable b: %+v", s)
	}

	// Disabling a pack switches off all its skills, also individually enabled ones.
	if err := c.Enable(&s, "a"); err != nil {
		t.Fatal(err)
	}
	if err := c.Disable(&s, "b"); err != nil {
		t.Fatal(err)
	}
	if err := c.Disable(&s, "@acme"); err != nil {
		t.Fatal(err)
	}
	if got := c.Resolve(s); !reflect.DeepEqual(got, []string{"pr"}) || len(s.Except) != 0 {
		t.Fatalf("after disable @acme: %+v -> %v", s, got)
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
	if err := c.AddSkills("other/repo", "https://x/other/repo.git", "", []string{"new", "pr"}); err == nil {
		t.Fatal("expected a collision error for pr")
	}
	if _, ok := c.Sources["other/repo"]; ok {
		t.Error("a failed add must not leave a source behind")
	}
	if err := c.AddSkills("acme/skills", "", "", []string{"a", "d"}); err != nil {
		t.Fatal(err)
	}
	if got := c.Sources["acme/skills"].Skills; !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("got %v", got)
	}
}

func TestRemoveSkillCleansEverything(t *testing.T) {
	c := sample()
	c.Enabled = Set{Skills: []string{"pr"}, Except: []string{"pr"}}
	c.RemoveSkill("pr")
	if _, ok := c.Sources["me/own"]; ok {
		t.Error("a source without skills should be dropped")
	}
	if !reflect.DeepEqual(c.Packs["mixed"].Skills, []string{"b"}) {
		t.Errorf("pack not cleaned: %v", c.Packs["mixed"].Skills)
	}
	if len(c.Enabled.Skills) != 0 || len(c.Enabled.Except) != 0 {
		t.Errorf("enabled set not cleaned: %+v", c.Enabled)
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
	if got := c.Packs["new"].Skills; !reflect.DeepEqual(got, []string{"a@acme/skills", "c@acme/skills"}) {
		t.Errorf("got %v", got)
	}
	c.Enabled.Packs = []string{"new"}
	c.RemovePack("new")
	if _, ok := c.Packs["new"]; ok || len(c.Enabled.Packs) != 0 {
		t.Error("pack not removed everywhere")
	}
}

func TestSetFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), ".skillet.toml")
	s, err := LoadSet(file)
	if err != nil || !s.Empty() {
		t.Fatalf("missing manifest: %+v %v", s, err)
	}
	want := Set{Packs: []string{"ios"}, Skills: []string{"pr"}}
	if err := want.Save(file); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSet(file)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestMerge(t *testing.T) {
	local := sample()
	local.Enabled = Set{Skills: []string{"pr"}}

	first := New()
	first.Sources["acme/skills"] = &Source{URL: "ignored", Ref: "v9", Skills: []string{"a", "z"}}
	first.Sources["other/repo"] = &Source{URL: "https://x/other/repo.git", Ref: "v2", Skills: []string{"pr", "new"}}
	first.Packs["acme"] = &Pack{Description: "Their idea of acme", Skills: []string{"z"}}
	first.Packs["theirs"] = &Pack{Description: "Theirs", Skills: []string{"new", "z"}}
	first.Enabled = Set{Packs: []string{"theirs"}, Except: []string{"z"}}

	second := New()
	second.Sources["late/repo"] = &Source{URL: "u", Skills: []string{"new", "late"}}
	second.Enabled = Set{Skills: []string{"late"}}

	merged, err := Merge(local, []string{"g1", "g2"}, []*Catalog{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if src := merged.Sources["acme/skills"]; src.Ref != "" || !reflect.DeepEqual(src.Skills, []string{"a", "b", "c", "z"}) {
		t.Errorf("the local source keeps its ref and gains skills: %+v", src)
	}
	if got, _ := merged.SourceOf("pr"); got != "me/own" {
		t.Errorf("a local skill keeps its source: %s", got)
	}
	if got, _ := merged.SourceOf("new"); got != "other/repo" {
		t.Errorf("the earlier include wins: %s", got)
	}
	if merged.Packs["acme"].Description != "Acme things" || merged.PackOrigin["theirs"] != "g1" {
		t.Errorf("packs: %+v %v", merged.Packs["acme"], merged.PackOrigin)
	}
	if merged.SkillOrigin["late"] != "g2" || merged.SkillOrigin["pr"] != "" {
		t.Errorf("origins: %v", merged.SkillOrigin)
	}
	if got := merged.Resolve(merged.Enabled); !reflect.DeepEqual(got, []string{"late", "new", "pr"}) {
		t.Errorf("enabled: %v", got)
	}
	if len(local.Sources["acme/skills"].Skills) != 3 || local.Enabled.Inherited != nil {
		t.Error("merging must not change the local catalog")
	}

	// An inherited skill is switched off with an exception and on again by dropping it.
	set := merged.Enabled
	if err := merged.Disable(&set, "late"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(set.Except, []string{"late@late/repo"}) || slices.Contains(merged.Resolve(set), "late") {
		t.Errorf("after disable: %+v", set)
	}
	if err := merged.Enable(&set, "late"); err != nil {
		t.Fatal(err)
	}
	if len(set.Except) != 0 || slices.Contains(set.Skills, "late") {
		t.Errorf("after enable: %+v", set)
	}
}

func TestExampleCatalogEnablesTheSkilletSkill(t *testing.T) {
	c, err := Load(filepath.Join("..", "..", "examples", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Resolve(c.Enabled); !reflect.DeepEqual(got, []string{"skillet"}) {
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
	c.Sources["big/repo"] = &Source{URL: "u", Skills: many}
	c.MCPs["x"] = &MCP{Type: "local", Command: []string{"npx", "x"}, Environment: map[string]string{"K": "${K}"}}
	c.Enabled = Set{Packs: []string{"acme"}}
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
	if !strings.Contains(text, "[mcps.x]\ntype = 'local'") {
		t.Errorf("a table with keys keeps its header:\n%s", text)
	}
	if !strings.Contains(text, "skills = [\n  'a-rather-long-skill-name-00',\n") || !strings.Contains(text, "skills = ['a', 'b', 'c']") {
		t.Errorf("long lists wrap, short ones stay on one line:\n%s", text)
	}
	got, err := Load(file)
	if err != nil || !reflect.DeepEqual(got, c) {
		t.Fatalf("the tidy file loads back unchanged: %v", err)
	}
}

func TestPackOfWholeSources(t *testing.T) {
	c := sample()
	if err := c.CreatePack("all-acme", "Everything from acme", []string{"acme/skills", "pr"}); err != nil {
		t.Fatal(err)
	}
	if pack := c.Packs["all-acme"]; !reflect.DeepEqual(pack.Sources, []string{"acme/skills"}) || !reflect.DeepEqual(pack.Skills, []string{"pr@me/own"}) {
		t.Fatalf("a source is kept by name, not as a list of its skills: %+v", pack)
	}
	if got := c.PackSkills("all-acme"); !reflect.DeepEqual(got, []string{"a", "b", "c", "pr"}) {
		t.Fatalf("pack skills: %v", got)
	}

	var s Set
	c.Enable(&s, "@all-acme")
	c.AddSkills("acme/skills", "", "", []string{"d"})
	if got := c.Resolve(s); !reflect.DeepEqual(got, []string{"a", "b", "c", "d", "pr"}) {
		t.Errorf("a skill added to the source joins the pack: %v", got)
	}
	c.Disable(&s, "b")
	if !reflect.DeepEqual(s.Except, []string{"b@acme/skills"}) || !slices.Contains(c.PacksOf("d"), "all-acme") {
		t.Errorf("exceptions and membership work through sources: %+v", s)
	}

	if err := c.PackRemove("all-acme", []string{"a"}); err == nil {
		t.Error("a skill that a source brings cannot be taken out on its own")
	}
	if err := c.PackRemove("all-acme", []string{"acme/skills"}); err != nil || len(c.PackSkills("all-acme")) != 1 {
		t.Errorf("taking the source out: %v %v", err, c.PackSkills("all-acme"))
	}

	c.PackAdd("all-acme", []string{"me/own"})
	c.RemoveSkill("pr")
	if len(c.Packs["all-acme"].Sources) != 0 {
		t.Errorf("a source that is gone leaves the pack: %+v", c.Packs["all-acme"])
	}
}

func TestSourceWithoutSkillsTakesAllItOffers(t *testing.T) {
	c := sample()
	c.AddSource("whole/repo", "https://x/whole/repo.git", "")
	c.AddSource("other/whole", "https://x/other/whole.git", "")
	merged, err := Merge(c, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !merged.Sources["whole/repo"].All || merged.Sources["acme/skills"].All {
		t.Fatalf("only a source without a skills list takes all: %+v", merged.Sources["whole/repo"])
	}
	offered := map[string][]string{
		"whole/repo":  {"x", "y", "a"}, // "a" belongs to acme/skills, which names it
		"other/whole": {"y", "z"},      // "y" goes to other/whole, which sorts first
		"acme/skills": {"a", "b", "c", "unlisted"},
	}
	merged.ExpandAll(offered)
	if got := merged.Sources["whole/repo"].Skills; !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("whole/repo: %v", got)
	}
	if got := merged.Sources["other/whole"].Skills; !reflect.DeepEqual(got, []string{"y", "z"}) {
		t.Errorf("other/whole: %v", got)
	}
	if got := merged.Sources["acme/skills"].Skills; !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("a source with a list keeps it: %v", got)
	}
	offered["whole/repo"] = []string{"w"}
	merged.ExpandAll(offered)
	if got := merged.Sources["whole/repo"].Skills; !reflect.DeepEqual(got, []string{"w"}) {
		t.Errorf("expanding again follows the clone: %v", got)
	}

	// Naming a skill of a source that takes all keeps it taking all.
	if err := c.AddSkills("whole/repo", "", "", []string{"x"}); err != nil || len(c.Sources["whole/repo"].Skills) != 0 {
		t.Errorf("AddSkills: %v %+v", err, c.Sources["whole/repo"])
	}

	c.CreatePack("whole", "Whole repos", []string{"whole/repo", "pr"})
	c.Enabled.Skills = []string{"pr"}
	c.RemoveSource("whole/repo")
	c.RemoveSource("me/own")
	if _, ok := c.Sources["whole/repo"]; ok || len(c.Packs["whole"].Sources)+len(c.Packs["whole"].Skills) != 0 || len(c.Enabled.Skills) != 0 {
		t.Errorf("RemoveSource cleans packs and the enabled set: %+v %+v", c.Packs["whole"], c.Enabled)
	}
}
