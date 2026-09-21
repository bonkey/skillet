// Package link makes the symlinks in skill directories match a desired set
// of skills.
//
// Links have two levels, like the `skills` CLI lays them out: the canonical
// directory <base>/.agents/skills links into the clones, and every other
// agent directory links to the canonical entry.
package link

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Agent names the skill directories of one agent, relative to the home
// directory (Global) and to a project root (Project).
type Agent struct{ Global, Project string }

const canonical = ".agents/skills"

var Agents = map[string]Agent{
	"claude-code":    {".claude/skills", ".claude/skills"},
	"codex":          {".codex/skills", canonical},
	"cursor":         {".cursor/skills", canonical},
	"gemini-cli":     {".gemini/skills", canonical},
	"github-copilot": {".copilot/skills", canonical},
	"opencode":       {".config/opencode/skills", canonical},
	// pi reads ~/.agents/skills and ./.agents/skills itself, and warns about
	// a skill it finds twice, so it gets no directory of its own.
	"pi": {canonical, canonical},
	// crush and zed take MCP servers only; they have no skills directory.
	"crush": {},
	"zed":   {},
}

// Canonical is the directory below base whose links point into the clones.
func Canonical(base string) string { return filepath.Join(base, filepath.FromSlash(canonical)) }

// Dirs lists the agent directories below base that link to the canonical
// directory. base is the home directory, or a project root when project is
// set. Agents that read the canonical directory itself add nothing.
func Dirs(agents []string, base string, project bool) ([]string, error) {
	var dirs []string
	seen := map[string]bool{Canonical(base): true}
	for _, name := range agents {
		agent, ok := Agents[name]
		if !ok {
			return nil, fmt.Errorf("unknown agent %q", name)
		}
		rel := agent.Global
		if project {
			rel = agent.Project
		}
		if rel == "" {
			continue
		}
		if dir := filepath.Join(base, filepath.FromSlash(rel)); !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}

const (
	OpLink     = "link"
	OpUnlink   = "unlink"
	OpRelink   = "relink"
	OpReplace  = "replace"  // an unmanaged entry is deleted and a link takes its place
	OpConflict = "conflict" // something skillet does not own is in the way
)

type Action struct {
	Op     string `json:"op"`
	Dir    string `json:"dir"`
	Name   string `json:"name"`
	Target string `json:"target,omitempty"`
}

func (a Action) String() string {
	path := filepath.Join(a.Dir, a.Name)
	switch a.Op {
	case OpConflict:
		return fmt.Sprintf("conflict %s exists and is not managed by skillet; --force replaces it", path)
	case OpUnlink:
		return fmt.Sprintf("unlink   %s", path)
	default:
		return fmt.Sprintf("%-8s %s -> %s", a.Op, path, a.Target)
	}
}

type Options struct {
	// ReposDir marks ownership: a canonical entry is managed when it is a
	// symlink pointing into ReposDir.
	ReposDir string
	// Replace allows deleting an unmanaged entry, in the canonical or an
	// agent directory, that stands where the named skill goes.
	Replace func(name string) bool
	DryRun  bool
}

// Sync reconciles the canonical directory with desired (skill name to
// absolute skill folder) and the agent directories with the canonical one.
// It returns what it did, or would do in a dry run.
func Sync(canonicalDir string, agentDirs []string, desired map[string]string, opt Options) ([]Action, error) {
	actions, linked, foreign, err := planCanonical(canonicalDir, desired, opt)
	if err != nil {
		return nil, err
	}
	for _, dir := range agentDirs {
		if sameDir(dir, canonicalDir) {
			continue
		}
		planned, err := planAgentDir(dir, canonicalDir, linked, foreign, opt)
		if err != nil {
			return nil, err
		}
		actions = append(actions, planned...)
	}
	if opt.DryRun {
		return actions, nil
	}
	for _, a := range actions {
		path := filepath.Join(a.Dir, a.Name)
		switch a.Op {
		case OpUnlink, OpRelink:
			err = os.Remove(path)
		case OpReplace:
			err = os.RemoveAll(path)
		}
		if err != nil {
			return actions, err
		}
		if a.Op == OpLink || a.Op == OpRelink || a.Op == OpReplace {
			if err := os.MkdirAll(a.Dir, 0o755); err != nil {
				return actions, err
			}
			if err := os.Symlink(a.Target, path); err != nil {
				return actions, err
			}
		}
	}
	return actions, nil
}

// planCanonical also reports which names end up as managed links (linked)
// and which unmanaged entries stay (foreign).
func planCanonical(dir string, desired map[string]string, opt Options) (actions []Action, linked, foreign map[string]bool, err error) {
	linked, foreign = map[string]bool{}, map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		target, isLink := readLink(dir, name)
		switch _, wanted := desired[name]; {
		case isLink && within(target, opt.ReposDir):
			if !wanted {
				actions = append(actions, Action{Op: OpUnlink, Dir: dir, Name: name})
			}
		case !wanted || opt.Replace == nil || !opt.Replace(name):
			foreign[name] = true
		}
	}
	for _, name := range sortedKeys(desired) {
		action := Action{Dir: dir, Name: name, Target: desired[name]}
		target, isLink := readLink(dir, name)
		_, statErr := os.Lstat(filepath.Join(dir, name))
		switch {
		case os.IsNotExist(statErr):
			action.Op = OpLink
		case isLink && target == desired[name]:
			linked[name] = true
			continue
		case isLink && within(target, opt.ReposDir):
			action.Op = OpRelink
		case foreign[name]:
			action.Op = OpConflict
		default:
			action.Op = OpReplace
		}
		linked[name] = action.Op != OpConflict
		actions = append(actions, action)
	}
	return actions, linked, foreign, nil
}

