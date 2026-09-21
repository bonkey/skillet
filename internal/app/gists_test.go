package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bonkey/skillet/internal/catalog"
)

// fakeGists keeps gists in memory.
type fakeGists struct {
	files   map[string]string
	reads   int
	offline bool
}

func (f *fakeGists) Read(id string) (string, error) {
	f.reads++
	if content, ok := f.files[id]; ok && !f.offline {
		return content, nil
	}
	return "", errors.New("gist not reachable")
}

func (f *fakeGists) Create(content string, public bool) (string, error) {
	id := fmt.Sprintf("%032x", len(f.files)+1)
	f.files[id] = content
	return id, nil
}

func (f *fakeGists) Update(id, content string) error {
	if _, ok := f.files[id]; !ok {
		return errors.New("no such gist")
	}
	f.files[id] = content
	return nil
}

var (
	gistA = strings.Repeat("a", 32)
	gistB = strings.Repeat("b", 32)
	gistL = strings.Repeat("c", 32) // the local catalog's own gist
)

func manifest(source, url, skill string, includes ...string) string {
	return fmt.Sprintf("includes: [%s]\nsources:\n  %s:\n    url: %s\n    skills: [%s]\n"+
		"packs:\n  pack-%s:\n    description: Pack of %s\n    skills: [%s]\nenabled:\n  packs: [pack-%s]\n",
		strings.Join(includes, ", "), source, url, skill, skill, skill, skill, skill)
}

