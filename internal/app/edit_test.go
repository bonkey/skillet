package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bonkey/skillet/internal/paths"
)

// editor is a script that appends its arguments, as a comment, to the file
// it is given last.
func editor(t *testing.T) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "editor")
	write(t, script, "#!/bin/sh\nfor file; do :; done\necho \"# $*\" >> \"$file\"\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestEditRunsTheEditorOnTheScopesFile(t *testing.T) {
	e := setup(t)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", editor(t)+" --wait")
	scope, err := Edit(e.p, false)
	if err != nil || scope != e.app.Global() {
		t.Fatalf("scope: %+v, %v", scope, err)
	}
	if got := read(t, e.p.ConfigFile()); got != "# --wait "+e.p.ConfigFile()+"\n" {
		t.Fatalf("the editor gets its arguments and config.toml:\n%s", got)
	}

	t.Setenv("VISUAL", editor(t))
	t.Setenv("EDITOR", "false")
	scope, err = Edit(e.p, true)
	manifest := filepath.Join(e.p.Cwd, paths.ManifestName)
	if err != nil || !scope.Project || scope.Root != e.p.Cwd {
		t.Fatalf("scope: %+v, %v", scope, err)
	}
	if got := read(t, manifest); got != "# "+manifest+"\n" {
		t.Fatalf("-p edits the project's manifest with $VISUAL:\n%s", got)
	}
}

func TestEditReportsAMissingOrFailingEditor(t *testing.T) {
	e := setup(t)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if _, err := Edit(e.p, false); err == nil || !strings.Contains(err.Error(), "$EDITOR") {
		t.Fatalf("no editor: %v", err)
	}
	t.Setenv("EDITOR", "false")
	if _, err := Edit(e.p, false); err == nil || !strings.Contains(err.Error(), "exited with code 1") {
		t.Fatalf("a failing editor: %v", err)
	}
	t.Setenv("EDITOR", editor(t))
	home := e.p
	home.Cwd = home.Home
	if _, err := Edit(home, true); err == nil {
		t.Fatal("the home directory is no project")
	}
}
