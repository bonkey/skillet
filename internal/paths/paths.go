// Package paths locates the files skillet reads and writes.
package paths

import (
	"os"
	"path/filepath"
)

const ManifestName = ".skillet.toml"

type Paths struct {
	Home   string
	Config string // directory holding config.toml and secrets.toml
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

// ConfigFile holds the catalog, the 1Password items, the agents and the
// global enabled set.
func (p Paths) ConfigFile() string { return filepath.Join(p.Config, "config.toml") }
func (p Paths) ReposDir() string   { return filepath.Join(p.Data, "repos") }
func (p Paths) IndexFile() string  { return filepath.Join(p.Data, "index.json") }

// SecretsFile holds the values of ${NAME} placeholders. It is never pushed.
func (p Paths) SecretsFile() string { return filepath.Join(p.Config, "secrets.toml") }

// MCPStateFile records the MCP entries skillet wrote into agent configs.
func (p Paths) MCPStateFile() string { return filepath.Join(p.Data, "mcp-state.json") }

// SessionsDir holds the global part of running `skillet run` sessions.
func (p Paths) SessionsDir() string { return filepath.Join(p.Data, "sessions") }

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
