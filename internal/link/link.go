// Package link makes the symlinks in skill directories match a desired set
// of skills.
//
// Every agent directory links straight into the clones with absolute paths.
// The links are the enabled state: a skill is enabled where a link to it
// exists.
package link

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Agent names the skill directories of one agent, relative to the home
// directory (Global) and to a project root (Project).
type Agent struct{ Global, Project string }

// canonical is the directory that several agents read.
const canonical = ".agents/skills"

var Agents = map[string]Agent{
	"claude-code":    {".claude/skills", ".claude/skills"},
	"codex":          {".codex/skills", canonical},
	"cursor":         {".cursor/skills", canonical},
	"gemini-cli":     {".gemini/skills", canonical},
	"github-copilot": {".copilot/skills", canonical},
	"opencode":       {".config/opencode/skills", canonical},
	// pi reads ~/.agents/skills and ./.agents/skills, and warns about a skill
	// it finds twice.
	"pi": {canonical, canonical},
	// crush and zed take MCP servers only; they have no skills directory.
	"crush": {},
	"zed":   {},
}

// Dirs lists the skill directories of the agents below base. base is the
// home directory, or a project root when project is set. Agents that share a
// directory add it once.
func Dirs(agents []string, base string, project bool) ([]string, error) {
	var dirs []string
	seen := map[string]bool{}
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
	// ReposDir marks ownership: a link is managed when it points into
	// ReposDir, directly or through another link.
	ReposDir string
	// Keep names managed links that stay as they are although they are not
	// desired.
	Keep map[string]bool
	// Replace allows deleting an unmanaged entry that stands where the named
	// skill goes.
	Replace func(name string) bool
	DryRun  bool
}

// Linked lists the names that have a managed link in any of dirs.
func Linked(dirs []string, reposDir string) []string {
	names := map[string]bool{}
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if owned(dir, entry.Name(), dirs, reposDir) {
				names[entry.Name()] = true
			}
		}
	}
	return sortedKeys(names)
}

// Sync makes the managed links in dirs match desired (skill name to absolute
// skill folder). It returns what it did, or would do in a dry run.
func Sync(dirs []string, desired map[string]string, opt Options) ([]Action, error) {
	var actions []Action
	var done []string
	for _, dir := range dirs {
		if slices.ContainsFunc(done, func(other string) bool { return sameDir(dir, other) }) {
			continue
		}
		done = append(done, dir)
		planned, err := plan(dir, dirs, desired, opt)
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
		var err error
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

func plan(dir string, dirs []string, desired map[string]string, opt Options) ([]Action, error) {
	var actions []Action
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if _, wanted := desired[name]; !wanted && !opt.Keep[name] && owned(dir, name, dirs, opt.ReposDir) {
			actions = append(actions, Action{Op: OpUnlink, Dir: dir, Name: name})
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
			continue
		case owned(dir, name, dirs, opt.ReposDir):
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

// owned reports whether dir/name is a link that belongs to skillet: it
// points into the clones, at another link that does, or at a missing entry
// of one of dirs.
func owned(dir, name string, dirs []string, reposDir string) bool {
	target, isLink := readLink(dir, name)
	if !isLink {
		return false
	}
	if within(target, reposDir) {
		return true
	}
	if next, isLink := readLink(filepath.Dir(target), filepath.Base(target)); isLink {
		return within(next, reposDir)
	}
	_, err := os.Lstat(target)
	return os.IsNotExist(err) && slices.Contains(dirs, filepath.Dir(target))
}

// sameDir reports whether two paths lead to one directory, as when
// .claude/skills is a symlink to .agents/skills.
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
