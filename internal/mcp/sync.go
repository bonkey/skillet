package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"

	"github.com/bonkey/skillet/internal/catalog"
)

const (
	OpAdd      = "mcp-add"
	OpUpdate   = "mcp-update"
	OpRemove   = "mcp-remove"
	OpConflict = "mcp-conflict" // an entry of that name exists and skillet did not write it
)

type Action struct {
	Op   string `json:"op"`
	File string `json:"file"`
	Name string `json:"name"`
}

func (a Action) String() string {
	if a.Op == OpConflict {
		return fmt.Sprintf("%-12s %s in %s exists and is not managed by skillet; --force replaces it", a.Op, a.Name, a.File)
	}
	return fmt.Sprintf("%-12s %s in %s", a.Op, a.Name, a.File)
}

// State records, per config file, the entries skillet wrote. Only those are
// ever changed or removed.
type State map[string][]string

func LoadState(file string) (State, error) {
	state := State{}
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	return state, json.Unmarshal(data, &state)
}

func (s State) Save(file string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, append(data, '\n'), 0o644)
}

type Options struct {
	// Keep names servers to leave exactly as they are, present or not.
	Keep []string
	// Force takes over entries skillet did not write.
	Force  bool
	DryRun bool
}

// document is a config file opened for editing server entries.
type document interface {
	names() ([]string, error)
	same(name string, entry Entry) (bool, error)
	set(name string, entry Entry) error
	remove(name string) error
	// enabled reports whether a present entry is switched on.
	enabled(name string) bool
	bytes() []byte
}

type jsonDocument struct {
	*jsonFile
	node []string
}

func (d jsonDocument) names() ([]string, error)           { return d.jsonFile.names(d.node) }
func (d jsonDocument) set(name string, entry Entry) error { return d.jsonFile.set(d.node, name, entry) }
func (d jsonDocument) remove(name string) error           { return d.jsonFile.remove(d.node, name) }
func (d jsonDocument) same(name string, entry Entry) (bool, error) {
	value, ok, err := d.get(d.node, name)
	return ok && reflect.DeepEqual(value, entry.plain()), err
}

func (d jsonDocument) enabled(name string) bool {
	value, _, _ := d.get(d.node, name)
	entry, _ := value.(map[string]any)
	return entry["enabled"] != false && entry["disabled"] != true
}

type tomlDocument struct{ *tomlFile }

var tomlDisabled = regexp.MustCompile(`(?m)^\s*enabled\s*=\s*false\b`)

func (d tomlDocument) enabled(name string) bool {
	text, _ := d.get(name)
	return !tomlDisabled.MatchString(text)
}

func (d tomlDocument) names() ([]string, error)           { return d.tomlFile.names(), nil }
func (d tomlDocument) set(name string, entry Entry) error { d.tomlFile.set(name, entry); return nil }
func (d tomlDocument) remove(name string) error           { d.tomlFile.remove(name); return nil }
func (d tomlDocument) same(name string, entry Entry) (bool, error) {
	text, ok := d.get(name)
	return ok && text == d.render(name, entry), nil
}

func open(target Target, data []byte) (document, error) {
	if target.TOML {
		f, err := parseTOMLFile(data, target.Node[0])
		return tomlDocument{f}, err
	}
	f, err := parseJSONFile(data)
	return jsonDocument{f, target.Node}, err
}

