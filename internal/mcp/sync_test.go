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

// asIs stands for definitions without placeholders.
func asIs(def catalog.MCP) (catalog.MCP, []string) { return def, nil }

func (h home) file(rel string) string { return filepath.Join(h.dir, rel) }

func (h home) sync(t *testing.T, agents []string, desired map[string]catalog.MCP, opt Options) map[string]string {
	t.Helper()
	actions, _, _, err := Sync(h.dir, agents, desired, asIs, h.state, opt)
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
	if actions, kept, _, err := Sync(h.dir, agents, desired, asIs, h.state, Options{}); err != nil || len(actions) != 0 || len(kept) != 6 || kept[0].Op != OpKeep {
		t.Fatalf("second sync must be a no-op that reports every entry as kept: %v %v %v", actions, kept, err)
	}

	got = h.sync(t, agents, map[string]catalog.MCP{"remote": remoteDef}, Options{})
	want = map[string]string{".claude.json#simctl": OpRemove, ".codex/config.toml#simctl": OpRemove, ".config/zed/settings.json#simctl": OpRemove}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("a server that is no longer desired goes: %v", got)
	}
	h.sync(t, agents, map[string]catalog.MCP{}, Options{})
	if text := read(t, claude); text != original {
		t.Errorf("the user's own entry and the rest of the file must survive:\n%s", text)
	}
	if len(h.state) != 0 {
		t.Errorf("state should be empty: %v", h.state)
	}
}

func TestPurgeDeletesEntriesSkilletDoesNotManage(t *testing.T) {
	h := home{t.TempDir(), State{}}
	cursor := h.file(".cursor/mcp.json")
	os.MkdirAll(filepath.Dir(cursor), 0o755)
	os.WriteFile(cursor, []byte(`{
  "theme": "dark",
  "mcpServers": {
    "simctl": { "command": "something-else" },
    "mine": { "command": "untouched" }
  }
}
`), 0o644)
	desired := map[string]catalog.MCP{"simctl": localDef}
	got := h.sync(t, []string{"cursor"}, desired, Options{Purge: true})
	want := map[string]string{".cursor/mcp.json#simctl": OpUpdate, ".cursor/mcp.json#mine": OpDelete}
	if text := read(t, cursor); !reflect.DeepEqual(got, want) || strings.Contains(text, "untouched") || !strings.Contains(text, "\"theme\"") {
		t.Fatalf("purge deletes the user's entry and keeps the rest of the file: %v\n%s", got, text)
	}
	if got := h.sync(t, []string{"cursor"}, desired, Options{Purge: true}); len(got) != 0 {
		t.Errorf("a second purge has nothing to do: %v", got)
	}
}

func TestSyncTakesOverEntriesOfTheSameName(t *testing.T) {
	h := home{t.TempDir(), State{}}
	cursor := h.file(".cursor/mcp.json")
	os.MkdirAll(filepath.Dir(cursor), 0o755)
	os.WriteFile(cursor, []byte(`{
  "mcpServers": {
    "remote": { "url": "https://mcp.example.com/mcp", "headers": { "Authorization": "Bearer abc" } },
    "simctl": { "command": "something-else" },
    "mine": { "command": "untouched" }
  }
}
`), 0o644)
	before := read(t, cursor)
	desired := map[string]catalog.MCP{"simctl": localDef, "remote": remoteDef}

	dry := h.sync(t, []string{"cursor"}, desired, Options{DryRun: true})
	if !reflect.DeepEqual(dry, map[string]string{".cursor/mcp.json#simctl": OpUpdate}) || read(t, cursor) != before {
		t.Fatalf("dry run: an identical entry needs nothing, a differing one is updated: %v", dry)
	}
	h.sync(t, []string{"cursor"}, desired, Options{})
	if text := read(t, cursor); !strings.Contains(text, "simctl-mcp") || strings.Contains(text, "something-else") || !strings.Contains(text, "untouched") {
		t.Errorf("the entry of the same name is overwritten, other names stay:\n%s", text)
	}
	if !reflect.DeepEqual(sortedNames(h.state[cursor]), []string{"remote", "simctl"}) {
		t.Errorf("both are managed now: %v", h.state)
	}

	got := h.sync(t, []string{"cursor"}, map[string]catalog.MCP{}, Options{})
	want := map[string]string{".cursor/mcp.json#remote": OpRemove, ".cursor/mcp.json#simctl": OpRemove}
	if !reflect.DeepEqual(got, want) || !strings.Contains(read(t, cursor), "untouched") {
		t.Errorf("managed entries go when disabled, the user's own stays: %v", got)
	}
}

func TestStateFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "mcp-state.json")
	state, err := LoadState(file)
	if err != nil || len(state) != 0 {
		t.Fatalf("missing file: %v %v", state, err)
	}
	state["/x"] = map[string]Record{"a": {Def: "d", Entry: "e"}}
	if err := state.Save(file); err != nil {
		t.Fatal(err)
	}
	again, err := LoadState(file)
	if err != nil || !reflect.DeepEqual(again, state) {
		t.Fatalf("round trip: %v %v", again, err)
	}

	os.WriteFile(file, []byte(`{"/x": ["a", "b"]}`), 0o644)
	listed, err := LoadState(file)
	if err != nil || !reflect.DeepEqual(sortedNames(listed["/x"]), []string{"a", "b"}) {
		t.Fatalf("a state file that lists names is still read: %v %v", listed, err)
	}
}

func TestSyncAsksForSecretsOnlyWhenAnEntryMayChange(t *testing.T) {
	h := home{t.TempDir(), State{}}
	def := catalog.MCP{Type: "remote", URL: "https://x/mcp?key=${KEY}"}
	plain := catalog.MCP{Type: "local", Command: []string{"npx", "plain"}}
	calls, known := 0, true
	expand := func(d catalog.MCP) (catalog.MCP, []string) {
		calls++
		if !strings.Contains(d.URL, "${KEY}") {
			return d, nil
		}
		if !known {
			return d, []string{"KEY"}
		}
		d.URL = strings.ReplaceAll(d.URL, "${KEY}", "value-1")
		return d, nil
	}
	sync := func(desired map[string]catalog.MCP) ([]Action, map[string][]string) {
		t.Helper()
		actions, _, missing, err := Sync(h.dir, []string{"cursor"}, desired, expand, h.state, Options{})
		if err != nil {
			t.Fatal(err)
		}
		return actions, missing
	}
	cursor := h.file(".cursor/mcp.json")
	desired := map[string]catalog.MCP{"secretive": def, "plain": plain}

	if actions, _ := sync(desired); len(actions) != 2 || calls != 2 {
		t.Fatalf("first sync writes both: %v, %d calls", actions, calls)
	}
	calls = 0
	if actions, _ := sync(desired); len(actions) != 0 || calls != 0 {
		t.Fatalf("an unchanged sync needs no secret: %v, %d calls", actions, calls)
	}

	// A changed definition, or an entry edited behind skillet's back, is looked at again.
	changed := def
	changed.URL = "https://y/mcp?key=${KEY}"
	if actions, _ := sync(map[string]catalog.MCP{"secretive": changed, "plain": plain}); len(actions) != 1 || calls != 1 {
		t.Fatalf("a changed definition: %v, %d calls", actions, calls)
	}
	text := read(t, cursor)
	os.WriteFile(cursor, []byte(strings.Replace(text, "value-1", "tampered", 1)), 0o644)
	calls = 0
	if actions, _ := sync(map[string]catalog.MCP{"secretive": changed, "plain": plain}); len(actions) != 1 || calls != 1 || !strings.Contains(read(t, cursor), "value-1") {
		t.Fatalf("a tampered entry is repaired: %v, %d calls", actions, calls)
	}

	// Without the secret the entry stays as it is and is reported.
	known = false
	again := changed
	again.URL = "https://z/mcp?key=${KEY}"
	before := read(t, cursor)
	actions, missing := sync(map[string]catalog.MCP{"secretive": again, "plain": plain})
	if len(actions) != 0 || !reflect.DeepEqual(missing, map[string][]string{"secretive": {"KEY"}}) || read(t, cursor) != before {
		t.Fatalf("missing secret: %v %v", actions, missing)
	}
	if _, managed := h.state[cursor]["secretive"]; !managed {
		t.Error("the entry stays managed while its secret is missing")
	}
}
