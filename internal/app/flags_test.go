package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bonkey/skillet/internal/paths"
)

func TestSaveWritesTheFlagsIntoTheConfig(t *testing.T) {
	e := setup(t)
	e.add(t, true)
	global := filepath.Join(e.p.Home, ".claude", "skills")
	config := func() string { return read(t, e.p.ConfigFile()) }

	report, err := e.app.Toggle(e.app.Global(), false, true, "beta")
	if err != nil || len(report.Notes) != 0 || isLink(filepath.Join(global, "beta")) {
		t.Fatalf("disable --save: %+v %v", report, err)
	}
	if !strings.Contains(config(), "only = ['alpha', {name = 'beta', enabled = false}]") {
		t.Fatalf("the skill is switched off in the file:\n%s", config())
	}
	if report, err := e.app.Sync(e.app.Global(), SyncOptions{}); err != nil || len(report.Extra) != 0 || isLink(filepath.Join(global, "beta")) {
		t.Fatalf("sync leaves it off: %+v %v", report, err)
	}

	if _, err := e.app.Toggle(e.app.Global(), false, true, "@acme"); err != nil || isLink(filepath.Join(global, "alpha")) {
		t.Fatalf("disable --save @acme: %v", err)
	}
	if !strings.Contains(config(), "[[packs]]\nname = 'acme'\ndescription = 'Acme skills'\nenabled = false\n") {
		t.Fatalf("the pack is switched off in the file:\n%s", config())
	}

	// A skill whose only pack is off cannot come on by itself.
	_, err = e.app.Toggle(e.app.Global(), true, true, "alpha")
	if err == nil || !strings.Contains(err.Error(), "enable --save @acme") || isLink(filepath.Join(global, "alpha")) {
		t.Fatalf("enable --save alpha inside a pack that is off: %v", err)
	}

	if _, err := e.app.Toggle(e.app.Global(), true, true, "@acme", "beta"); err != nil {
		t.Fatal(err)
	}
	if c := config(); strings.Contains(c, "enabled") || !strings.Contains(c, "only = ['alpha', 'beta']") {
		t.Fatalf("switching on drops the flags:\n%s", c)
	}
	if !isLink(filepath.Join(global, "alpha")) || !isLink(filepath.Join(global, "beta")) {
		t.Fatal("both are linked again")
	}

	if _, err := e.app.Toggle(e.app.Global(), false, true, "skills:skills"); err != nil {
		t.Fatal(err)
	}
	if isLink(filepath.Join(global, "alpha")) || !strings.Contains(config(), "enabled = false\nonly = ['alpha', 'beta']") {
		t.Fatalf("disable --save of a source:\n%s", config())
	}
	if _, err := e.app.Toggle(e.app.Global(), true, true, "alpha"); err == nil || !strings.Contains(err.Error(), "enable --save skills:skills") {
		t.Fatalf("enable --save alpha inside a source that is off: %v", err)
	}
	if _, err := e.app.Toggle(e.app.Global(), true, true, "skills:skills"); err != nil || !isLink(filepath.Join(global, "alpha")) {
		t.Fatalf("enable --save of a source: %v", err)
	}

	// Without --save the file stays as it is and the note says so.
	report, err = e.app.Toggle(e.app.Global(), false, false, "alpha")
	if err != nil || len(report.Notes) != 1 || !strings.Contains(report.Notes[0], "disable --save") || strings.Contains(config(), "enabled") {
		t.Fatalf("disable without --save: %+v %v\n%s", report, err, config())
	}
}

func TestSaveFlagsServers(t *testing.T) {
	e := setup(t)
	withServers(t, &e)
	claude := filepath.Join(e.p.Home, ".claude.json")
	config := func() string { return read(t, e.p.ConfigFile()) }

	if _, err := e.app.Toggle(e.app.Global(), true, true, "mcp:simctl"); err == nil || !strings.Contains(err.Error(), "enable --save @ios") {
		t.Fatalf("a server held by a pack that is off: %v", err)
	}
	if _, err := e.app.Toggle(e.app.Global(), true, true, "@ios"); err != nil || !strings.Contains(read(t, claude), "simctl") {
		t.Fatalf("enable --save @ios: %v", err)
	}
	if strings.Contains(config(), "Building for iOS'\nenabled = false") {
		t.Fatalf("the pack is on:\n%s", config())
	}
	if _, err := e.app.Toggle(e.app.Global(), false, true, "mcp:simctl"); err != nil || strings.Contains(read(t, claude), "simctl") {
		t.Fatalf("disable --save mcp:simctl: %v", err)
	}
	if !strings.Contains(config(), "[[mcps]]\nname = 'simctl'\ncommand = ['npx', '-y', 'simctl-mcp']\nenabled = false\n") {
		t.Fatalf("the server is switched off in the file:\n%s", config())
	}
	if report, err := e.app.Sync(e.app.Global(), SyncOptions{}); err != nil || len(report.Extra) != 0 || strings.Contains(read(t, claude), "simctl") {
		t.Fatalf("sync leaves it off: %+v %v", report, err)
	}
}

func TestSaveInAProjectFlagsTheManifestOnly(t *testing.T) {
	e := setup(t)
	// The global catalog knows alpha and switches its pack off; the manifest
	// brings beta through its own source and alpha through its own pack.
	e.open(t, e.config("only = ['alpha']", "enabled = false\n"))
	manifest := filepath.Join(e.p.Cwd, paths.ManifestName)
	write(t, manifest, fmt.Sprintf("[[packs]]\nname = 'proj'\ndescription = 'Project pack'\nskills = ['alpha']\n\n[[skills]]\nname = 'mine'\nurl = %q\nonly = ['beta']\n", e.origin))
	a, err := Open(e.p)
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := a.ProjectScope()
	if _, err := a.Sync(scope, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(e.p.Cwd, ".claude", "skills")
	if !isLink(filepath.Join(local, "alpha")) || !isLink(filepath.Join(local, "beta")) {
		t.Fatal("the manifest's own pack and source are on in the project")
	}

	if _, err := a.Toggle(scope, false, true, "@proj"); err != nil || isLink(filepath.Join(local, "alpha")) {
		t.Fatalf("disable -p --save @proj: %v", err)
	}
	if !strings.Contains(read(t, manifest), "[[packs]]\nname = 'proj'\ndescription = 'Project pack'\nenabled = false\n") {
		t.Fatalf("the manifest holds the flag:\n%s", read(t, manifest))
	}
	if _, err := a.Toggle(scope, false, true, "@acme"); err == nil || !strings.Contains(err.Error(), "own entries") {
		t.Fatalf("a project cannot flag a global pack: %v", err)
	}
	if _, err := a.Toggle(a.Global(), false, true, "@proj"); err == nil || !strings.Contains(err.Error(), "use -p") {
		t.Fatalf("the global scope cannot flag a project's pack: %v", err)
	}
	if strings.Contains(read(t, e.p.ConfigFile()), "proj") {
		t.Error("the global config must stay untouched")
	}
	if _, err := os.Stat(manifest); err != nil {
		t.Fatal(err)
	}
}

func TestSaveRejectsEntriesOfAGist(t *testing.T) {
	e := setup(t)
	e.add(t, true)
	gists := &fakeGists{files: map[string]string{gistA: manifest("theirs", e.origin, "gamma")}}
	e.app.Gists = gists
	if _, err := e.app.Include(gistA); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"@pack-gamma", "skills:theirs"} {
		if _, err := e.app.Toggle(e.app.Global(), false, true, name); err == nil || !strings.Contains(err.Error(), "gist") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