// planAgentDir manages the links of one agent directory. A link belongs to
// skillet when it points into the clones, or at a canonical entry that
// is managed or missing.
func planAgentDir(dir, canonicalDir string, linked, foreign map[string]bool, opt Options) ([]Action, error) {
	var actions []Action
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	owned := func(target string) bool {
		if within(target, opt.ReposDir) {
			return true
		}
		return within(target, canonicalDir) && !foreign[filepath.Base(target)]
	}
	for _, entry := range entries {
		target, isLink := readLink(dir, entry.Name())
		if isLink && owned(target) && !linked[entry.Name()] {
			actions = append(actions, Action{Op: OpUnlink, Dir: dir, Name: entry.Name()})
		}
	}
	for _, name := range sortedKeys(linked) {
		if !linked[name] {
			continue
		}
		want := filepath.Join(canonicalDir, name)
		rel, err := filepath.Rel(dir, want)
		if err != nil {
			rel = want
		}
		action := Action{Dir: dir, Name: name, Target: rel}
		target, isLink := readLink(dir, name)
		_, statErr := os.Lstat(filepath.Join(dir, name))
		switch {
		case os.IsNotExist(statErr):
			action.Op = OpLink
		case isLink && target == want:
			continue
		case isLink && owned(target):
			action.Op = OpRelink
		case opt.Replace != nil && opt.Replace(name):
			action.Op = OpReplace
		default:
			action.Op = OpConflict
		}
		actions = append(actions, action)
	}
	return actions, nil
}

// sameDir reports whether two paths lead to one directory, as when a project
// symlinks .claude/skills to .agents/skills.
func sameDir(a, b string) bool {
	realA, errA := filepath.EvalSymlinks(a)
	realB, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && realA == realB
}

// readLink returns the absolute target of dir/name when it is a symlink.
func readLink(dir, name string) (string, bool) {
	target, err := os.Readlink(filepath.Join(dir, name))
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	return filepath.Clean(target), true
}

func within(path, root string) bool {
	return root != "" && strings.HasPrefix(path, filepath.Clean(root)+string(filepath.Separator))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
