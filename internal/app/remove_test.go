package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bonkey/skillet/internal/paths"
)

// servers follow the catalog of config: one with a command and, after a
// comment of its own, one with a secret in its URL.
const servers = `
[[mcps]]
name = 'simctl'
command = ['npx', '-y', 'simctl-mcp']

# search
[[mcps]]
url = 'https://mcp.exa.ai/mcp?exaApiKey=${EXA_API_KEY}'
`

func TestRemoveTakesASourceOutAndKeepsTheRest(t *testing.T) {
	e := setup(t)
	e.open(t, e.config("only = ['alpha', 'beta']", "")+servers)
	before := read(t, e.p.ConfigFile())

	report, err := e.app.Remove(e.app.Global(), false, "skills:skills")
	if err != nil || !reflect.DeepEqual(report.Removed, []string{"skills:skills"}) || report.File != e.p.ConfigFile() {
		t.Fatalf("report: %+v, %v", report, err)
	}
	if len(report.Notes) != 1 || !strings.Contains(report.Notes[0], "skills:skills is still named in @acme;") {
		t.Errorf("the pack that still names it is reported: %v", report.Notes)
	}
	block := "[[skills]]\nurl = " + `"` + e.origin + `"` + "\nonly = ['alpha', 'beta']\n\n"
	if got := read(t, e.p.ConfigFile()); got != strings.Replace(before, block, "", 1) {
		t.Fatalf("only the entry goes:\n%s", got)
	}
	if _, ok := e.app.Catalog.Sources["skills"]; ok {
		t.Error("the catalog is reloaded")
	}
	global := filepath.Join(e.p.Home, ".claude", "skills")
	if !isLink(filepath.Join(global, "alpha")) {
		t.Fatal("without sync the links stay")
	}
	if _, err := e.app.Sync(e.app.Global(), SyncOptions{Remove: true}); err != nil || isLink(filepath.Join(global, "alpha")) {
		t.Fatalf("sync --clean disables what was removed: %v", err)
	}
}

func TestRemoveWithSyncDisablesWhatItRemoved(t *testing.T) {
	e := setup(t)
	claude := filepath.Join(e.p.Home, ".claude.json")
	write(t, claude, "{}\n")
	e.open(t, e.config("only = ['alpha', 'beta']", "mcps = ['simctl']\n")+servers)
	global := filepath.Join(e.p.Home, ".claude", "skills")
	if !isLink(filepath.Join(global, "alpha")) || !strings.Contains(read(t, claude), "simctl") {
		t.Fatal("setup: alpha and simctl are enabled")
	}

	report, err := e.app.Remove(e.app.Global(), true, e.origin, "simctl", "https://mcp.exa.ai/mcp?exaApiKey=secret")
	if err != nil || report.Sync == nil || !reflect.DeepEqual(report.Removed, []string{"skills:skills", "mcp:simctl", "mcp:exa"}) {
		t.Fatalf("a path, a name and a URL with its secret: %+v, %v", report, err)
	}
	if isLink(filepath.Join(global, "alpha")) || isLink(filepath.Join(global, "beta")) || strings.Contains(read(t, claude), "simctl") {
		t.Fatal("sync disables what was removed")
	}
	if got := read(t, e.p.ConfigFile()); strings.Contains(got, "[[skills]]") || strings.Contains(got, "[[mcps]]") || strings.Contains(got, "# search") {
		t.Fatalf("config:\n%s", got)
	}
	if again, err := e.app.Sync(e.app.Global(), SyncOptions{}); err != nil || len(again.Extra)+len(again.Actions)+len(again.MCP) != 0 {
		t.Fatalf("nothing is left to sync: %+v, %v", again, err)
	}
}

func TestRemoveBySkillNameTakesItsSourceWhenItIsTheOnlyOne(t *testing.T) {
	e := setup(t)
	e.open(t, e.config("only = ['alpha', 'beta']", ""))
	if _, err := e.app.Remove(e.app.Global(), false, "alpha"); err == nil || !strings.Contains(err.Error(), "remove skills:skills") || !strings.Contains(err.Error(), "disable --save alpha") {
		t.Fatalf("a skill among others: %v", err)
	}

	e.open(t, e.config("only = ['alpha']", ""))
	report, err := e.app.Remove(e.app.Global(), false, "alpha@skills")
	if err != nil || !reflect.DeepEqual(report.Removed, []string{"skills:skills"}) {
		t.Fatalf("the only skill: %+v, %v", report, err)
	}
}

