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

// config is a catalog with the origin as its source, in the pack "acme".
// Without a list of skills, the source takes all it offers.
func (e env) config(skills, enabled string) string {
	members := "skills = ['alpha', 'beta']"
	if skills == "" {
		members = "sources = ['acme/skills']"
	}
	return fmt.Sprintf("agents = ['claude-code']\n\n[sources.'acme/skills']\nurl = %q\n%s\n\n"+
		"[packs.acme]\ndescription = 'Acme skills'\n%s\n\n%s", e.origin, skills, members, enabled)
}

// add writes a catalog with alpha and beta, optionally declared enabled,
// and syncs the global scope.
func (e *env) add(t *testing.T, enable bool) {
	t.Helper()
	enabled := ""
	if enable {
		enabled = "[enabled]\npacks = ['acme']\n"
	}
	e.open(t, e.config("skills = ['alpha', 'beta']", enabled))
}

func (e *env) open(t *testing.T, config string) {
	t.Helper()
	write(t, e.p.ConfigFile(), config)
	a, err := OpenWith(e.p, e.app.Gists)
	if err != nil {
		t.Fatal(err)
	}
	a.OnePassword = e.app.OnePassword
	e.app = a
	if _, err := a.Sync(a.Global(), SyncOptions{}); err != nil {
		t.Fatal(err)
	}
}

func isLink(path string) bool {
	_, err := os.Readlink(path)
	return err == nil
}

