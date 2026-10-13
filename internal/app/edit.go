package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bonkey/skillet/internal/paths"
	"github.com/bonkey/skillet/internal/session"
)

// Edit runs $VISUAL, or else $EDITOR, on the config file of the global scope
// or of the project around the working directory, and returns that scope.
// It reads no catalog, so a file that no longer parses can be fixed with it.
// The editor's value may hold arguments, as in "code --wait".
func Edit(p paths.Paths, project bool) (Scope, error) {
	a := &App{Paths: p}
	scope := a.Global()
	if project {
		var err error
		if scope, err = a.ProjectScope(); err != nil {
			return scope, err
		}
	}
	file := a.configOf(scope)
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		return scope, fmt.Errorf("set $EDITOR to the editor for %s", file)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return scope, err
	}
	code, err := session.Run([]string{"sh", "-c", editor + ` "$@"`, editor, file})
	if err == nil && code != 0 {
		err = fmt.Errorf("%s exited with code %d", editor, code)
	}
	return scope, err
}
