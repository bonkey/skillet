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

	store := secrets.Store{"TAVILY_API_KEY": "tvly-secret"}
	store.Save(e.p.SecretsFile())
	if _, err := e.app.Sync(e.app.Global(), SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	if text := read(t, codex); !strings.Contains(text, `url = "https://mcp.tavily.com/mcp?tavilyApiKey=tvly-secret"`) {
		t.Errorf("codex config:\n%s", text)
	}
	if raw := read(t, e.p.CatalogFile()); strings.Contains(raw, "tvly-secret") {
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
	e.app.Local.Agents = []string{"claude-code", "opencode"}
	e.app.Save()
	claude := filepath.Join(e.p.Home, ".claude.json")
	opencode := filepath.Join(e.p.Home, ".config", "opencode", "opencode.json")
	cursor := filepath.Join(e.p.Home, ".cursor", "mcp.json")
	write(t, e.app.MCPSetupConfig(), `{
  "presets": {"research": ["tavily", "firecrawl"], "ios-dev": ["simctl"], "empty": ["ghost"]},
  "mcps": {
    "tavily": {"type": "remote", "url": "https://mcp.tavily.com/mcp/?tavilyApiKey=tvly-1&profile=x"},
    "firecrawl": {"type": "local", "command": ["npx", "-y", "firecrawl-mcp", "--api-token=fc-arg"],
                  "environment": {"FIRECRAWL_API_KEY": "fc-1"}},
    "simctl": {"type": "local", "command": ["npx", "-y", "simctl-mcp"]},
    "hdr": {"type": "remote", "url": "https://x/mcp", "headers": {"Authorization": "Bearer hdr-1"}},
    "broken": {"type": "local"}
  }
}`)
	// What mcp-setup left behind: an entry that differs from skillet's
	// rendering, a disabled one, and one in an agent that is not configured.
	write(t, claude, `{"mcpServers": {"tavily": {"type": "http", "url": "https://old"}, "mine": {"url": "https://mine"}}}`)
	write(t, opencode, `{"mcp": {"simctl": {"type": "local", "command": ["npx", "-y", "simctl-mcp"], "enabled": false}}}`)
	write(t, cursor, `{"mcpServers": {"hdr": {"url": "https://x/mcp"}}}`)

	before := map[string]string{claude: read(t, claude), opencode: read(t, opencode), cursor: read(t, cursor)}
	report, err := e.app.ImportMCP(e.app.MCPSetupConfig(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Servers, []string{"firecrawl", "hdr", "simctl", "tavily"}) || report.Skipped["broken"] == "" {
		t.Errorf("servers: %v skipped: %v", report.Servers, report.Skipped)
	}
	if !reflect.DeepEqual(report.Packs, []string{"ios-dev", "research"}) || !reflect.DeepEqual(report.OtherAgents, []string{"cursor"}) {
		t.Errorf("packs: %v, other agents: %v", report.Packs, report.OtherAgents)
	}
	for file, content := range before {
		if read(t, file) != content {
			t.Errorf("a plain import must not change %s", file)
		}
	}
	if !e.app.Local.Enabled.Empty() {
		t.Errorf("a plain import enables nothing: %+v", e.app.Local.Enabled)
	}
	store, _ := secrets.Load(e.p.SecretsFile())
	want := secrets.Store{"TAVILY_API_KEY": "tvly-1", "FIRECRAWL_API_KEY": "fc-1",
		"FIRECRAWL_API_TOKEN": "fc-arg", "HDR_AUTHORIZATION": "hdr-1"}
	if !reflect.DeepEqual(store, want) {
		t.Errorf("secrets: %v", store.Names())
	}
	raw := read(t, e.p.CatalogFile())
	for _, value := range []string{"tvly-1", "fc-1", "fc-arg", "hdr-1"} {
		if strings.Contains(raw, value) {
			t.Errorf("the catalog holds the secret %q", value)
		}
	}
	for _, placeholder := range []string{"tavilyApiKey=${TAVILY_API_KEY}&profile=x", "--api-token=${FIRECRAWL_API_TOKEN}", "Bearer ${HDR_AUTHORIZATION}"} {
		if !strings.Contains(raw, placeholder) {
			t.Errorf("the catalog lacks %q:\n%s", placeholder, raw)
		}
	}

	// Enabling meets the entry mcp-setup left: it differs, so it is a conflict.
	toggled, err := e.app.Toggle(e.app.Global(), true, "mcp:tavily")
	if err != nil || len(toggled.MCP) != 2 || read(t, claude) != before[claude] {
		t.Fatalf("a differing unmanaged entry is a conflict: %+v %v", toggled.MCP, err)
	}
	if !strings.Contains(read(t, opencode), "tavilyApiKey=tvly-1") {
		t.Errorf("an agent without such an entry gets it:\n%s", read(t, opencode))
	}

	again, err := e.app.ImportMCP(e.app.MCPSetupConfig(), false, false)
	if err != nil || len(again.Servers) != 0 {
		t.Errorf("a second import adds nothing: %+v %v", again, err)
	}
}

func TestImportMCPSetupAdopting(t *testing.T) {
	e := setup(t)
	e.app.Local.Agents = []string{"claude-code", "opencode"}
	e.app.Save()
	claude := filepath.Join(e.p.Home, ".claude.json")
	opencode := filepath.Join(e.p.Home, ".config", "opencode", "opencode.json")
	write(t, e.app.MCPSetupConfig(), `{"presets": {"ios-dev": ["simctl"]},
  "mcps": {"simctl": {"type": "local", "command": ["npx", "-y", "simctl-mcp"]},
           "tavily": {"type": "remote", "url": "https://mcp.tavily.com/mcp"}}}`)
	write(t, claude, `{"mcpServers": {"tavily": {"type": "http", "url": "https://old"}, "mine": {"url": "https://mine"}}}`)
	write(t, opencode, `{"mcp": {"simctl": {"type": "local", "command": ["npx", "-y", "simctl-mcp"], "enabled": false}}}`)

	dry, err := e.app.ImportMCP(e.app.MCPSetupConfig(), true, true)
	if err != nil || len(dry.Sync.MCP) != 2 {
		t.Fatalf("the dry run reports both removals: %+v %v", dry.Sync.MCP, err)
	}
	if _, err := os.Stat(e.p.SecretsFile()); !os.IsNotExist(err) || !strings.Contains(read(t, claude), "https://old") {
		t.Fatal("a dry run writes nothing")
	}

	fresh, _ := Open(e.p)
	if _, err := fresh.ImportMCP(fresh.MCPSetupConfig(), false, true); err != nil {
		t.Fatal(err)
	}
	if text := read(t, claude); strings.Contains(text, "tavily") || !strings.Contains(text, "https://mine") {
		t.Errorf("adopted entries that are not enabled go, the user's own stays:\n%s", text)
	}
	if strings.Contains(read(t, opencode), "simctl") {
		t.Errorf("opencode:\n%s", read(t, opencode))
	}
	if _, err := fresh.Toggle(fresh.Global(), true, "@ios-dev"); err != nil || !strings.Contains(read(t, claude), "simctl-mcp") {
		t.Errorf("enabling the pack writes its server: %v\n%s", err, read(t, claude))
	}
}
