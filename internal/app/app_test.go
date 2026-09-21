package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

type env struct {
	app    *App
	origin string // git repository acting as the remote "acme/skills"
	p      paths.Paths
}

func setup(t *testing.T) env {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "remotes", "acme", "skills")
	for _, name := range []string{"alpha", "beta"} {
		write(t, filepath.Join(origin, "skills", name, "SKILL.md"),
			fmt.Sprintf("---\nname: %s\ndescription: The %s skill\n---\n", name, name))
	}
	write(t, filepath.Join(origin, "skills/alpha/reference/notes.md"), "notes\n")
	git(t, origin, "init", "-q", "-b", "main")
	git(t, origin, "add", "-A")
	git(t, origin, "commit", "-q", "-m", "init")

	p := paths.Paths{
		Home:   filepath.Join(root, "home"),
		Config: filepath.Join(root, "home", ".config", "skillet"),
		Data:   filepath.Join(root, "home", ".local", "share", "skillet"),
		Cwd:    filepath.Join(root, "home", "work", "project"),
	}
	if err := os.MkdirAll(p.Cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return env{app: a, origin: origin, p: p}
}

func (e env) add(t *testing.T, enable bool) {
	t.Helper()
	name, url, skills, err := e.app.Fetch(e.origin, "")
	if err != nil || name != "acme/skills" || len(skills) != 2 {
		t.Fatalf("fetch: %q %v %v", name, skills, err)
	}
	err = e.app.Add(AddRequest{Source: name, URL: url, Skills: []string{"alpha", "beta"},
		Pack: "acme", PackDescription: "Acme skills", Enable: enable})
	if err != nil {
		t.Fatal(err)
	}
}

func isLink(path string) bool {
	_, err := os.Readlink(path)
	return err == nil
}

func TestAddEnableDisableRemove(t *testing.T) {
	e := setup(t)
	global := filepath.Join(e.p.Home, ".claude", "skills")

	e.add(t, false)
	if isLink(filepath.Join(global, "alpha")) {
		t.Fatal("added skills start disabled")
	}
	if _, err := e.app.Toggle(e.app.Global(), true, "@acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(global, "alpha", "reference", "notes.md")); err != nil {
		t.Fatal("the whole skill folder must be reachable through the link")
	}
	if _, err := e.app.Toggle(e.app.Global(), false, "beta"); err != nil {
		t.Fatal(err)
	}
	if isLink(filepath.Join(global, "beta")) || !isLink(filepath.Join(global, "alpha")) {
		t.Fatal("disable beta: wrong links")
	}

	// The catalog file holds names only; a reopened app sees the same state.
	raw, _ := os.ReadFile(e.p.CatalogFile())
	if strings.Contains(string(raw), "The alpha skill") || strings.Contains(string(raw), "skills/alpha") {
		t.Errorf("catalog leaks skill details:\n%s", raw)
	}
	reopened, err := Open(e.p)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := reopened.Enabled(reopened.Global()); !reflect.DeepEqual(got, []string{"alpha"}) {
		t.Errorf("enabled after reopen: %v", got)
	}
	if found, ok := reopened.Index.Lookup(reopened.Catalog, "alpha"); !ok || found.Description != "The alpha skill" {
		t.Errorf("description comes from the skill: %+v", found)
	}

	if _, err := e.app.Remove("@acme"); err != nil {
		t.Fatal(err)
	}
	if isLink(filepath.Join(global, "alpha")) {
		t.Error("removed skill is still linked")
	}
	if _, err := os.Stat(e.p.RepoDir("acme/skills")); !os.IsNotExist(err) {
		t.Error("unused clone was kept")
	}
	if len(e.app.Catalog.Sources)+len(e.app.Catalog.Packs) != 0 {
		t.Errorf("catalog not empty: %+v", e.app.Catalog)
	}
}

