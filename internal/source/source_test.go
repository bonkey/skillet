package source

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/paths"
)

func write(t *testing.T, file, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// remote creates a git repository with two skills and returns its path.
func remote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "skills/alpha/SKILL.md"), "---\nname: alpha\ndescription: \"Alpha: does things\"\n---\nbody\n")
	write(t, filepath.Join(dir, "skills/alpha/reference/notes.md"), "notes\n")
	write(t, filepath.Join(dir, "skills/beta/SKILL.md"), "---\nname: beta\ndescription: Beta skill\n---\n")
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestParseName(t *testing.T) {
	tests := []struct{ in, name, url string }{
		{"acme/skills", "acme/skills", "https://github.com/acme/skills.git"},
		{"https://github.com/acme/skills.git", "acme/skills", "https://github.com/acme/skills.git"},
		{"https://github.com/acme/skills", "acme/skills", "https://github.com/acme/skills"},
		{"git@github.com:acme/skills.git", "acme/skills", "git@github.com:acme/skills.git"},
	}
	for _, tt := range tests {
		name, url, err := ParseName(tt.in)
		if err != nil || name != tt.name || url != tt.url {
			t.Errorf("ParseName(%q) = %q, %q, %v", tt.in, name, url, err)
		}
	}
	if _, _, err := ParseName("nonsense"); err == nil {
		t.Error("expected an error")
	}
}

