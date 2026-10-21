package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/bonkey/skillet/internal/app"
	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/paths"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// output returns what fn prints. Printed to a pipe, the cells are letters.
// lipgloss picks its colors from the terminal of the process, not from
// os.Stdout, so output also turns colors off while fn runs.
func output(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, profile := os.Stdout, lipgloss.ColorProfile()
	os.Stdout = w
	lipgloss.SetColorProfile(termenv.Ascii)
	defer func() {
		os.Stdout = stdout
		lipgloss.SetColorProfile(profile)
	}()
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
		"no pack",
		"  acme/beta      x       -  ",
		"@ios  off",
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

func TestPrintTablesListsPacksThatAreOffLast(t *testing.T) {
	agents := []string{"claude-code"}
	on := map[string]string{"claude-code": app.StateOn}
	off := map[string]string{"claude-code": app.StateOff}
	skills := []app.Row{
		{Name: "zeta", Label: "acme/zeta", Packs: []string{"web"}, Agents: on},
		{Name: "gamma", Label: "acme/gamma", Packs: []string{"ios"}, Agents: off},
		{Name: "alpha", Label: "acme/alpha", Packs: []string{"core"}, Agents: on},
		{Name: "beta", Label: "acme/beta", Agents: on},
		{Name: "hand", Label: "hand", Unmanaged: true, Agents: map[string]string{"claude-code": app.StateUnmanaged}},
	}
	got := output(t, func() {
		printTables([]table{{"global skills", agents, skills, markOfState, []string{"ios", "android"}}}, statusNames)
	})
	want := strings.Join([]string{
		"global skills  claude",
		"@core",
		"  acme/alpha     ok  ",
		"@web",
		"  acme/zeta      ok  ",
		"no pack",
		"  acme/beta      ok  ",
		"unmanaged",
		"  hand           u   ",
		"@android  off",
		"@ios  off",
		"  acme/gamma     -   ",
		"- absent   ok on   u not managed by skillet",
		"",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestPrintStatusListsSkillsAndServersOfAPackInOneTable(t *testing.T) {
	a := &app.App{Paths: paths.Paths{Home: "/h"}, Catalog: &catalog.Catalog{Agents: []string{"claude-code", "pi", "crush"}}}
	status := app.Status{
		Report: app.SyncReport{Scope: a.Global()},
		Skills: []app.Row{
			{Name: "alpha", Label: "acme/alpha", Packs: []string{"core"}, Agents: map[string]string{"claude-code": app.StateOn, "pi": app.StateOn}},
			{Name: "beta", Label: "acme/beta", Agents: map[string]string{"claude-code": app.StateOn, "pi": app.StateDrift}},
		},
		MCPs: []app.Row{
			{Name: "exa", Packs: []string{"core"}, Agents: map[string]string{"claude-code": app.StateOn, "crush": app.StateOn}},
			{Name: "shadcn", Packs: []string{"web"}, Agents: map[string]string{"claude-code": app.StateOff, "crush": app.StateOff}},
		},
		OffPacks: []string{"web"},
	}
	got := output(t, func() { printStatus(a, status) })
	want := strings.Join([]string{
		"global        claude  pi  crush",
		"@core",
		"  acme/alpha    ok    ok   na  ",
		"  mcp:exa       ok    na   ok  ",
		"no pack",
		"  acme/beta     ok    x    na  ",
		"@web  off",
		"  mcp:shadcn    -     na    -  ",
		"- absent   ok on   x not on disk; sync enables   na not supported by the agent",
		"",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestPrintTablesNamesSkillsInFoldersOfAnotherName(t *testing.T) {
	agents := []string{"claude-code", "codex"}
	on := map[string]string{"claude-code": app.StateOn, "codex": app.StateOn}
	skills := []app.Row{
		{Name: "alpha", Label: "acme/alpha", Agents: on},
		{Name: "apple-hig", Label: "hig", Agents: on},
	}
	got := output(t, func() {
		printTables([]table{{"global skills", agents, skills, markOfState, nil}}, statusNames)
	})
	want := strings.Join([]string{
		"global skills      claude  codex",
		"no pack",
		"  acme/alpha         ok     ok  ",
		"  hig (apple-hig)    ok     ok  ",
		"ok on",
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
		"unmanaged",
		"  hand           u       -  ",
		"@ios  off",
		"  acme/beta      e       e  ",
		"  acme/gamma     -       -  ",
		"- absent   e off in config; sync --clean disables   u not managed by skillet",
		"",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
