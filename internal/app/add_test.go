package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bonkey/skillet/internal/paths"
	"github.com/bonkey/skillet/internal/source"
)

func TestAddWritesTheSourceAndLinksNothing(t *testing.T) {
	e := setup(t)
	report, err := e.app.Add(e.app.Global(), AddRequest{Arg: e.origin})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Added, []string{"skills:skills"}) || report.File != e.p.ConfigFile() {
		t.Fatalf("report: %+v", report)
	}
	if got := read(t, e.p.ConfigFile()); !strings.Contains(got, "[[skills]]\nurl = '"+e.origin+"'\n") || strings.Contains(got, "only") {
		t.Fatalf("config:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(e.p.RepoDir("skills"), "skills", "alpha", "SKILL.md")); err != nil {
		t.Fatalf("the source is cloned: %v", err)
	}
	if isLink(filepath.Join(e.p.Home, ".claude", "skills", "alpha")) {
		t.Fatal("add links nothing; sync does")
	}
	if got := e.app.Declared(e.app.Global()).Skills; !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("the config switches the whole source on: %v", got)
	}
	if entries, _ := os.ReadDir(e.p.Data); len(entries) != 2 {
		t.Errorf("the temporary clone is gone: %v", entries)
	}

	again, err := e.app.Add(e.app.Global(), AddRequest{Arg: e.origin})
	if err != nil || len(again.Added) > 0 || !reflect.DeepEqual(again.Present, []string{"skills:skills"}) || again.File != "" {
		t.Fatalf("adding it again: %+v, %v", again, err)
	}
}

func TestAddTakesTheSkillsOfAPastedCommand(t *testing.T) {
	e := setup(t)
	command := func(skill string) []string {
		return strings.Fields("npx skills add " + e.origin + " -g --skill " + skill + " -y")
	}
	report, err := e.app.Add(e.app.Global(), AddRequest{Command: command("alpha")})
	if err != nil || !reflect.DeepEqual(report.Added, []string{"alpha@skills"}) {
		t.Fatalf("report: %+v, %v", report, err)
	}
	if got := read(t, e.p.ConfigFile()); !strings.Contains(got, "only = ['alpha']") {
		t.Fatalf("config:\n%s", got)
	}

	report, err = e.app.Add(e.app.Global(), AddRequest{Command: command("beta")})
	if err != nil || !reflect.DeepEqual(report.Added, []string{"beta@skills"}) || !strings.Contains(read(t, e.p.ConfigFile()), "only = ['alpha', 'beta']") {
		t.Fatalf("a second skill joins the list: %+v, %v\n%s", report, err, read(t, e.p.ConfigFile()))
	}
	report, err = e.app.Add(e.app.Global(), AddRequest{Command: command("alpha")})
	if err != nil || !reflect.DeepEqual(report.Present, []string{"alpha@skills"}) {
		t.Fatalf("a listed skill is present: %+v, %v", report, err)
	}

	report, err = e.app.Add(e.app.Global(), AddRequest{Arg: e.origin})
	if err != nil || !reflect.DeepEqual(report.Added, []string{"skills:skills"}) || strings.Contains(read(t, e.p.ConfigFile()), "only") {
		t.Fatalf("without skills the source takes all: %+v, %v\n%s", report, err, read(t, e.p.ConfigFile()))
	}
}

func TestAddRefusesASkillTheSourceLacks(t *testing.T) {
	e := setup(t)
	_, err := e.app.Add(e.app.Global(), AddRequest{Arg: e.origin, Skills: []string{"gamma"}})
	if err == nil || !strings.Contains(err.Error(), "gamma") || !strings.Contains(err.Error(), "alpha, beta") {
		t.Fatalf("err: %v", err)
	}
	if _, err := os.Stat(e.p.ConfigFile()); !os.IsNotExist(err) {
		t.Error("nothing is written")
	}
	if entries, _ := os.ReadDir(e.p.Data); len(entries) > 0 {
		t.Errorf("no clone stays: %v", entries)
	}
}