func TestSetRefPinsASource(t *testing.T) {
	e := setup(t)
	e.add(t, true)
	git(t, e.origin, "tag", "v1")
	git(t, e.origin, "config", "uploadpack.allowAnySHA1InWant", "true")
	write(t, filepath.Join(e.origin, "skills/alpha/SKILL.md"), "---\nname: alpha\ndescription: Second alpha\n---\n")
	git(t, e.origin, "commit", "-qam", "second")
	if _, err := e.app.Update(false); err != nil {
		t.Fatal(err)
	}
	description := func() string {
		found, _ := e.app.Index.Lookup(e.app.Catalog, "alpha")
		return found.Description
	}
	if description() != "Second alpha" {
		t.Fatalf("before pinning: %q", description())
	}

	if err := e.app.SetRef("acme/skills", "v1"); err != nil {
		t.Fatal(err)
	}
	if description() != "The alpha skill" || e.app.Local.Sources["acme/skills"].Ref != "v1" {
		t.Fatalf("pinned to v1: %q", description())
	}
	pinned := e.app.Sources()[0].Commit
	if _, err := e.app.Update(false); err != nil || e.app.Sources()[0].Commit != pinned {
		t.Errorf("an update must not move a tag: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(e.p.Home, ".claude", "skills", "alpha", "SKILL.md"))
	if !strings.Contains(string(data), "The alpha skill") {
		t.Errorf("the enabled link must show the pinned content: %q", data)
	}

	if err := e.app.SetRef("acme/skills", pinned); err != nil {
		t.Fatalf("pinning a commit: %v", err)
	}
	if err := e.app.SetRef("acme/skills", ""); err != nil || description() != "Second alpha" {
		t.Errorf("back on the default branch: %q %v", description(), err)
	}
	if err := e.app.SetRef("acme/skills", "no-such-ref"); err == nil {
		t.Error("expected an error for an unknown ref")
	}
	if e.app.Local.Sources["acme/skills"].Ref != "" {
		t.Error("a failed change must not be saved")
	}
}

func TestPushAndPull(t *testing.T) {
	e := setup(t)
	gists := &fakeGists{files: map[string]string{}}
	e.app.Gists = gists
	e.add(t, true)

	id, created, err := e.app.Push(false, false)
	if err != nil || !created || e.app.Local.Gist != id {
		t.Fatalf("first push: %q %v %v", id, created, err)
	}
	if !strings.Contains(gists.files[id], id) || !strings.Contains(gists.files[id], "alpha") {
		t.Fatalf("pushed content:\n%s", gists.files[id])
	}
	e.app.Toggle(e.app.Global(), false, "beta")
	if _, created, err = e.app.Push(false, false); err != nil || created || !strings.HasSuffix(gists.files[id], "skills:\n        - alpha\n") {
		t.Fatalf("second push: %v %v\n%s", created, err, gists.files[id])
	}

	// Another machine starts from a different catalog and pulls.
	other := e.p
	other.Home = filepath.Join(filepath.Dir(e.p.Home), "other")
	other.Config, other.Data = filepath.Join(other.Home, "config"), filepath.Join(other.Home, "data")
	old := catalog.New()
	old.Packs["old"] = &catalog.Pack{Description: "Old"}
	old.Save(other.ConfigFile())
	b, err := OpenWith(other, gists)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Pull(id); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.Enabled(b.Global()); !reflect.DeepEqual(got, []string{"alpha"}) {
		t.Errorf("enabled after pull: %v", got)
	}
	if !isLink(filepath.Join(other.Home, ".claude", "skills", "alpha")) {
		t.Error("pull should fetch the sources and link the enabled skills")
	}
	if backup, _ := os.ReadFile(other.ConfigFile() + ".bak"); !strings.Contains(string(backup), "Old") {
		t.Error("the previous catalog should be kept as a backup")
	}
	if _, err := b.Pull(""); err != nil {
		t.Errorf("a pulled catalog knows its gist: %v", err)
	}
}

func TestIncludesMergeOnceAndSurviveCycles(t *testing.T) {
	e := setup(t)
	gists := &fakeGists{files: map[string]string{
		gistA: manifest("acme/skills", e.origin, "alpha", gistB),
		gistB: manifest("acme/second", e.origin, "beta", gistA, gistL, gistB),
	}}
	e.app.Gists = gists
	e.app.Local.Gist = gistL
	if _, err := e.app.Include("https://gist.github.com/someone/" + gistA); err != nil {
		t.Fatal(err)
	}

	if len(e.app.Included) != 2 || e.app.Included[0].ID != gistA || e.app.Included[1].Parent != gistA {
		t.Fatalf("included: %+v", e.app.Included)
	}
	if got := e.app.Included[1].Skipped; !reflect.DeepEqual(got, []string{gistA, gistL, gistB}) {
		t.Errorf("cycles back to A, to the local gist and to B itself must be skipped: %v", got)
	}
	if got, _ := e.app.Enabled(e.app.Global()); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("included gists enable their skills: %v", got)
	}
	global := filepath.Join(e.p.Home, ".claude", "skills")
	if !isLink(filepath.Join(global, "alpha")) || !isLink(filepath.Join(global, "beta")) {
		t.Fatal("included skills should be linked")
	}
	view, _ := e.app.View()
	if view.Skills["beta"].From != gistB || view.Packs[0].From != gistA {
		t.Errorf("origins: %+v %+v", view.Skills["beta"], view.Packs[0])
	}
	raw, _ := os.ReadFile(e.p.ConfigFile())
	if strings.Contains(string(raw), "alpha") {
		t.Errorf("included entries must not be copied into the local catalog:\n%s", raw)
	}

	// Included entries are read-only, but can be switched off and grouped.
	if _, err := e.app.Remove("alpha"); err == nil {
		t.Error("removing an included skill should fail")
	}
	if err := e.app.EditPack("pack-alpha", false, func(*catalog.Catalog) error { return nil }); err == nil {
		t.Error("editing an included pack should fail")
	}
	if _, err := e.app.Toggle(e.app.Global(), false, "alpha"); err != nil {
		t.Fatal(err)
	}
	if isLink(filepath.Join(global, "alpha")) || !reflect.DeepEqual(e.app.Local.Enabled.Except, []string{"alpha"}) {
		t.Errorf("disable of an inherited skill: %+v", e.app.Local.Enabled)
	}
	err := e.app.EditPack("mine", true, func(local *catalog.Catalog) error {
		return local.CreatePack("mine", "My pick", []string{"beta"})
	})
	if err != nil {
		t.Errorf("a local pack may hold included skills: %v", err)
	}

	// A later start works from the cache.
	gists.offline, gists.reads = true, 0
	again, err := OpenWith(e.p, gists)
	if err != nil || len(again.Included) != 2 || gists.reads != 0 || len(again.Warnings) != 0 {
		t.Fatalf("offline start: %v %d reads %v", err, gists.reads, again.Warnings)
	}
	if _, err := again.Update(true); err != nil || len(again.Included) != 2 || len(again.Warnings) == 0 {
		t.Errorf("an offline update falls back to the cache with a warning: %v %v", err, again.Warnings)
	}
	gists.offline = false

	if _, err := e.app.Exclude(gistA); err != nil {
		t.Fatal(err)
	}
	if len(e.app.Included) != 0 || e.app.Catalog.HasSkill("alpha") || isLink(filepath.Join(global, "beta")) {
		t.Errorf("after exclude: %+v", e.app.Included)
	}
	if _, err := os.Stat(e.p.RepoDir("acme/second")); !os.IsNotExist(err) {
		t.Error("a clone only the excluded gist needed should be deleted")
	}
	if _, err := e.app.Include(gistL); err == nil {
		t.Error("including the catalog's own gist should fail")
	}
}
