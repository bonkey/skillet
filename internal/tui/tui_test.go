package tui

import (
	"os"
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
	c := catalog.New()
	c.Sources["acme/skills"] = &catalog.Source{URL: "x", Skills: []string{"alpha", "beta", "loose"}}
	c.Packs["acme"] = &catalog.Pack{Description: "Acme skills", Skills: []string{"alpha", "beta"}}
	if err := c.Save(p.CatalogFile()); err != nil {
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
	want := []row{{"acme", ""}, {"acme", "alpha"}, {"acme", "beta"}, {app.NoPack, ""}, {app.NoPack, "loose"}}
	if !reflect.DeepEqual(m.rows, want) {
		t.Fatalf("rows: %v", m.rows)
	}
}

func TestToggleSkillAndPack(t *testing.T) {
	m := model(t)
	press(m, "down", " ") // alpha
	if got := m.app.Catalog.Enabled; !reflect.DeepEqual(got.Skills, []string{"alpha"}) {
		t.Fatalf("after toggling alpha: %+v", got)
	}
	if !strings.Contains(m.View(), "[-] @acme  1/2 enabled") {
		t.Errorf("pack header should show a partial state:\n%s", m.View())
	}

	m.cursor = 0
	press(m, " ") // whole pack on
	if got := m.app.Catalog.Resolve(m.app.Catalog.Enabled); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("after enabling the pack: %v", got)
	}
	press(m, " ") // whole pack off
	if got := m.app.Catalog.Resolve(m.app.Catalog.Enabled); len(got) != 0 {
		t.Fatalf("after disabling the pack: %v", got)
	}

	reopened, _ := app.Open(m.app.Paths)
	if !reopened.Catalog.Enabled.Empty() {
		t.Errorf("toggles must be saved: %+v", reopened.Catalog.Enabled)
	}
}

func TestScopeSwitchWritesProjectManifest(t *testing.T) {
	m := model(t)
	press(m, "tab", "down", " ")
	if !m.scope.Project || !m.app.Catalog.Enabled.Empty() {
		t.Fatalf("scope %+v, global set %+v", m.scope, m.app.Catalog.Enabled)
	}
	set, err := catalog.LoadSet(filepath.Join(m.app.Paths.Cwd, paths.ManifestName))
	if err != nil || !reflect.DeepEqual(set.Skills, []string{"alpha"}) {
		t.Fatalf("manifest: %+v %v", set, err)
	}
	press(m, "tab")
	if m.scope.Project {
		t.Error("tab should switch back to the global scope")
	}
}

func TestFilterAndFold(t *testing.T) {
	m := model(t)
	press(m, "/", "b", "e", "t", "enter")
	if want := []row{{"acme", ""}, {"acme", "beta"}}; !reflect.DeepEqual(m.rows, want) {
		t.Fatalf("filtered rows: %v", m.rows)
	}
	press(m, "esc")
	if len(m.rows) != 5 {
		t.Fatalf("esc should clear the filter: %v", m.rows)
	}
	press(m, "down", "left")
	if len(m.rows) != 3 || m.cursor != 0 {
		t.Fatalf("folded rows: %v cursor %d", m.rows, m.cursor)
	}
}

func TestEditPackDescription(t *testing.T) {
	m := model(t)
	press(m, "e")
	m.input.SetValue("Better words")
	press(m, "enter")
	reopened, _ := app.Open(m.app.Paths)
	if got := reopened.Catalog.Packs["acme"].Description; got != "Better words" {
		t.Errorf("description: %q", got)
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