func TestAddReadsASubpath(t *testing.T) {
	e := setup(t)
	report := AddReport{}
	if err := e.app.addSource(e.app.Global(), e.app.Local, source.Spec{URL: e.origin, Path: "skills/beta"}, nil, &report); err != nil {
		t.Fatal(err)
	}
	if src := e.app.Local.Sources["skills"]; src == nil || src.Path != "" || !reflect.DeepEqual(src.Skills, []string{"beta"}) {
		t.Fatalf("a skill folder is one skill of the whole repository: %+v", src)
	}

	e = setup(t)
	if err := e.app.addSource(e.app.Global(), e.app.Local, source.Spec{URL: e.origin, Path: "skills"}, nil, &report); err != nil {
		t.Fatal(err)
	}
	if src := e.app.Local.Sources["skills"]; src == nil || src.Path != "skills" || len(src.Skills) > 0 {
		t.Fatalf("a directory of skills is the path: %+v", src)
	}
}

func TestAddNamesANewSourceWithoutRenamingOthers(t *testing.T) {
	e := setup(t)
	other := filepath.Join(filepath.Dir(filepath.Dir(e.origin)), "other", "skills")
	write(t, filepath.Join(other, "gamma", "SKILL.md"), "---\nname: gamma\ndescription: The gamma skill\n---\n")
	git(t, other, "init", "-q", "-b", "main")
	git(t, other, "add", "-A")
	git(t, other, "commit", "-q", "-m", "init")
	e.open(t, "agents = ['claude-code']\n\n[[skills]]\nurl = '"+other+"'\n")

	report, err := e.app.Add(e.app.Global(), AddRequest{Arg: e.origin})
	if err != nil || !reflect.DeepEqual(report.Added, []string{"skills:acme-skills"}) {
		t.Fatalf("report: %+v, %v", report, err)
	}
	if !source.SameRepo(e.app.Local.Sources["skills"].URL, other) || e.app.Local.Sources["acme-skills"].Name != "acme-skills" {
		t.Fatalf("sources: %v", e.app.SourceNames())
	}
	if _, err := os.Stat(e.p.RepoDir("acme-skills")); err != nil {
		t.Fatalf("the clone goes by the new name: %v", err)
	}
}

func TestAddInAProjectWritesItsManifest(t *testing.T) {
	e := setup(t)
	scope, _ := e.app.ProjectScope()
	report, err := e.app.Add(scope, AddRequest{Arg: e.origin, Skills: []string{"beta"}})
	manifest := filepath.Join(e.p.Cwd, paths.ManifestName)
	if err != nil || report.File != manifest {
		t.Fatalf("report: %+v, %v", report, err)
	}
	if got := read(t, manifest); !strings.Contains(got, "only = ['beta']") {
		t.Fatalf("manifest:\n%s", got)
	}
	if _, err := os.Stat(e.p.ConfigFile()); !os.IsNotExist(err) {
		t.Error("config.toml stays as it is")
	}
	if got := e.app.Declared(scope).Skills; !reflect.DeepEqual(got, []string{"beta"}) {
		t.Fatalf("the project switches beta on: %v", got)
	}
	if _, err := e.app.Add(scope, AddRequest{Command: []string{"npx", "mobile-mcp"}}); err == nil || !strings.Contains(err.Error(), "global") {
		t.Fatalf("servers are global: %v", err)
	}
}