func TestProjectScope(t *testing.T) {
	e := setup(t)
	e.add(t, false)
	scope, err := e.app.ProjectScope()
	if err != nil || scope.Root != e.p.Cwd {
		t.Fatalf("scope %+v", scope)
	}
	if _, err := e.app.Toggle(scope, true, "alpha"); err != nil {
		t.Fatal(err)
	}
	if !isLink(filepath.Join(e.p.Cwd, ".claude", "skills", "alpha")) {
		t.Error("project link missing")
	}
	if isLink(filepath.Join(e.p.Home, ".claude", "skills", "alpha")) {
		t.Error("project enable leaked into the global scope")
	}
	if _, err := os.Stat(filepath.Join(e.p.Cwd, paths.ManifestName)); err != nil {
		t.Error("manifest was not written")
	}

	// The manifest is found from a subdirectory.
	sub := e.p
	sub.Cwd = filepath.Join(e.p.Cwd, "deep", "er")
	os.MkdirAll(sub.Cwd, 0o755)
	below, _ := Open(sub)
	if got, _ := below.ProjectScope(); got.Root != e.p.Cwd {
		t.Errorf("root from subdirectory: %s", got.Root)
	}

	home := e.p
	home.Cwd = e.p.Home
	atHome, _ := Open(home)
	if _, err := atHome.ProjectScope(); err == nil {
		t.Error("the home directory must not become a project")
	}
}

func TestSyncReportsSkillsMissingFromTheirSource(t *testing.T) {
	e := setup(t)
	e.add(t, true)
	e.app.Catalog.Sources["acme/skills"].Skills = append(e.app.Catalog.Sources["acme/skills"].Skills, "vanished")
	e.app.Catalog.Enabled.Skills = append(e.app.Catalog.Enabled.Skills, "vanished")
	report, err := e.app.Sync(e.app.Global(), SyncOptions{})
	if err != nil || !reflect.DeepEqual(report.Missing, []string{"vanished"}) {
		t.Errorf("got %+v, %v", report, err)
	}
}

func TestUpdate(t *testing.T) {
	e := setup(t)
	e.add(t, true)
	write(t, filepath.Join(e.origin, "skills/alpha/SKILL.md"), "---\nname: alpha\ndescription: Better alpha\n---\n")
	write(t, filepath.Join(e.origin, "README.md"), "hello\n")
	git(t, e.origin, "add", "-A")
	git(t, e.origin, "commit", "-q", "-m", "improve alpha")

	updates, err := e.app.Update(true)
	if err != nil || len(updates) != 1 || !reflect.DeepEqual(updates[0].Changed, []string{"alpha"}) {
		t.Fatalf("check: %+v %v", updates, err)
	}
	if found, _ := e.app.Index.Lookup(e.app.Catalog, "alpha"); found.Description != "The alpha skill" {
		t.Error("a check must not move the clone")
	}

	if _, err := e.app.Update(false); err != nil {
		t.Fatal(err)
	}
	if found, _ := e.app.Index.Lookup(e.app.Catalog, "alpha"); found.Description != "Better alpha" {
		t.Errorf("description after update: %q", found.Description)
	}
	if updates, _ = e.app.Update(true); len(updates[0].Changed) != 0 || updates[0].Other {
		t.Errorf("nothing should be pending: %+v", updates[0])
	}

	// A catalog copied to a fresh machine gets its clones back.
	os.RemoveAll(e.p.RepoDir("acme/skills"))
	updates, _ = e.app.Update(false)
	if !updates[0].Cloned || !isLink(filepath.Join(e.p.Home, ".claude", "skills", "alpha")) {
		t.Errorf("clone not restored: %+v", updates[0])
	}
}

