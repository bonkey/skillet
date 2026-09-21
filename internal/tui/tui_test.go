package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bonkey/skillet/internal/app"
	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/paths"
)

func model(t *testing.T) *Model {
	t.Helper()
	root := t.TempDir()
	p := paths.Paths{
		Home:   root,
		Config: filepath.Join(root, ".config", "skillet"),
		Data:   filepath.Join(root, ".local", "share", "skillet"),
		Cwd:    filepath.Join(root, "project"),
	}
	os.MkdirAll(p.Cwd, 0o755)
	clone := p.RepoDir("acme/skills")
	for _, name := range []string{"alpha", "beta", "loose"} {
		os.MkdirAll(filepath.Join(clone, name), 0o755)
		os.WriteFile(filepath.Join(clone, name, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: The "+name+" skill\n---\n"), 0o644)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", clone}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	c := catalog.New()
	c.Sources["acme/skills"] = &catalog.Source{URL: "x", Skills: []string{"alpha", "beta", "loose"}}
	c.MCPs["simctl"] = &catalog.MCP{Type: "local", Command: []string{"npx", "-y", "simctl-mcp"}}
	c.Packs["acme"] = &catalog.Pack{Description: "Acme skills", Skills: []string{"alpha", "beta"}, MCPs: []string{"simctl"}}
	if err := c.Save(p.ConfigFile()); err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(a)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func press(m *Model, keys ...string) {
	for _, key := range keys {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		switch key {
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "left":
			msg = tea.KeyMsg{Type: tea.KeyLeft}
		case " ":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
		}
		m.Update(msg)
	}
}

func TestRowsGroupSkillsUnderPacks(t *testing.T) {
	m := model(t)
	want := []row{{pack: "acme"}, {pack: "acme", skill: "alpha"}, {pack: "acme", skill: "beta"},
		{pack: "acme", mcp: "simctl"}, {pack: app.NoPack}, {pack: app.NoPack, skill: "loose"}}
	if !reflect.DeepEqual(m.rows, want) {
		t.Fatalf("rows: %v", m.rows)
	}
}

func enabled(t *testing.T, a *app.App) []string {
	t.Helper()
	got, err := a.Enabled(a.Global())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestToggleSkillAndPack(t *testing.T) {
	m := model(t)
	press(m, "down", " ") // alpha
	if got := enabled(t, m.app); len(got) != 0 {
		t.Fatalf("a mark changes nothing yet: %v", got)
	}
	if view := m.View(); !strings.Contains(view, "[+] alpha") || !strings.Contains(view, "[~] @acme  1/3 once applied") || !strings.Contains(view, "1 marked") {
		t.Errorf("the mark should show:\n%s", view)
	}
	press(m, "a", "n")
	if got := enabled(t, m.app); len(got) != 0 || m.marked() != 1 {
		t.Fatalf("a declined confirmation keeps the marks and changes nothing: %v", got)
	}
	press(m, "a", "y")
	if got := enabled(t, m.app); !reflect.DeepEqual(got, []string{"alpha"}) || m.marked() != 0 {
		t.Fatalf("after applying alpha: %v", got)
	}
	if !strings.Contains(m.View(), "[~] @acme  1/3 enabled") {
		t.Errorf("pack header should show a partial state:\n%s", m.View())
	}

	press(m, " ", " ")
	if m.marked() != 0 {
		t.Errorf("marking a row twice takes the change back: %v", m.marks)
	}

	m.cursor = 0
	press(m, " ", "a", "y") // whole pack on
	if got := enabled(t, m.app); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("after enabling the pack: %v", got)
	}
	reopened, _ := app.Open(m.app.Paths)
	if got := enabled(t, reopened); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Errorf("the links hold the state: %v", got)
	}
	press(m, " ", "a", "y") // whole pack off
	if got := enabled(t, m.app); len(got) != 0 {
		t.Fatalf("after disabling the pack: %v", got)
	}
}

func TestScopeSwitchTogglesInTheProject(t *testing.T) {
	m := model(t)
	press(m, "down", "down", " ", "tab", "k", " ", "a", "y") // beta globally, alpha in the project
	if got := enabled(t, m.app); !m.scope.Project || !reflect.DeepEqual(got, []string{"beta"}) {
		t.Fatalf("one confirmation applies the marks of both scopes: %+v %v", m.scope, got)
	}
	if _, err := os.Readlink(filepath.Join(m.app.Paths.Cwd, ".claude", "skills", "alpha")); err != nil {
		t.Fatalf("project link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.app.Paths.Cwd, paths.ManifestName)); !os.IsNotExist(err) {
		t.Error("toggling must not write a manifest")
	}
	press(m, "tab")
	if m.scope.Project {
		t.Error("tab should switch back to the global scope")
	}
}

func TestFilterAndFold(t *testing.T) {
	m := model(t)
	press(m, "/", "b", "e", "t", "enter")
	if want := []row{{pack: "acme"}, {pack: "acme", skill: "beta"}}; !reflect.DeepEqual(m.rows, want) {
		t.Fatalf("filtered rows: %v", m.rows)
	}
	press(m, "esc")
	if len(m.rows) != 6 {
		t.Fatalf("esc should clear the filter: %v", m.rows)
	}
	press(m, "down", "left")
	if len(m.rows) != 3 || m.cursor != 0 {
		t.Fatalf("folded rows: %v cursor %d", m.rows, m.cursor)
	}
}

func TestViewFitsTheTerminal(t *testing.T) {
	m := model(t)
	m.view.Skills["alpha"] = app.SkillView{Name: "alpha", Description: strings.Repeat("long description ", 200)}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	press(m, "down")
	lines := strings.Split(m.View(), "\n")
	if len(lines) > 12 {
		t.Errorf("view has %d lines for a terminal of 12", len(lines))
	}
	if !strings.Contains(lines[0], "skillet") {
		t.Errorf("header missing: %q", lines[0])
	}
}

func TestToggleServer(t *testing.T) {
	m := model(t)
	m.cursor = 3 // mcp:simctl
	press(m, " ", "a", "y")
	if !m.view.MCPs["simctl"].Global {
		t.Fatalf("after toggling the server: %+v", m.view.MCPs["simctl"])
	}
	if text := string(must(os.ReadFile(filepath.Join(m.app.Paths.Home, ".claude.json")))); !strings.Contains(text, "simctl-mcp") {
		t.Errorf("the server is written into the agent config:\n%s", text)
	}
	if !strings.Contains(m.View(), "[x] mcp:simctl") {
		t.Errorf("view:\n%s", m.View())
	}

	press(m, "tab", " ")
	if !strings.Contains(m.status, "global") || !m.view.MCPs["simctl"].Global {
		t.Errorf("a server cannot be toggled in the project scope: %q", m.status)
	}
}

func must(data []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return data
}