// Sync makes the agents' config files below home hold exactly the desired
// servers among the entries skillet manages. Agents without a target are
// skipped. An entry that already has the desired content is adopted.
func Sync(home string, agents []string, desired map[string]catalog.MCP, state State, opt Options) ([]Action, error) {
	var actions []Action
	names := make([]string, 0, len(desired))
	for name := range desired {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, agent := range agents {
		target, ok := Targets[agent]
		if !ok {
			continue
		}
		file, doc, err := load(home, target)
		if err != nil {
			return actions, err
		}
		present, err := doc.names()
		if err != nil {
			return actions, fmt.Errorf("%s: %w", file, err)
		}

		owned := slices.Clone(state[file])
		var planned []Action
		for _, name := range state[file] {
			_, wanted := desired[name]
			if wanted || slices.Contains(opt.Keep, name) {
				continue
			}
			owned = slices.DeleteFunc(owned, func(n string) bool { return n == name })
			if slices.Contains(present, name) {
				planned = append(planned, Action{OpRemove, file, name})
				if err := doc.remove(name); err != nil {
					return actions, err
				}
			}
		}
		for _, name := range names {
			if slices.Contains(opt.Keep, name) {
				continue
			}
			entry := target.Render(desired[name])
			same, err := doc.same(name, entry)
			if err != nil {
				return actions, fmt.Errorf("%s: %w", file, err)
			}
			isOwned := slices.Contains(owned, name)
			switch {
			case same:
			case !slices.Contains(present, name):
				planned = append(planned, Action{OpAdd, file, name})
			case isOwned || opt.Force:
				planned = append(planned, Action{OpUpdate, file, name})
			default:
				planned = append(planned, Action{OpConflict, file, name})
				continue
			}
			if !same {
				if err := doc.set(name, entry); err != nil {
					return actions, err
				}
			}
			if !isOwned {
				owned = append(owned, name)
			}
		}
		actions = append(actions, planned...)
		if opt.DryRun {
			continue
		}
		if changed := slices.ContainsFunc(planned, func(a Action) bool { return a.Op != OpConflict }); changed {
			if err := writeFile(file, doc.bytes()); err != nil {
				return actions, err
			}
		}
		sort.Strings(owned)
		if len(owned) == 0 {
			delete(state, file)
		} else {
			state[file] = owned
		}
	}
	return actions, nil
}

// writeFile replaces a file atomically. It keeps the mode of an existing
// file; a new one is private, because entries may hold secrets.
func writeFile(file string, data []byte) error {
	mode := fs.FileMode(0o600)
	if real, err := filepath.EvalSymlinks(file); err == nil {
		file = real // write through a symlinked config
	}
	if info, err := os.Stat(file); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".skillet.tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// load opens an agent's config file; a missing file is an empty document.
func load(home string, target Target) (string, document, error) {
	file := filepath.Join(home, filepath.FromSlash(target.File))
	data, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return file, nil, err
	}
	doc, err := open(target, data)
	if err != nil {
		return file, nil, fmt.Errorf("%s: %w", file, err)
	}
	return file, doc, nil
}

// Present lists the servers in an agent's config and whether each is
// switched on there. An agent without a target has none.
func Present(home, agent string) (map[string]bool, error) {
	target, ok := Targets[agent]
	if !ok {
		return nil, nil
	}
	_, doc, err := load(home, target)
	if err != nil {
		return nil, err
	}
	names, err := doc.names()
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	for _, name := range names {
		present[name] = doc.enabled(name)
	}
	return present, nil
}

// Adopt marks servers that are present in the agents' configs as written by
// skillet, so that a sync may change and remove them.
func Adopt(home string, agents, names []string, state State) error {
	for _, agent := range agents {
		target, ok := Targets[agent]
		if !ok {
			continue
		}
		present, err := Present(home, agent)
		if err != nil {
			return err
		}
		file := filepath.Join(home, filepath.FromSlash(target.File))
		for _, name := range names {
			if _, ok := present[name]; ok && !slices.Contains(state[file], name) {
				state[file] = append(state[file], name)
			}
		}
		sort.Strings(state[file])
	}
	return nil
}

// Managed lists the agents whose config holds entries that skillet wrote.
func Managed(home string, state State) []string {
	var agents []string
	for agent, target := range Targets {
		if len(state[filepath.Join(home, filepath.FromSlash(target.File))]) > 0 {
			agents = append(agents, agent)
		}
	}
	sort.Strings(agents)
	return agents
}
