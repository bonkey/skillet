package mcp

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bonkey/skillet/internal/catalog"
)

var (
	localDef = catalog.MCP{Type: "local", Command: []string{"npx", "-y", "simctl-mcp"},
		Environment: map[string]string{"KEY": "secret-value"}}
	remoteDef = catalog.MCP{Type: "remote", URL: "https://mcp.example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer abc"}}
	sseDef = catalog.MCP{Type: "remote", URL: "https://sse.example.com/sse", Transport: "sse",
		Timeout: 30, DisabledTools: []string{"danger"}}
)

func TestRenderShapes(t *testing.T) {
	tests := []struct {
		agent string
		def   catalog.MCP
		want  Entry
	}{
		{"claude-code", localDef, Entry{{"type", "stdio"}, {"command", "npx"}, {"args", []string{"-y", "simctl-mcp"}}, {"env", localDef.Environment}}},
		{"claude-code", remoteDef, Entry{{"type", "http"}, {"url", remoteDef.URL}, {"headers", remoteDef.Headers}}},
		{"claude-code", sseDef, Entry{{"type", "sse"}, {"url", sseDef.URL}, {"timeout", 30000.0}}},
		{"codex", sseDef, Entry{{"url", sseDef.URL}, {"tool_timeout_sec", 30.0}, {"disabled_tools", []string{"danger"}}}},
		{"opencode", sseDef, Entry{{"type", "remote"}, {"url", sseDef.URL}, {"timeout", 30000.0}, {"enabled", true}}},
		{"zed", sseDef, Entry{{"enabled", true}, {"url", sseDef.URL}, {"timeout", 30.0}}},
		{"cursor", localDef, Entry{{"command", "npx"}, {"args", []string{"-y", "simctl-mcp"}}, {"env", localDef.Environment}}},
		{"cursor", remoteDef, Entry{{"url", remoteDef.URL}, {"headers", remoteDef.Headers}}},
		{"gemini-cli", remoteDef, Entry{{"httpUrl", remoteDef.URL}, {"headers", remoteDef.Headers}}},
		{"gemini-cli", sseDef, Entry{{"url", sseDef.URL}, {"timeout", 30000.0}, {"excludeTools", []string{"danger"}}}},
		{"opencode", localDef, Entry{{"type", "local"}, {"command", localDef.Command}, {"environment", localDef.Environment}, {"enabled", true}}},
		{"opencode", remoteDef, Entry{{"type", "remote"}, {"url", remoteDef.URL}, {"headers", remoteDef.Headers}, {"enabled", true}}},
		{"codex", localDef, Entry{{"command", "npx"}, {"args", []string{"-y", "simctl-mcp"}}, {"env", localDef.Environment}}},
		{"codex", remoteDef, Entry{{"url", remoteDef.URL}, {"http_headers", remoteDef.Headers}}},
		{"crush", sseDef, Entry{{"type", "sse"}, {"url", sseDef.URL}, {"timeout", 30.0}, {"disabled_tools", []string{"danger"}}, {"disabled", false}}},
		{"zed", localDef, Entry{{"enabled", true}, {"command", "npx"}, {"args", []string{"-y", "simctl-mcp"}}, {"env", localDef.Environment}}},
		{"zed", remoteDef, Entry{{"enabled", true}, {"url", remoteDef.URL}, {"headers", remoteDef.Headers}}},
	}
	for _, tt := range tests {
		if got := Targets[tt.agent].Render(tt.def); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s %s:\n got %v\nwant %v", tt.agent, tt.def.Type, got, tt.want)
		}
	}
}

type home struct {
	dir   string
	state State
}

func (h home) file(rel string) string { return filepath.Join(h.dir, rel) }

func (h home) sync(t *testing.T, agents []string, desired map[string]catalog.MCP, opt Options) map[string]string {
	t.Helper()
	actions, err := Sync(h.dir, agents, desired, h.state, opt)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, a := range actions {
		rel, _ := filepath.Rel(h.dir, a.File)
		out[rel+"#"+a.Name] = a.Op
	}
	return out
}

