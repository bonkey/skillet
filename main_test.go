package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/bonkey/skillet/internal/app"
)

// output returns what fn prints. Printed to a pipe, the cells are letters and
// carry no colors.
func output(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = stdout }()
	fn()
	w.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPrintTablesGroupsRowsByPack(t *testing.T) {
	agents := []string{"claude-code", "codex"}
	skills := []app.Row{
		{Name: "alpha", Label: "acme/alpha", Packs: []string{"core"}, Agents: map[string]string{"claude-code": app.StateOn, "codex": app.StateOn}},
		{Name: "beta", Label: "acme/beta", Agents: map[string]string{"claude-code": app.StateDrift, "codex": app.StateAbsent}},
	}
	servers := []app.Row{
		{Name: "exa", Packs: []string{"core"}, Agents: map[string]string{"claude-code": app.StateExtra, "codex": app.StateOn}},
	}
	got := output(t, func() {
		printTables([]table{
			{"global skills", agents, skills, markOfState, []string{"ios"}},
			{"global mcp", agents, servers, markOfState, nil},
		}, statusNames)
	})
	want := strings.Join([]string{
		"global skills  claude  codex",
		"@core",
		"  acme/alpha     ok     ok  ",
		"@ios  off",
		"no pack",
		"  acme/beta      x       -  ",
		"",
		"global mcp     claude  codex",
		"@core",
		"  exa            e      ok  ",
		"- absent   ok on   x not on disk; sync enables   e off in config; sync --clean disables",
		"",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestPrintTablesListsOffPacksAndUnmanagedEntries(t *testing.T) {
	agents := []string{"claude-code", "codex"}
	skills := []app.Row{
		{Name: "beta", Label: "acme/beta", Packs: []string{"ios"}, Agents: map[string]string{"claude-code": app.StateExtra, "codex": app.StateExtra}},
		{Name: "gamma", Label: "acme/gamma", Packs: []string{"ios"}, Agents: map[string]string{"claude-code": app.StateOff, "codex": app.StateOff}},
		{Name: "hand", Label: "hand", Unmanaged: true, Agents: map[string]string{"claude-code": app.StateUnmanaged, "codex": app.StateAbsent}},
	}
	got := output(t, func() {
		printTables([]table{{"global skills", agents, skills, markOfState, []string{"ios"}}}, statusNames)
	})
	want := strings.Join([]string{
		"global skills  claude  codex",
		"@ios  off",
		"  acme/beta      e       e  ",
		"  acme/gamma     -       -  ",
		"unmanaged",
		"  hand           u       -  ",
		"- absent   e off in config; sync --clean disables   u not managed by skillet",
		"",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
