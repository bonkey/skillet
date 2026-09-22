package mcp

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"

	"github.com/bonkey/skillet/internal/catalog"
)

const (
	OpAdd    = "mcp-add"
	OpUpdate = "mcp-update" // also when the entry was there before skillet managed it
	OpRemove = "mcp-remove"
	OpKeep   = "mcp-keep"   // the entry is already right
	OpDelete = "mcp-delete" // an unmanaged entry that is not desired is deleted (purge)
)

type Action struct {
	Op   string `json:"op"`
	File string `json:"file"`
	Name string `json:"name"`
}

func (a Action) String() string {
	return fmt.Sprintf("%-12s %s in %s", a.Op, a.Name, a.File)
}

// Record remembers one managed entry by two hashes: of the server's
// definition with its ${NAME} placeholders unexpanded, and of the entry as it
// was written. While both still match, the entry is up to date, and a sync
// needs no secret to know that.
type Record struct {
	Def   string `json:"def"`
	Entry string `json:"entry"`
}

// State records, per config file, the entries skillet manages: those it
// wrote or took over. Only those are ever removed.
type State map[string]map[string]Record

func LoadState(file string) (State, error) {
	state := State{}
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(data, &state) == nil {
		return state, nil
	}
	// A state file that lists plain names per config file.
	var names map[string][]string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	state = State{}
	for config, list := range names {
		state[config] = map[string]Record{}
		for _, name := range list {
			state[config][name] = Record{}
		}
	}
	return state, nil
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
	DryRun bool
	// Purge deletes the entries skillet does not manage, unless desired.
	Purge bool
}

// Expand fills in the secrets of a definition and lists the placeholders
// that have no value.
type Expand func(def catalog.MCP) (catalog.MCP, []string)

// shapes changes whenever a Render function does, so that entries written
// in an older shape are rewritten.
const shapes = "1"

func hashOf(parts ...any) string {
	data, _ := json.Marshal(parts)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// document is a config file opened for editing server entries.
type document interface {
	names() ([]string, error)
	same(name string, entry Entry) (bool, error)
	set(name string, entry Entry) error
	remove(name string) error
	// hash identifies the content of a present entry.
	hash(name string) string
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

func (d jsonDocument) hash(name string) string {
	value, _, _ := d.get(d.node, name)
	return hashOf(value)
}

type tomlDocument struct{ *tomlFile }

func (d tomlDocument) names() ([]string, error)           { return d.tomlFile.names(), nil }
func (d tomlDocument) set(name string, entry Entry) error { d.tomlFile.set(name, entry); return nil }
func (d tomlDocument) remove(name string) error           { d.tomlFile.remove(name); return nil }
func (d tomlDocument) same(name string, entry Entry) (bool, error) {
	text, ok := d.get(name)
	return ok && text == d.render(name, entry), nil
}

func (d tomlDocument) hash(name string) string {
	text, _ := d.get(name)
	return hashOf(text)
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
// servers among the entries skillet manages. An entry named like a desired
// server is that server: it is overwritten when it differs, and managed from
// then on. Agents without a target are skipped.
//
// desired holds definitions with their placeholders unexpanded. expand is
// called only for a server whose entry may need writing; a server with
// missing secrets is left as it is and returned with the missing names.
func Sync(home string, agents []string, desired map[string]catalog.MCP, expand Expand, state State, opt Options) (actions, kept []Action, missing map[string][]string, err error) {
	missing = map[string][]string{}
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
			return actions, kept, missing, err
		}
		present, err := doc.names()
		if err != nil {
			return actions, kept, missing, fmt.Errorf("%s: %w", file, err)
		}

		records := map[string]Record{}
		for name, record := range state[file] {
			records[name] = record
		}
		var planned []Action
		for _, name := range sortedNames(state[file]) {
			if _, wanted := desired[name]; wanted {
				continue
			}
			delete(records, name)
			if slices.Contains(present, name) {
				planned = append(planned, Action{OpRemove, file, name})
				if err := doc.remove(name); err != nil {
					return actions, kept, missing, err
				}
			}
		}
		if opt.Purge {
			for _, name := range slices.Sorted(slices.Values(present)) {
				_, wanted := desired[name]
				_, managed := state[file][name]
				if wanted || managed {
					continue
				}
				planned = append(planned, Action{OpDelete, file, name})
				if err := doc.remove(name); err != nil {
					return actions, kept, missing, err
				}
			}
		}
		for _, name := range names {
			def := desired[name]
			defHash, isPresent := hashOf(shapes, agent, def), slices.Contains(present, name)
			if record, managed := records[name]; managed && isPresent && record.Def == defHash && record.Entry == doc.hash(name) {
				kept = append(kept, Action{OpKeep, file, name})
				continue
			}
			expanded, lacking := expand(def)
			if len(lacking) > 0 {
				missing[name] = lacking
				continue
			}
			entry := target.Render(expanded)
			same, err := doc.same(name, entry)
			if err != nil {
				return actions, kept, missing, fmt.Errorf("%s: %w", file, err)
			}
			if same {
				kept = append(kept, Action{OpKeep, file, name})
			} else {
				op := OpAdd
				if isPresent {
					op = OpUpdate
				}
				planned = append(planned, Action{op, file, name})
				if err := doc.set(name, entry); err != nil {
					return actions, kept, missing, err
				}
			}
			records[name] = Record{Def: defHash, Entry: doc.hash(name)}
		}
		actions = append(actions, planned...)
		if opt.DryRun {
			continue
		}
		if len(planned) > 0 {
			if err := writeFile(file, doc.bytes()); err != nil {
				return actions, kept, missing, err
			}
		}
		if len(records) == 0 {
			delete(state, file)
		} else {
			state[file] = records
		}
	}
	return actions, kept, missing, nil
}

func sortedNames(records map[string]Record) []string {
	names := make([]string, 0, len(records))
	for name := range records {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

// Enabled lists the servers skillet manages in the config of any of agents.
// The managed entries are the enabled state of servers.
func Enabled(home string, agents []string, state State) []string {
	var names []string
	for _, agent := range agents {
		if target, ok := Targets[agent]; ok {
			for name := range state[filepath.Join(home, filepath.FromSlash(target.File))] {
				if !slices.Contains(names, name) {
					names = append(names, name)
				}
			}
		}
	}
	sort.Strings(names)
	return names
}