func TestImport(t *testing.T) {
	e := setup(t)
	global := filepath.Join(e.p.Home, ".claude", "skills")
	legacy := filepath.Join(e.p.Home, ".agents", "skills")
	app := filepath.Join(e.p.Home, "Applications", "skill")
	for _, dir := range []string{global, app, filepath.Join(global, "unmanaged"), filepath.Join(legacy, "not-locked")} {
		os.MkdirAll(dir, 0o755)
	}
	// What the `skills` CLI leaves behind: a folder per skill and a link to it.
	for _, name := range []string{"alpha", "beta"} {
		write(t, filepath.Join(legacy, name, "SKILL.md"), "installed copy\n")
		os.Symlink("../../.agents/skills/"+name, filepath.Join(global, name))
	}
	os.Symlink("../../.agents/skills/not-locked", filepath.Join(global, "not-locked"))
	os.Symlink(app, filepath.Join(global, "from-app"))
	lock := fmt.Sprintf(`{"version":3,"skills":{
		"alpha":{"sourceUrl":%q,"skillPath":"skills/alpha/SKILL.md"},
		"beta":{"sourceUrl":%q,"skillPath":"skills/beta/SKILL.md"},
		"lost":{"sourceUrl":%q,"skillPath":"skills/lost/SKILL.md"}}}`, e.origin, e.origin, e.origin)
	write(t, e.app.LegacyLock(), lock)

	dry, err := e.app.Import(e.app.LegacyLock(), true)
	if err != nil || len(dry.Sync.Actions) != 2 {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	if _, err := os.Stat(e.p.CatalogFile()); !os.IsNotExist(err) {
		t.Fatal("dry run wrote the catalog")
	}
	if data, _ := os.ReadFile(filepath.Join(legacy, "alpha", "SKILL.md")); string(data) != "installed copy\n" {
		t.Fatal("dry run touched an installed folder")
	}

	// The clone left by the dry run is stale by the time of the real import.
	write(t, filepath.Join(e.origin, "skills/alpha/SKILL.md"), "---\nname: alpha\ndescription: Fresh alpha\n---\n")
	git(t, e.origin, "commit", "-qam", "upstream moved on")

	fresh, _ := Open(e.p)
	report, err := fresh.Import(fresh.LegacyLock(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Imported, []string{"alpha", "beta"}) || report.Skipped["lost"] == "" {
		t.Errorf("report: %+v", report)
	}
	for _, name := range []string{"alpha", "beta"} {
		got, _ := os.Readlink(filepath.Join(legacy, name))
		if want := filepath.Join(e.p.RepoDir("acme/skills"), "skills", name); got != want {
			t.Errorf("~/.agents/skills/%s -> %q, want %s", name, got, want)
		}
		if got, _ := os.Readlink(filepath.Join(global, name)); got != "../../.agents/skills/"+name {
			t.Errorf("~/.claude/skills/%s -> %q", name, got)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(global, "alpha", "SKILL.md")); !strings.Contains(string(data), "Fresh alpha") {
		t.Errorf("content must be fetched, not reused: %q", data)
	}
	if got, _ := os.Readlink(filepath.Join(global, "from-app")); got != app {
		t.Error("foreign link was touched")
	}
	for _, dir := range []string{filepath.Join(global, "unmanaged"), filepath.Join(legacy, "not-locked")} {
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
			t.Errorf("%s was touched", dir)
		}
	}
	if got, _ := os.Readlink(filepath.Join(global, "not-locked")); got != "../../.agents/skills/not-locked" {
		t.Error("the link to a folder that is not in the lock was touched")
	}
	pack := fresh.Catalog.Packs["acme"]
	if pack == nil || pack.Description == "" || !reflect.DeepEqual(fresh.Catalog.Enabled.Packs, []string{"acme"}) {
		t.Errorf("pack: %+v enabled: %+v", pack, fresh.Catalog.Enabled)
	}
}

func TestRunEnablesSkillsOnlyWhileTheCommandRuns(t *testing.T) {
	e := setup(t)
	e.add(t, false)
	linkPath := filepath.Join(e.p.Cwd, ".claude", "skills", "alpha")
	code, err := e.app.Run([]string{"alpha"}, []string{"test", "-L", linkPath})
	if err != nil || code != 0 {
		t.Fatalf("the link should exist during the run: %d %v", code, err)
	}
	if isLink(linkPath) {
		t.Error("the link should be gone after the run")
	}
	if _, err := os.Stat(filepath.Join(e.p.Cwd, paths.ManifestName)); !os.IsNotExist(err) {
		t.Error("run must not create a manifest")
	}
}
