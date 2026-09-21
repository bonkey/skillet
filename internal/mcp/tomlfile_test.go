package mcp

import (
	"reflect"
	"testing"
)

const codexConfig = `# Codex settings
model = "gpt-5"

[mcp_servers.keep]
command = "npx"
args = ["-y", "keep-mcp"] # a comment

[mcp_servers.keep.env]
TOKEN = "abc"

[profiles.fast]
model = "mini"
`

var tomlEntry = Entry{
	{"command", "uvx"}, {"args", []string{"serve", `say "hi"`}},
	{"env", map[string]string{"B": "2", "A": "1"}}, {"startup_timeout_sec", 20.0},
}

func editTOML(t *testing.T, src string, fn func(f *tomlFile)) string {
	t.Helper()
	f, err := parseTOMLFile([]byte(src), "mcp_servers")
	if err != nil {
		t.Fatal(err)
	}
	fn(f)
	return string(f.bytes())
}

func TestTOMLAppendAndRemoveRestoresTheFile(t *testing.T) {
	added := editTOML(t, codexConfig, func(f *tomlFile) { f.set("new-one", tomlEntry) })
	want := codexConfig + `
[mcp_servers.new-one]
command = "uvx"
args = ["serve", "say \"hi\""]
startup_timeout_sec = 20

[mcp_servers.new-one.env]
A = "1"
B = "2"
`
	if added != want {
		t.Fatalf("got:\n%s\nwant:\n%s", added, want)
	}
	removed := editTOML(t, added, func(f *tomlFile) { f.remove("new-one") })
	if removed != codexConfig {
		t.Errorf("remove must restore the file:\n%s", removed)
	}
}

func TestTOMLReplaceInPlace(t *testing.T) {
	got := editTOML(t, codexConfig, func(f *tomlFile) {
		if names := f.names(); !reflect.DeepEqual(names, []string{"keep"}) {
			t.Errorf("names: %v", names)
		}
		f.set("keep", Entry{{"url", "https://x"}})
	})
	want := `# Codex settings
model = "gpt-5"

[mcp_servers.keep]
url = "https://x"

[profiles.fast]
model = "mini"
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTOMLGetSetIsStable(t *testing.T) {
	once := editTOML(t, codexConfig, func(f *tomlFile) { f.set("x", tomlEntry) })
	f, _ := parseTOMLFile([]byte(once), "mcp_servers")
	text, ok := f.get("x")
	if !ok || text != f.render("x", tomlEntry) {
		t.Errorf("an entry reads back as it was rendered:\n%s", text)
	}
	twice := editTOML(t, once, func(f *tomlFile) { f.set("x", tomlEntry) })
	if twice != once {
		t.Errorf("setting the same entry again must not change the file:\n%s", twice)
	}
}

func TestTOMLQuotedNamesAndEmptyFile(t *testing.T) {
	got := editTOML(t, "", func(f *tomlFile) { f.set("my.server", Entry{{"command", "x"}}) })
	if got != "[mcp_servers.\"my.server\"]\ncommand = \"x\"\n" {
		t.Errorf("got %q", got)
	}
	f, _ := parseTOMLFile([]byte(got), "mcp_servers")
	if names := f.names(); !reflect.DeepEqual(names, []string{"my.server"}) {
		t.Errorf("names: %v", names)
	}
	f.remove("my.server")
	if len(f.bytes()) != 0 {
		t.Errorf("left: %q", f.bytes())
	}
}

func TestTOMLRejectsInlineDefinitions(t *testing.T) {
	for _, src := range []string{"mcp_servers = { a = { command = \"x\" } }\n", "mcp_servers.a.command = \"x\"\n"} {
		if _, err := parseTOMLFile([]byte(src), "mcp_servers"); err == nil {
			t.Errorf("%q should be rejected", src)
		}
	}
}
