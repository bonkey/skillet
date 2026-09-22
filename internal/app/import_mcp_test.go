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

func TestUpperSnake(t *testing.T) {
	for in, want := range map[string]string{"tavilyApiKey": "TAVILY_API_KEY", "api-key": "API_KEY", "tavily Authorization": "TAVILY_AUTHORIZATION",
		"X-Api-Key": "X_API_KEY", "FIRECRAWL_API_KEY": "FIRECRAWL_API_KEY", "mobile-mcp token": "MOBILE_MCP_TOKEN"} {
		if got := upperSnake(in); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestHideSecrets(t *testing.T) {
	store, added := secrets.Store{"KEY": "other"}, map[string]bool{}
	def := catalog.MCP{Command: []string{"x"}, Environment: map[string]string{"KEY": "v1", "HOME": "/h", "PLACED": "${DONE}"},
		Headers: map[string]string{"Authorization": "Bearer t", "Accept": "json"},
		URL:     "https://mcp.tavily.com/mcp?tavilyApiKey=abc%21&plain=1"}
	got := hideSecrets("tavily", def, store, added)
	want := catalog.MCP{Command: []string{"x"}, Environment: map[string]string{"KEY": "${TAVILY_KEY}", "HOME": "/h", "PLACED": "${DONE}"},
		Headers: map[string]string{"Authorization": "${TAVILY_AUTHORIZATION}", "Accept": "json"},
		URL:     "https://mcp.tavily.com/mcp?tavilyApiKey=${TAVILY_API_KEY}&plain=1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if !reflect.DeepEqual(store, secrets.Store{"KEY": "other", "TAVILY_KEY": "v1", "TAVILY_AUTHORIZATION": "Bearer t", "TAVILY_API_KEY": "abc!"}) {
		t.Errorf("store: %v", store)
	}
	if !reflect.DeepEqual(sortedKeys(added), []string{"TAVILY_API_KEY", "TAVILY_AUTHORIZATION", "TAVILY_KEY"}) {
		t.Errorf("added: %v", added)
	}
}

func TestImportTakesServersFromTheAgents(t *testing.T) {
	e := setup(t)
	e.add(t, true)
	e.app.Local.Agents = []string{"claude-code", "codex"}
	e.app.Save()
	claude, codex := filepath.Join(e.p.Home, ".claude.json"), filepath.Join(e.p.Home, ".codex", "config.toml")
	write(t, claude, `{
  "mcpServers": {
    "mobile-mcp": { "type": "stdio", "command": "npx", "args": ["-y", "@mobilenext/mobile-mcp@latest"] },
    "search": { "type": "http", "url": "https://mcp.tavily.com/mcp?tavilyApiKey=secret-1" },
    "odd": { "note": "no command, no url" }
  }
}`)
	write(t, codex, "[mcp_servers.mobile-mcp]\ncommand = \"npx\"\nargs = [\"-y\", \"@mobilenext/mobile-mcp@latest\"]\n\n[mcp_servers.morph]\ncommand = \"npx\"\nargs = [\"morph-mcp\"]\n\n[mcp_servers.morph.env]\nMORPH_API_KEY = \"secret-2\"\n")
	write(t, e.app.LegacyLock(), `{"version":3,"skills":{}}`)

	dry, err := e.app.Import(e.app.LegacyLock(), true)
	if err != nil || !reflect.DeepEqual(dry.Servers, []string{"mobile-mcp", "search", "morph"}) || !reflect.DeepEqual(dry.Secrets, []string{"MORPH_API_KEY", "TAVILY_API_KEY"}) {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	if dry.Skipped["mcp:odd"] == "" || len(dry.Sync.MissingSecrets) != 0 {
		t.Errorf("an unusable entry is skipped and found secrets are not missing: %+v", dry)
	}
	if _, err := os.Stat(e.p.SecretsFile()); !os.IsNotExist(err) {
		t.Fatal("a dry run writes no secrets")
	}

	fresh, _ := Open(e.p)
	report, err := fresh.Import(fresh.LegacyLock(), false)
	if err != nil || !reflect.DeepEqual(report.Servers, []string{"mobile-mcp", "search", "morph"}) {
		t.Fatalf("import: %+v %v", report, err)
	}
	config := read(t, e.p.ConfigFile())
	for _, want := range []string{"command = ['npx', '-y', '@mobilenext/mobile-mcp@latest']", "name = 'search'\nurl = 'https://mcp.tavily.com/mcp?tavilyApiKey=${TAVILY_API_KEY}'", "environment = {MORPH_API_KEY = '${MORPH_API_KEY}'}"} {
		if !strings.Contains(config, want) {
			t.Errorf("config lacks %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, "secret-") {
		t.Fatalf("no secret value may reach the catalog:\n%s", config)
	}
	store, _ := secrets.Load(e.p.SecretsFile())
	if !reflect.DeepEqual(store, secrets.Store{"TAVILY_API_KEY": "secret-1", "MORPH_API_KEY": "secret-2"}) {
		t.Errorf("secrets: %v", store)
	}
	if text := read(t, claude); !strings.Contains(text, "secret-1") || !strings.Contains(text, "morph") {
		t.Errorf("the sync after the import writes every server to every agent, with the values filled in:\n%s", text)
	}
	if text := read(t, codex); !strings.Contains(text, "tavilyApiKey=secret-1") {
		t.Errorf("codex gets the search server too:\n%s", text)
	}

	again, err := fresh.Import(fresh.LegacyLock(), false)
	if err != nil || len(again.Servers) != 0 || len(again.Secrets) != 0 {
		t.Fatalf("a second import finds only managed entries: %+v %v", again, err)
	}
}