func TestEnableAndDisableChangeLinksOnly(t *testing.T) {
	e := setup(t)
	global := filepath.Join(e.p.Home, ".claude", "skills")

	e.add(t, false)
	if isLink(filepath.Join(global, "alpha")) {
		t.Fatal("skills start disabled")
	}
	before, _ := os.ReadFile(e.p.ConfigFile())
	if _, err := e.app.Toggle(e.app.Global(), true, "@acme"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(filepath.Join(global, "alpha")); got != filepath.Join(e.p.RepoDir("acme/skills"), "skills", "alpha") {
		t.Fatalf("the link goes straight into the clone: %q", got)
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
	if after, _ := os.ReadFile(e.p.ConfigFile()); string(after) != string(before) {
		t.Errorf("the config file was written:\n%s", after)
	}

	// The links are the state: a reopened app sees it.
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
}

func TestSyncEnablesWhatIsDeclaredAndFlagsTheRest(t *testing.T) {
	e := setup(t)
	global := filepath.Join(e.p.Home, ".claude", "skills")
	e.open(t, e.config("skills = ['alpha', 'beta']", "[enabled]\nskills = ['alpha']\n"))
	if !isLink(filepath.Join(global, "alpha")) || isLink(filepath.Join(global, "beta")) {
		t.Fatal("sync links what the config declares")
	}
	if _, err := e.app.Toggle(e.app.Global(), true, "beta"); err != nil {
		t.Fatal(err)
	}
	report, err := e.app.Toggle(e.app.Global(), false, "alpha")
	if err != nil || len(report.Notes) != 1 || !strings.Contains(report.Notes[0], "alpha") {
		t.Fatalf("disabling a declared skill says that sync brings it back: %+v %v", report, err)
	}

	report, err = e.app.Sync(e.app.Global(), SyncOptions{})
	if err != nil || !reflect.DeepEqual(report.Extra, []string{"beta"}) {
		t.Fatalf("beta is extra: %+v %v", report, err)
	}
	if !isLink(filepath.Join(global, "alpha")) || !isLink(filepath.Join(global, "beta")) {
		t.Fatal("sync brings alpha back and keeps beta")
	}
	if report, _ = e.app.Sync(e.app.Global(), SyncOptions{}); len(report.Actions) != 0 {
		t.Errorf("sync is idempotent: %+v", report.Actions)
	}

	report, err = e.app.Sync(e.app.Global(), SyncOptions{Remove: true})
	if err != nil || len(report.Extra) != 0 || isLink(filepath.Join(global, "beta")) || !isLink(filepath.Join(global, "alpha")) {
		t.Fatalf("remove disables what is not declared: %+v %v", report, err)
	}
}

func TestSyncGivesANewAgentTheSameLinks(t *testing.T) {
	e := setup(t)
	e.add(t, false)
	if _, err := e.app.Toggle(e.app.Global(), true, "alpha"); err != nil {
		t.Fatal(err)
	}
	e.app.UseAgents([]string{"claude-code", "codex"})
	if _, err := e.app.Sync(e.app.Global(), SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(e.p.RepoDir("acme/skills"), "skills", "alpha")
	if got, _ := os.Readlink(filepath.Join(e.p.Home, ".codex", "skills", "alpha")); got != want {
		t.Errorf("codex link: %q", got)
	}

	e.app.UseAgents([]string{"codex"})
	if _, err := e.app.Toggle(e.app.Global(), false, "alpha"); err != nil {
		t.Fatal(err)
	}
	if isLink(filepath.Join(e.p.Home, ".codex", "skills", "alpha")) || !isLink(filepath.Join(e.p.Home, ".claude", "skills", "alpha")) {
		t.Error("with agents given, only their directories change")
	}
}

func TestForceReplacesConflictingEntries(t *testing.T) {
	e := setup(t)
	e.add(t, false)
	blocked := filepath.Join(e.p.Home, ".claude", "skills", "alpha")
	os.MkdirAll(filepath.Join(blocked, "stale"), 0o755)

	report, err := e.app.Toggle(e.app.Global(), true, "alpha")
	if err != nil || isLink(blocked) || len(report.Actions) == 0 {
		t.Fatalf("without force the folder stays: %+v %v", report, err)
	}
	e.app.Force = true
	if _, err := e.app.Toggle(e.app.Global(), true, "alpha"); err != nil || !isLink(blocked) {
		t.Fatalf("with force the folder is replaced by the link: %v", err)
	}
}

func TestViewFilter(t *testing.T) {
	view := View{
		Packs: []PackView{
			{Name: "apple", Description: "Building for iOS", Skills: []string{"docc", "pr"}},
			{Name: "git", Description: "Version control", Skills: []string{"pr", "rebase"}},
		},
		Skills: map[string]SkillView{
			"docc":   {Name: "docc", Description: "Swift DocC documentation markup", Global: true},
			"pr":     {Name: "pr", Description: "Create and update pull requests"},
			"rebase": {Name: "rebase", Description: "Rebase a branch and resolve conflicts", Project: true},
		},
	}
	names := func(v View) []string {
		var out []string
		for _, pack := range v.Packs {
			for _, skill := range pack.Skills {
				out = append(out, pack.Name+"/"+skill)
			}
		}
		return out
	}
	tests := []struct {
		name    string
		pack    string
		enabled bool
		terms   []string
		want    []string
	}{
		{"no filter", "", false, nil, []string{"apple/docc", "apple/pr", "git/pr", "git/rebase"}},
		{"case-insensitive word in a description", "", false, []string{"SWIFT"}, []string{"apple/docc"}},
		{"every term must match", "", false, []string{"pull", "update"}, []string{"apple/pr", "git/pr"}},
		{"alternatives in one term", "", false, []string{"docc|rebase"}, []string{"apple/docc", "git/rebase"}},
		{"a pack's name and description count for its skills", "", false, []string{"ios"}, []string{"apple/docc", "apple/pr"}},
		{"combined with enabled and pack", "git", true, []string{"re"}, []string{"git/rebase"}},
		{"a term that is no regular expression is taken literally", "", false, []string{"(pull"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := view.Filter(tt.pack, tt.enabled, tt.terms)
			if !reflect.DeepEqual(names(got), tt.want) {
				t.Errorf("got %v, want %v", names(got), tt.want)
			}
			for _, pack := range got.Packs {
				for _, skill := range pack.Skills {
					if _, ok := got.Skills[skill]; !ok {
						t.Errorf("%s is listed in a pack but missing from the skills", skill)
					}
				}
			}
		})
	}
}

func TestSourceThatTakesAllSkills(t *testing.T) {
	e := setup(t)
	e.open(t, e.config("", "[enabled]\npacks = ['acme']\n"))
	global := filepath.Join(e.p.Home, ".claude", "skills")
	if !isLink(filepath.Join(global, "alpha")) || !isLink(filepath.Join(global, "beta")) {
		t.Fatal("all skills of the source are enabled")
	}

	// A skill the repository gains later joins the source and the pack.
	write(t, filepath.Join(e.origin, "skills/gamma/SKILL.md"), "---\nname: gamma\ndescription: The gamma skill\n---\n")
	git(t, e.origin, "add", "-A")
	git(t, e.origin, "commit", "-q", "-m", "add gamma")
	if _, err := e.app.Update(false); err != nil {
		t.Fatal(err)
	}
	if !e.app.Catalog.HasSkill("gamma") {
		t.Fatal("gamma should be in the catalog after the update")
	}
	if !isLink(filepath.Join(global, "gamma")) {
		t.Error("the enabled pack covers the new skill")
	}
	reopened, _ := Open(e.p)
	if got, _ := reopened.Enabled(reopened.Global()); !reflect.DeepEqual(got, []string{"alpha", "beta", "gamma"}) {
		t.Errorf("enabled after reopen: %v", got)
	}
}

func TestProjectScope(t *testing.T) {
	e := setup(t)
	e.add(t, false)
	os.MkdirAll(filepath.Join(e.p.Cwd, ".git"), 0o755)
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
	if _, err := os.Stat(filepath.Join(e.p.Cwd, paths.ManifestName)); !os.IsNotExist(err) {
		t.Error("enabling must not write a manifest")
	}

	// The repository is found from a subdirectory.
	sub := e.p
	sub.Cwd = filepath.Join(e.p.Cwd, "deep", "er")
	os.MkdirAll(sub.Cwd, 0o755)
	below, _ := Open(sub)
	if got, _ := below.ProjectScope(); got.Root != e.p.Cwd {
		t.Errorf("root from subdirectory: %s", got.Root)
	}
	if view, _ := below.View(); !view.Skills["alpha"].Project || view.Skills["alpha"].Global {
		t.Errorf("the view reads the project's links: %+v", view.Skills["alpha"])
	}

	home := e.p
	home.Cwd = e.p.Home
	atHome, _ := Open(home)
	if _, err := atHome.ProjectScope(); err == nil {
		t.Error("the home directory must not become a project")
	}
}

func TestProjectManifestIsACatalogOfItsOwn(t *testing.T) {
	e := setup(t)
	e.open(t, "agents = ['claude-code', 'codex']\n")
	write(t, filepath.Join(e.p.Cwd, paths.ManifestName), fmt.Sprintf(
		"[sources.'acme/skills']\nurl = %q\nskills = ['alpha', 'beta']\n\n[packs.acme]\ndescription = 'Acme skills'\n"+
			"skills = ['alpha', 'beta']\n\n[enabled]\npacks = ['acme']\nexcept = ['beta']\n", e.origin))
	sub := e.p
	sub.Cwd = filepath.Join(e.p.Cwd, "deep")
	os.MkdirAll(sub.Cwd, 0o755)
	a, err := Open(sub)
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := a.ProjectScope()
	if scope.Root != e.p.Cwd {
		t.Fatalf("the manifest marks the project root: %s", scope.Root)
	}
	if _, err := a.Sync(scope, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(e.p.RepoDir("acme/skills"), "skills", "alpha")
	for _, dir := range []string{".claude/skills", ".agents/skills"} {
		if got, _ := os.Readlink(filepath.Join(e.p.Cwd, dir, "alpha")); got != want {
			t.Errorf("%s/alpha -> %q", dir, got)
		}
		if isLink(filepath.Join(e.p.Cwd, dir, "beta")) {
			t.Errorf("%s/beta is excepted", dir)
		}
	}
	if _, err := a.Sync(a.Global(), SyncOptions{}); err != nil || isLink(filepath.Join(e.p.Home, ".claude", "skills", "alpha")) {
		t.Errorf("a manifest enables for its project only: %v", err)
	}
	if view, _ := a.View(); view.Skills["alpha"].From != paths.ManifestName || !view.Skills["alpha"].Project {
		t.Errorf("view: %+v", view.Skills["alpha"])
	}

	outside := e.p
	outside.Cwd = filepath.Join(e.p.Home, "other")
	os.MkdirAll(outside.Cwd, 0o755)
	if elsewhere, _ := Open(outside); elsewhere.Catalog.HasSkill("alpha") {
		t.Error("outside the project its catalog is unknown")
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
	if _, err := os.Stat(e.p.ConfigFile()); !os.IsNotExist(err) {
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
		got, _ := os.Readlink(filepath.Join(global, name))
		if want := filepath.Join(e.p.RepoDir("acme/skills"), "skills", name); got != want {
			t.Errorf("~/.claude/skills/%s -> %q, want %s", name, got, want)
		}
		if _, err := os.Lstat(filepath.Join(legacy, name)); !os.IsNotExist(err) {
			t.Errorf("the installed copy of %s must be deleted", name)
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