func TestRemoveRefusesWhatTheFileDoesNotHold(t *testing.T) {
	e := setup(t)
	e.open(t, e.config("only = ['alpha', 'beta']", "")+servers)
	before := read(t, e.p.ConfigFile())
	project, _ := e.app.ProjectScope()
	for _, c := range []struct {
		scope Scope
		args  []string
		want  string
	}{
		{e.app.Global(), []string{"nothing"}, `no skill, source or server is named "nothing"`},
		{e.app.Global(), []string{"mcp:nothing"}, "not in the catalog"},
		{e.app.Global(), []string{"https://example.com/mcp?key=secret"}, "has the URL https://example.com/mcp"},
		{project, []string{"mcp:simctl"}, "drop -p"},
		{project, []string{"skills:skills"}, "drop -p"},
		{e.app.Global(), []string{"skills:skills", "nothing"}, "nothing"},
	} {
		_, err := e.app.Remove(c.scope, false, c.args...)
		if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "secret") {
			t.Errorf("%v: %v", c.args, err)
		}
	}
	if got := read(t, e.p.ConfigFile()); got != before {
		t.Fatalf("a refusal writes nothing:\n%s", got)
	}

	write(t, filepath.Join(e.p.Cwd, paths.ManifestName), "[[mcps]]\nname = 'skills'\ncommand = ['npx', 'skills-mcp']\n")
	write(t, e.p.LocalConfigFile(), "disabled = ['beta']\n")
	e.open(t, read(t, e.p.ConfigFile()))
	if _, err := e.app.Remove(e.app.Global(), false, "skills"); err == nil || !strings.Contains(err.Error(), "stands for mcp:skills and skills:skills") {
		t.Errorf("a name of two entries: %v", err)
	}
	if _, err := e.app.Remove(e.app.Global(), false, "mcp:skills"); err == nil || !strings.Contains(err.Error(), "use -p") {
		t.Errorf("a server of the project: %v", err)
	}
	if _, err := e.app.Remove(e.app.Global(), false, "skills:skills"); err == nil || !strings.Contains(err.Error(), "config.local.toml names beta") {
		t.Errorf("the overlay names one of its skills: %v", err)
	}
	if got := read(t, e.p.ConfigFile()); got != before {
		t.Fatalf("a refusal writes nothing:\n%s", got)
	}
	if _, err := os.Stat(e.p.ConfigFile() + ".tmp"); !os.IsNotExist(err) {
		t.Error("no temporary file is left")
	}

	project, _ = e.app.ProjectScope()
	if _, err := e.app.Remove(project, true, "mcp:skills"); err != nil || strings.Contains(read(t, filepath.Join(e.p.Cwd, paths.ManifestName)), "mcps") {
		t.Errorf("-p removes the project's server: %v", err)
	}
}

func TestRemoveOwnerRepoAtSkillTakesTheSkillsRule(t *testing.T) {
	e := setup(t)
	for _, c := range []struct{ only, want string }{
		{"only = ['alpha', 'beta']", "takes other skills too"},
		{"only = ['alpha']", ""},
	} {
		write(t, e.p.ConfigFile(), "agents = ['claude-code']\n\n[[skills]]\nurl = 'https://github.com/acme/skills.git'\n"+c.only+"\n")
		a, err := OpenWith(e.p, e.app.Gists)
		if err != nil {
			t.Fatal(err)
		}
		report, err := a.Remove(a.Global(), false, "acme/skills@alpha")
		switch {
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v", c.only, err)
		case c.want == "" && (err != nil || !reflect.DeepEqual(report.Removed, []string{"skills:skills"})):
			t.Errorf("%s: %+v, %v", c.only, report, err)
		}
	}
}
