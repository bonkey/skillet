package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/secrets"
)

// writeSecrets stands for the user editing secrets.yaml.
func writeSecrets(t *testing.T, e env, content string) {
	t.Helper()
	write(t, e.p.SecretsFile(), content)
}

func read(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// withServers gives the app two servers, one of them needing a secret, in a
// pack together with a skill.
func withServers(t *testing.T, e env) {
	t.Helper()
	e.add(t, false)
	e.app.Local.Agents = []string{"claude-code", "codex"}
	e.app.Local.MCPs["simctl"] = &catalog.MCP{Type: "local", Command: []string{"npx", "-y", "simctl-mcp"}}
	e.app.Local.MCPs["tavily"] = &catalog.MCP{Type: "remote", URL: "https://mcp.tavily.com/mcp?tavilyApiKey=${TAVILY_API_KEY}"}
	err := e.app.EditPack("ios", true, func(local *catalog.Catalog) error {
		return local.CreatePack("ios", "Building for iOS", []string{"alpha", "mcp:simctl", "mcp:tavily"})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEnablingAPackWritesItsServers(t *testing.T) {
	e := setup(t)
	withServers(t, e)
	claude, codex := filepath.Join(e.p.Home, ".claude.json"), filepath.Join(e.p.Home, ".codex", "config.toml")
	write(t, claude, "{\n  \"numStartups\": 3\n}\n")

	report, err := e.app.Toggle(e.app.Global(), true, "@ios")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.MissingSecrets, map[string][]string{"tavily": {"TAVILY_API_KEY"}}) {
		t.Fatalf("a server without its secret is left alone and reported: %+v", report.MissingSecrets)
	}
	if text := read(t, claude); !strings.Contains(text, `"simctl"`) || strings.Contains(text, "tavily") || !strings.Contains(text, `"numStartups": 3`) {
		t.Fatalf("claude config:\n%s", text)
	}
	if !isLink(filepath.Join(e.p.Home, ".claude", "skills", "alpha")) {
		t.Error("the pack's skill is linked too")
	}

	writeSecrets(t, e, "TAVILY_API_KEY: tvly-secret\n")
	if _, err := e.app.Sync(e.app.Global(), SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	if text := read(t, codex); !strings.Contains(text, `url = "https://mcp.tavily.com/mcp?tavilyApiKey=tvly-secret"`) {
		t.Errorf("codex config:\n%s", text)
	}
	if raw := read(t, e.p.ConfigFile()); strings.Contains(raw, "tvly-secret") {
		t.Error("a secret value must never reach the catalog")
	}
	view, _ := e.app.View()
	if got := view.MCPs["tavily"]; !got.Global || got.Target != "https://mcp.tavily.com/mcp?tavilyApiKey=${TAVILY_API_KEY}" || len(got.MissingSecrets) != 0 {
		t.Errorf("view: %+v", got)
	}

	if _, err := e.app.Toggle(e.app.Global(), false, "mcp:tavily"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read(t, claude), "tavily") || !reflect.DeepEqual(e.app.Local.Enabled.Except, []string{"mcp:tavily"}) {
		t.Errorf("disable one server of the pack: %+v", e.app.Local.Enabled)
	}
	if _, err := e.app.Toggle(e.app.Global(), false, "@ios"); err != nil {
		t.Fatal(err)
	}
	if text := read(t, claude); text != "{\n  \"numStartups\": 3,\n  \"mcpServers\": {\n  }\n}\n" && strings.Contains(text, "simctl") {
		t.Errorf("after disabling the pack:\n%s", text)
	}
	if _, err := e.app.Remove("mcp:simctl"); err != nil || e.app.Catalog.MCPs["simctl"] != nil {
		t.Errorf("remove: %v", err)
	}
}

func TestServersAreGlobalOnly(t *testing.T) {
	e := setup(t)
	withServers(t, e)
	scope, _ := e.app.ProjectScope()
	if _, err := e.app.Toggle(scope, true, "mcp:simctl"); err == nil {
		t.Error("a server cannot be enabled in a project")
	}
	report, err := e.app.Toggle(scope, true, "@ios")
	if err != nil || len(report.Notes) != 1 || !strings.Contains(report.Notes[0], "simctl") {
		t.Fatalf("a pack's servers are skipped with a note: %+v %v", report.Notes, err)
	}
	if _, err := os.Stat(filepath.Join(e.p.Home, ".claude.json")); !os.IsNotExist(err) {
		t.Error("a project must not write agent configs")
	}
}

func TestRunWritesServersOnlyWhileTheCommandRuns(t *testing.T) {
	e := setup(t)
	withServers(t, e)
	claude, codex := filepath.Join(e.p.Home, ".claude.json"), filepath.Join(e.p.Home, ".codex", "config.toml")

	// A command that is no agent: the servers go to every configured agent.
	code, err := e.app.Run([]string{"mcp:simctl", "alpha"}, []string{"grep", "-q", "simctl-mcp", claude, codex})
	if err != nil || code != 0 {
		t.Fatalf("the server should be configured during the run: %d %v", code, err)
	}
	if strings.Contains(read(t, claude), "simctl") || strings.Contains(read(t, codex), "simctl") {
		t.Errorf("the server should be gone after the run:\n%s", read(t, claude))
	}
	if entries, _ := os.ReadDir(e.p.SessionsDir()); len(entries) != 0 {
		t.Error("the session file should be gone")
	}
}

func TestRunLimitsServersToTheAgentItStarts(t *testing.T) {
	e := setup(t)
	withServers(t, e)
	claude, codex := filepath.Join(e.p.Home, ".claude.json"), filepath.Join(e.p.Home, ".codex", "config.toml")
	gemini := filepath.Join(e.p.Home, ".gemini", "settings.json")

	// Stand-ins for the agents: scripts named like them that check the configs.
	bin := t.TempDir()
	write(t, filepath.Join(bin, "codex"), "#!/bin/sh\ngrep -q simctl-mcp \"$1\" && ! grep -q simctl-mcp \"$2\" 2>/dev/null\n")
	write(t, filepath.Join(bin, "gemini"), "#!/bin/sh\ngrep -q simctl-mcp \"$1\"\n")
	os.Chmod(filepath.Join(bin, "codex"), 0o755)
	os.Chmod(filepath.Join(bin, "gemini"), 0o755)

	code, err := e.app.Run([]string{"mcp:simctl"}, []string{filepath.Join(bin, "codex"), codex, claude})
	if err != nil || code != 0 {
		t.Fatalf("only the started agent gets the server: %d %v", code, err)
	}
	// gemini-cli is not under `agents`, and still gets the server it is started with.
	code, err = e.app.Run([]string{"mcp:simctl"}, []string{filepath.Join(bin, "gemini"), gemini})
	if err != nil || code != 0 {
		t.Fatalf("an agent that is not configured gets its session servers: %d %v", code, err)
	}
	if strings.Contains(read(t, gemini), "simctl") {
		t.Errorf("and loses them afterwards:\n%s", read(t, gemini))
	}
}

func TestImportMCPSetup(t *testing.T) {
	e := setup(t)
	claude := filepath.Join(e.p.Home, ".claude.json")
	write(t, e.app.MCPSetupConfig(), `{
  "presets": {"research": ["tavily", "firecrawl"], "ios-dev": ["simctl"], "empty": ["ghost"]},
  "mcps": {
    "tavily": {"type": "remote", "url": "https://mcp.tavily.com/mcp/?tavilyApiKey=tvly-1"},
    "firecrawl": {"type": "local", "command": ["npx", "-y", "firecrawl-mcp"], "environment": {"FIRECRAWL_API_KEY": "fc-1"}},
    "simctl": {"type": "local", "command": ["npx", "-y", "simctl-mcp"], "timeout": 20},
    "broken": {"type": "local"}
  }
}`)
	write(t, claude, `{"mcpServers": {"tavily": {"type": "http", "url": "https://old"}, "mine": {"url": "https://mine"}}}`)
	before := read(t, claude)

	report, err := e.app.ImportMCP(e.app.MCPSetupConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Servers, []string{"firecrawl", "simctl", "tavily"}) || report.Skipped["broken"] == "" {
		t.Errorf("servers: %v skipped: %v", report.Servers, report.Skipped)
	}
	if !reflect.DeepEqual(report.Packs, []string{"ios-dev", "research"}) {
		t.Errorf("packs: %v", report.Packs)
	}
	if read(t, claude) != before || !e.app.Local.Enabled.Empty() {
		t.Error("an import enables nothing and changes no agent config")
	}
	reopened, _ := Open(e.p)
	if got := reopened.Catalog.MCPs["firecrawl"]; got == nil || got.Environment["FIRECRAWL_API_KEY"] != "fc-1" || reopened.Catalog.MCPs["simctl"].Timeout != 20 {
		t.Errorf("definitions are copied as they are: %+v", got)
	}
	if got := reopened.Catalog.Packs["research"]; got == nil || !reflect.DeepEqual(got.MCPs, []string{"firecrawl", "tavily"}) || got.Description == "" {
		t.Errorf("presets become packs: %+v", got)
	}

	// Enabling meets the entry mcp-setup left under the same name and overwrites it.
	if _, err := e.app.Toggle(e.app.Global(), true, "mcp:tavily"); err != nil {
		t.Fatal(err)
	}
	if text := read(t, claude); !strings.Contains(text, "tavilyApiKey=tvly-1") || strings.Contains(text, "https://old") || !strings.Contains(text, "https://mine") {
		t.Errorf("claude config:\n%s", text)
	}

	again, err := e.app.ImportMCP(e.app.MCPSetupConfig())
	if err != nil || len(again.Servers) != 0 || len(again.Skipped) != 4 {
		t.Errorf("a second import adds nothing: %+v %v", again, err)
	}
}

// fakeOnePassword serves item fields from memory and counts the reads.
type fakeOnePassword struct {
	items map[string]map[string]string
	reads int
}

func (f *fakeOnePassword) Fields(item secrets.Item) (map[string]string, error) {
	f.reads++
	if fields, ok := f.items[item.Item]; ok {
		return fields, nil
	}
	return nil, os.ErrPermission
}

func TestSecretsComeFromOnePasswordItemsOnlyWhenNeeded(t *testing.T) {
	e := setup(t)
	withServers(t, e)
	op := &fakeOnePassword{items: map[string]map[string]string{"personal": {"TAVILY_API_KEY": "from-1password"}}}
	e.app.OnePassword = op
	e.app.Local.Secrets = []catalog.SecretItem{{Account: "me.1password.com", Vault: "v", Item: "personal"}}
	e.app.Save()
	claude := filepath.Join(e.p.Home, ".claude.json")

	if _, err := e.app.Toggle(e.app.Global(), true, "mcp:simctl"); err != nil || op.reads != 0 {
		t.Fatalf("a server without placeholders needs no item: %d reads, %v", op.reads, err)
	}
	report, err := e.app.Toggle(e.app.Global(), true, "mcp:tavily")
	if err != nil || len(report.MissingSecrets) != 0 || !strings.Contains(read(t, claude), "tavilyApiKey=from-1password") {
		t.Fatalf("the field of the item fills the placeholder: %+v %v", report, err)
	}
	if raw := read(t, e.p.ConfigFile()) + read(t, e.p.MCPStateFile()); strings.Contains(raw, "from-1password") {
		t.Error("the value must reach neither the catalog nor the state file")
	}
	if _, err := os.Stat(e.p.SecretsFile()); !os.IsNotExist(err) {
		t.Error("nothing is stored locally")
	}

	reads := op.reads
	for range 3 {
		if _, err := e.app.Sync(e.app.Global(), SyncOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	view, _ := e.app.View()
	if op.reads != reads || len(view.MCPs["tavily"].MissingSecrets) != 0 {
		t.Errorf("unchanged syncs and listings read no item: %d -> %d reads", reads, op.reads)
	}

	// A local value wins over the item.
	writeSecrets(t, e, "TAVILY_API_KEY: local-override\n")
	e.app.Local.MCPs["tavily"].URL += "&v=2"
	e.app.Save()
	if _, err := e.app.Sync(e.app.Global(), SyncOptions{}); err != nil || !strings.Contains(read(t, claude), "local-override&v=2") {
		t.Errorf("local override: %v\n%s", err, read(t, claude))
	}
}

func TestAnUnreadableItemLeavesServersAlone(t *testing.T) {
	e := setup(t)
	withServers(t, e)
	e.app.OnePassword = &fakeOnePassword{}
	e.app.Local.Secrets = []catalog.SecretItem{{Account: "me.1password.com", Vault: "v", Item: "locked"}}
	e.app.Save()

	report, err := e.app.Toggle(e.app.Global(), true, "@ios")
	if err != nil {
		t.Fatalf("an item that cannot be read must not fail the sync: %v", err)
	}
	if !reflect.DeepEqual(report.MissingSecrets, map[string][]string{"tavily": {"TAVILY_API_KEY"}}) ||
		len(report.Notes) == 0 || !strings.Contains(report.Notes[0], "1Password") {
		t.Errorf("report: %+v", report)
	}
	if text := read(t, filepath.Join(e.p.Home, ".claude.json")); !strings.Contains(text, "simctl") || strings.Contains(text, "tavily") {
		t.Errorf("the other server is written:\n%s", text)
	}
}