func TestAddServerFromACommand(t *testing.T) {
	e := setup(t)
	command := strings.Fields("npx @mobilenext/mobile-mcp@latest")
	report, err := e.app.Add(e.app.Global(), AddRequest{Command: command})
	if err != nil || !reflect.DeepEqual(report.Added, []string{"mcp:mobile-mcp"}) {
		t.Fatalf("report: %+v, %v", report, err)
	}
	if got := read(t, e.p.ConfigFile()); !strings.Contains(got, "[[mcps]]\ncommand = ['npx', '@mobilenext/mobile-mcp@latest']\n") {
		t.Fatalf("config:\n%s", got)
	}
	if again, err := e.app.Add(e.app.Global(), AddRequest{Command: command}); err != nil || !reflect.DeepEqual(again.Present, []string{"mcp:mobile-mcp"}) {
		t.Fatalf("the same command is present: %+v, %v", again, err)
	}
	other := []string{"npx", "-y", "mobile-mcp"}
	if _, err := e.app.Add(e.app.Global(), AddRequest{Command: other}); err == nil || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("a taken name: %v", err)
	}
	if report, err := e.app.Add(e.app.Global(), AddRequest{Command: other, Name: "mobile"}); err != nil || !reflect.DeepEqual(report.Added, []string{"mcp:mobile"}) {
		t.Fatalf("--name: %+v, %v", report, err)
	}
	if _, err := e.app.Add(e.app.Global(), AddRequest{Command: command, Skills: []string{"x"}}); err == nil {
		t.Fatal("--skill takes skills, not servers")
	}
	if _, err := e.app.Add(e.app.Global(), AddRequest{Arg: e.origin, Name: "x"}); err == nil {
		t.Fatal("--name names servers, not sources")
	}
}

func TestAddServerURLKeepsItsSecret(t *testing.T) {
	e := setup(t)
	report, err := e.app.Add(e.app.Global(), AddRequest{Arg: "https://mcp.tavily.com/mcp/?tavilyApiKey=made-up", MCP: true})
	if err != nil || !reflect.DeepEqual(report.Added, []string{"mcp:tavily"}) || !reflect.DeepEqual(report.Secrets, []string{"TAVILY_API_KEY"}) {
		t.Fatalf("report: %+v, %v", report, err)
	}
	if got := read(t, e.p.ConfigFile()); !strings.Contains(got, "url = 'https://mcp.tavily.com/mcp/?tavilyApiKey=${TAVILY_API_KEY}'") {
		t.Fatalf("config:\n%s", got)
	}
	if got := read(t, e.p.SecretsFile()); !strings.Contains(got, "made-up") {
		t.Fatalf("secrets:\n%s", got)
	}
	again, err := e.app.Add(e.app.Global(), AddRequest{Arg: "https://mcp.tavily.com/mcp/?tavilyApiKey=made-up", MCP: true})
	if err != nil || !reflect.DeepEqual(again.Present, []string{"mcp:tavily"}) {
		t.Fatalf("the same URL is present: %+v, %v", again, err)
	}
}

func TestAddTellsAServerURLFromARepository(t *testing.T) {
	e := setup(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer server.Close()
	report, err := e.app.Add(e.app.Global(), AddRequest{Arg: server.URL + "/mcp", Name: "local"})
	if err != nil || !reflect.DeepEqual(report.Added, []string{"mcp:local"}) {
		t.Fatalf("an MCP endpoint is a server: %+v, %v", report, err)
	}

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<html></html>")
	}))
	defer page.Close()
	if _, err := e.app.Add(e.app.Global(), AddRequest{Arg: page.URL}); err == nil || !strings.Contains(err.Error(), "add mcp") {
		t.Fatalf("a web page is neither: %v", err)
	}
	if _, err := e.app.Add(e.app.Global(), AddRequest{Arg: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("a plain directory is not a source: %v", err)
	}
}

func TestAddRefusesWhatTheLocalFileDefines(t *testing.T) {
	e := setup(t)
	write(t, e.p.LocalConfigFile(), "[[mcps]]\ncommand = ['npx', 'mobile-mcp']\n")
	e.open(t, "agents = ['claude-code']\n")
	if _, err := e.app.Add(e.app.Global(), AddRequest{Command: []string{"npx", "mobile-mcp"}}); err == nil || !strings.Contains(err.Error(), "config.local.toml") {
		t.Fatalf("err: %v", err)
	}
}