func TestDiscover(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "skills/alpha/SKILL.md"), "---\nname: alpha\ndescription: >\n  Folded\n  text\n---\n")
	write(t, filepath.Join(dir, "skills/alpha/nested/SKILL.md"), "---\nname: nested\n---\n")
	write(t, filepath.Join(dir, "skills/folder-name/SKILL.md"), "no frontmatter\n")
	write(t, filepath.Join(dir, "skills/broken/SKILL.md"), "---\nname: broken\ndescription: Use when: things break\n---\n")
	write(t, filepath.Join(dir, "plugins/p/skills/alpha/SKILL.md"), "---\nname: alpha\ndescription: copy\n---\n")
	write(t, filepath.Join(dir, ".git/SKILL.md"), "---\nname: hidden\n---\n")

	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Skill{
		"alpha":       {Path: "skills/alpha", Description: "Folded text"},
		"folder-name": {Path: "skills/folder-name"},
		"broken":      {Path: "skills/broken", Description: "Use when: things break"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestDiscoverRootSkill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-repo")
	write(t, filepath.Join(dir, "SKILL.md"), "---\nname: root-skill\ndescription: Root\n---\n")
	write(t, filepath.Join(dir, "examples/other/SKILL.md"), "---\nname: other\n---\n")
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Skill{"root-skill": {Path: ".", Description: "Root"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestCloneFetchChanged(t *testing.T) {
	origin := remote(t)
	clone := filepath.Join(t.TempDir(), "acme", "skills")
	if err := Clone(origin, "", clone); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(clone, "skills/alpha/reference/notes.md")); err != nil {
		t.Fatal("clone is incomplete")
	}
	old, err := Head(clone)
	if err != nil {
		t.Fatal(err)
	}

	latest, err := Fetch(clone, "")
	if err != nil || latest != old {
		t.Fatalf("fetch without upstream changes: %q vs %q, %v", latest, old, err)
	}

	write(t, filepath.Join(origin, "skills/alpha/reference/notes.md"), "changed\n")
	gitT(t, origin, "commit", "-qam", "change alpha")
	latest, err = Fetch(clone, "")
	if err != nil || latest == old {
		t.Fatalf("fetch should see the new commit: %q, %v", latest, err)
	}
	if head, _ := Head(clone); head != old {
		t.Fatal("fetch must not move HEAD")
	}
	for path, want := range map[string]bool{"skills/alpha": true, "skills/beta": false} {
		got, err := Changed(clone, old, latest, path)
		if err != nil || got != want {
			t.Errorf("Changed(%s) = %v, %v", path, got, err)
		}
	}

	if err := Checkout(clone, latest); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(clone, "skills/alpha/reference/notes.md"))
	if string(data) != "changed\n" {
		t.Errorf("checkout did not update files: %q", data)
	}
}

func TestIndexBuildsAndRebuilds(t *testing.T) {
	origin := remote(t)
	p := paths.Paths{Data: t.TempDir()}
	c := catalog.New()
	c.Sources["acme/skills"] = &catalog.Source{URL: origin, Skills: []string{"alpha", "gone"}}
	c.Sources["not/cloned"] = &catalog.Source{URL: origin, Skills: []string{"x"}}
	if err := Clone(origin, "", p.RepoDir("acme/skills")); err != nil {
		t.Fatal(err)
	}

	idx, err := LoadIndex(p, c)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := idx.Lookup(c, "alpha")
	if !ok || got.Description != "Alpha: does things" || got.Dir != filepath.Join(p.RepoDir("acme/skills"), "skills/alpha") {
		t.Fatalf("lookup alpha: %+v %v", got, ok)
	}
	if _, ok := idx.Lookup(c, "gone"); ok {
		t.Error("a skill missing from its source must not resolve")
	}
	if _, ok := idx.Lookup(c, "x"); ok {
		t.Error("a skill of an uncloned source must not resolve")
	}
	if _, err := os.Stat(p.IndexFile()); err != nil {
		t.Fatal("index was not cached")
	}

	// A stale cache entry is rebuilt when the clone moves on.
	write(t, filepath.Join(origin, "skills/alpha/SKILL.md"), "---\nname: alpha\ndescription: New text\n---\n")
	gitT(t, origin, "commit", "-qam", "describe")
	latest, _ := Fetch(p.RepoDir("acme/skills"), "")
	if err := Checkout(p.RepoDir("acme/skills"), latest); err != nil {
		t.Fatal(err)
	}
	idx, _ = LoadIndex(p, c)
	if got, _ := idx.Lookup(c, "alpha"); got.Description != "New text" {
		t.Errorf("stale description: %q", got.Description)
	}

	// A deleted cache is rebuilt.
	os.Remove(p.IndexFile())
	idx, _ = LoadIndex(p, c)
	if _, ok := idx.Lookup(c, "alpha"); !ok {
		t.Error("index not rebuilt after deletion")
	}
}

func TestCloneAtBranchTagAndCommit(t *testing.T) {
	origin := remote(t)
	first, _ := Head(origin)
	gitT(t, origin, "tag", "v1")
	gitT(t, origin, "branch", "stable")
	write(t, filepath.Join(origin, "skills/beta/SKILL.md"), "---\nname: beta\ndescription: Second version\n---\n")
	gitT(t, origin, "commit", "-qam", "second")
	gitT(t, origin, "config", "uploadpack.allowAnySHA1InWant", "true")

	for _, ref := range []string{"v1", "stable", first} {
		dir := filepath.Join(t.TempDir(), "clone")
		if err := Clone(origin, ref, dir); err != nil {
			t.Fatalf("clone at %s: %v", ref, err)
		}
		if head, _ := Head(dir); head != first {
			t.Errorf("clone at %s is at %s, want %s", ref, head, first)
		}
		if latest, err := Fetch(dir, ref); err != nil || latest != first {
			t.Errorf("fetch of %s moved to %s, %v", ref, latest, err)
		}
	}

	// An existing clone switches to another ref.
	dir := filepath.Join(t.TempDir(), "clone")
	if err := Clone(origin, "", dir); err != nil {
		t.Fatal(err)
	}
	commit, err := Fetch(dir, "v1")
	if err != nil || commit != first {
		t.Fatalf("fetch v1: %s %v", commit, err)
	}

	if err := Clone(origin, "no-such-ref", filepath.Join(t.TempDir(), "bad")); err == nil {
		t.Error("expected an error for an unknown ref")
	}
}
