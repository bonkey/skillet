// Package paths locates the files skillet reads and writes.
package paths

import (
	"os"
	"path/filepath"
)

const ManifestName = ".skillet.yaml"

type Paths struct {
	Home   string
	Config string // directory holding catalog.yaml
	Data   string // directory holding repos/ and index.json
	Cwd    string
}

func Default() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Paths{}, err
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	return Paths{
		Home:   home,
		Config: filepath.Join(config, "skillet"),
		Data:   filepath.Join(data, "skillet"),
		Cwd:    cwd,
	}, nil
}

func (p Paths) CatalogFile() string { return filepath.Join(p.Config, "catalog.yaml") }
func (p Paths) ReposDir() string    { return filepath.Join(p.Data, "repos") }
func (p Paths) IndexFile() string   { return filepath.Join(p.Data, "index.json") }

// RepoDir is the clone of a source named "owner/repo".
func (p Paths) RepoDir(source string) string {
	return filepath.Join(p.ReposDir(), filepath.FromSlash(source))
}

// ProjectRoot is the nearest ancestor of Cwd holding a manifest. Home and
// everything above it never count as a project.
func (p Paths) ProjectRoot() (string, bool) {
	dir := p.Cwd
	for {
		if dir == p.Home {
			return "", false
		}
		if _, err := os.Stat(filepath.Join(dir, ManifestName)); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