func read(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSyncWritesEveryAgentAndRemovesOnlyItsOwn(t *testing.T) {
	h := home{t.TempDir(), State{}}
	claude := h.file(".claude.json")
	original := "{\n  \"numStartups\": 7,\n  \"mcpServers\": {\n    \"mine\": {\n      \"type\": \"http\",\n      \"url\": \"https://mine\"\n    }\n  }\n}\n"
	os.WriteFile(claude, []byte(original), 0o644)
	agents := []string{"claude-code", "codex", "pi", "zed"}
	desired := map[string]catalog.MCP{"simctl": localDef, "remote": remoteDef}

	got := h.sync(t, agents, desired, Options{})
	want := map[string]string{
		".claude.json#remote": OpAdd, ".claude.json#simctl": OpAdd,
		".codex/config.toml#remote": OpAdd, ".codex/config.toml#simctl": OpAdd,
		".config/zed/settings.json#remote": OpAdd, ".config/zed/settings.json#simctl": OpAdd,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("first sync: %v", got)
	}
	if text := read(t, h.file(".codex/config.toml")); !strings.Contains(text, "[mcp_servers.simctl.env]\nKEY = \"secret-value\"") {
		t.Errorf("codex config:\n%s", text)
	}
	if info, _ := os.Stat(h.file(".codex/config.toml")); info.Mode().Perm() != 0o600 {
		t.Errorf("a new config file is private: %v", info.Mode().Perm())
	}
	if info, _ := os.Stat(claude); info.Mode().Perm() != 0o644 {
		t.Errorf("an existing file keeps its mode: %v", info.Mode().Perm())
	}
	if got := h.sync(t, agents, desired, Options{}); len(got) != 0 {
		t.Fatalf("second sync must be a no-op: %v", got)
	}

	got = h.sync(t, agents, map[string]catalog.MCP{}, Options{Keep: []string{"remote"}})
	want = map[string]string{".claude.json#simctl": OpRemove, ".codex/config.toml#simctl": OpRemove, ".config/zed/settings.json#simctl": OpRemove}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kept servers stay, the rest goes: %v", got)
	}
	h.sync(t, agents, map[string]catalog.MCP{}, Options{})
	if text := read(t, claude); text != original {
		t.Errorf("the user's own entry and the rest of the file must survive:\n%s", text)
	}
	if len(h.state) != 0 {
		t.Errorf("state should be empty: %v", h.state)
	}
}

func TestSyncConflictsForceAndAdoption(t *testing.T) {
	h := home{t.TempDir(), State{}}
	cursor := h.file(".cursor/mcp.json")
	os.MkdirAll(filepath.Dir(cursor), 0o755)
	os.WriteFile(cursor, []byte(`{
  "mcpServers": {
    "remote": { "url": "https://mcp.example.com/mcp", "headers": { "Authorization": "Bearer abc" } },
    "simctl": { "command": "something-else" }
  }
}
`), 0o644)
	before := read(t, cursor)
	desired := map[string]catalog.MCP{"simctl": localDef, "remote": remoteDef}

	got := h.sync(t, []string{"cursor"}, desired, Options{})
	if !reflect.DeepEqual(got, map[string]string{".cursor/mcp.json#simctl": OpConflict}) {
		t.Fatalf("a different foreign entry is a conflict, an identical one is adopted silently: %v", got)
	}
	if read(t, cursor) != before {
		t.Error("a conflict must not change the file")
	}
	if !reflect.DeepEqual(h.state[cursor], []string{"remote"}) {
		t.Errorf("the identical entry is adopted: %v", h.state)
	}

	dry := h.sync(t, []string{"cursor"}, desired, Options{Force: true, DryRun: true})
	if dry[".cursor/mcp.json#simctl"] != OpUpdate || read(t, cursor) != before {
		t.Fatalf("dry run: %v", dry)
	}
	h.sync(t, []string{"cursor"}, desired, Options{Force: true})
	if !strings.Contains(read(t, cursor), "simctl-mcp") || !reflect.DeepEqual(h.state[cursor], []string{"remote", "simctl"}) {
		t.Errorf("force takes the entry over:\n%s\n%v", read(t, cursor), h.state)
	}

	changed := localDef
	changed.Command = []string{"npx", "-y", "simctl-mcp@2"}
	got = h.sync(t, []string{"cursor"}, map[string]catalog.MCP{"simctl": changed, "remote": remoteDef}, Options{})
	if !reflect.DeepEqual(got, map[string]string{".cursor/mcp.json#simctl": OpUpdate}) {
		t.Errorf("a managed entry follows its definition: %v", got)
	}
}

func TestStateFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "mcp-state.json")
	state, err := LoadState(file)
	if err != nil || len(state) != 0 {
		t.Fatalf("missing file: %v %v", state, err)
	}
	state["/x"] = []string{"a"}
	if err := state.Save(file); err != nil {
		t.Fatal(err)
	}
	again, err := LoadState(file)
	if err != nil || !reflect.DeepEqual(again, state) {
		t.Fatalf("round trip: %v %v", again, err)
	}
}
