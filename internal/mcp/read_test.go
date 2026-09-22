package mcp

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bonkey/skillet/internal/catalog"
)

func TestReadTurnsAgentEntriesIntoDefinitions(t *testing.T) {
	home := t.TempDir()
	write := func(rel, text string) {
		t.Helper()
		path := filepath.Join(home, rel)
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(text), 0o644)
	}
	write(".claude.json", `{
  "theme": "dark",
  "mcpServers": {
    "simctl": { "type": "stdio", "command": "npx", "args": ["-y", "simctl-mcp"], "env": { "KEY": "v" }, "timeout": 5000 },
    "tavily": { "type": "http", "url": "https://mcp.tavily.com/mcp", "headers": { "Authorization": "Bearer x" } },
    "events": { "type": "sse", "url": "https://sse.example.com/mcp" },
    "odd": { "comment": "nothing usable" }
  }
}`)
	write(".codex/config.toml", `model = "x"

[mcp_servers.simctl]
command = "npx"
args = ["-y", "simctl-mcp"]
tool_timeout_sec = 5

[mcp_servers.simctl.env]
KEY = "v"

[mcp_servers.remote]
url = "https://mcp.example.com/mcp"
disabled_tools = ["dangerous"]

[mcp_servers.remote.http_headers]
X-Api-Key = "k"
`)
	write(".gemini/settings.json", `{
  "mcpServers": {
    "local": { "command": "uvx", "args": ["mcp-server-fetch"], "excludeTools": ["fetch_raw"] },
    "http": { "httpUrl": "https://mcp.example.com/mcp", "headers": { "Authorization": "Bearer y" }, "timeout": 2000 },
    "sse": { "url": "https://sse.example.com/mcp" }
  }
}`)

	claude, invalid, err := Read(home, "claude-code")
	if err != nil || !reflect.DeepEqual(invalid, []string{"odd"}) {
		t.Fatalf("claude: %v %v", invalid, err)
	}
	want := map[string]catalog.MCP{
		"simctl": {Type: "local", Command: []string{"npx", "-y", "simctl-mcp"}, Environment: map[string]string{"KEY": "v"}, Timeout: 5},
		"tavily": {Type: "remote", URL: "https://mcp.tavily.com/mcp", Headers: map[string]string{"Authorization": "Bearer x"}},
		"events": {Type: "remote", URL: "https://sse.example.com/mcp", Transport: "sse"},
	}
	if !reflect.DeepEqual(claude, want) {
		t.Errorf("claude:\n got %+v\nwant %+v", claude, want)
	}

	codex, invalid, err := Read(home, "codex")
	if err != nil || len(invalid) != 0 {
		t.Fatalf("codex: %v %v", invalid, err)
	}
	want = map[string]catalog.MCP{
		"simctl": {Type: "local", Command: []string{"npx", "-y", "simctl-mcp"}, Environment: map[string]string{"KEY": "v"}, Timeout: 5},
		"remote": {Type: "remote", URL: "https://mcp.example.com/mcp", Headers: map[string]string{"X-Api-Key": "k"}, DisabledTools: []string{"dangerous"}},
	}
	if !reflect.DeepEqual(codex, want) {
		t.Errorf("codex:\n got %+v\nwant %+v", codex, want)
	}

	gemini, _, err := Read(home, "gemini-cli")
	if err != nil {
		t.Fatal(err)
	}
	want = map[string]catalog.MCP{
		"local": {Type: "local", Command: []string{"uvx", "mcp-server-fetch"}, DisabledTools: []string{"fetch_raw"}},
		"http":  {Type: "remote", URL: "https://mcp.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer y"}, Timeout: 2},
		"sse":   {Type: "remote", URL: "https://sse.example.com/mcp", Transport: "sse"},
	}
	if !reflect.DeepEqual(gemini, want) {
		t.Errorf("gemini:\n got %+v\nwant %+v", gemini, want)
	}

	if defs, _, err := Read(home, "cursor"); err != nil || defs != nil {
		t.Errorf("an agent without a parser reads nothing: %v %v", defs, err)
	}
	if defs, _, err := Read(t.TempDir(), "codex"); err != nil || len(defs) != 0 {
		t.Errorf("a missing file holds nothing: %v %v", defs, err)
	}
}
