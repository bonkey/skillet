package app

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/link"
	"github.com/bonkey/skillet/internal/mcp"
	"github.com/bonkey/skillet/internal/paths"
)

// The states of a skill or server for one agent in Status.
const (
	StateOn        = "on"        // on in the config and on the disk
	StateDrift     = "drift"     // on in the config, absent from the disk
	StateExtra     = "extra"     // on the disk, off in the config
	StateRepair    = "repair"    // on, but the link or entry needs rewriting
	StateConflict  = "conflict"  // something skillet does not own is in the way
	StateOff       = "off"       // in a pack that is off, and absent from the disk
	StateUnmanaged = "unmanaged" // on the disk, not managed by skillet
	StateAbsent    = "absent"    // the agent does not have it
)

// Row is one skill or server of a scope. For a report, Agents maps each
// agent to the op of the action on its directory or config, empty where
// there is none; in a Status it maps each agent to a state.
type Row struct {
	Name string `json:"name"`
	// Label is a skill's folder inside the clones.
	Label  string            `json:"label,omitempty"`
	Packs  []string          `json:"packs"`
	Agents map[string]string `json:"agents"`
	// Unmanaged marks an entry that skillet does not manage, which a sync
	// with Purge deletes.
	Unmanaged bool `json:"unmanaged,omitempty"`
}

// Columns lists the agents that have a skills directory in a scope, and
// those that have an MCP config there, with that directory or file.
func (a *App) Columns(scope Scope) (dirs, files [][2]string) {
	for _, name := range a.Catalog.Agents {
		if agent, ok := link.Agents[name]; ok {
			rel := agent.Global
			if scope.Project {
				rel = agent.Project
			}
			if rel != "" {
				dirs = append(dirs, [2]string{name, filepath.Join(scope.Root, filepath.FromSlash(rel))})
			}
		}
		if target, ok := mcp.Targets[name]; ok && !scope.Project {
			files = append(files, [2]string{name, filepath.Join(a.Paths.Home, filepath.FromSlash(target.File))})
		}
	}
	return dirs, files
}

// Rows turns a report into one row per skill and per server, sorted by
// label, each with the op of every agent that has the directory or config.
// An entry that skillet does not manage is a row of its own, named by its
// folder or entry, after the rest of its kind.
func (a *App) Rows(report SyncReport) (skills, servers []Row) {
	dirs, files := a.Columns(report.Scope)
	repos := a.Paths.ReposDir() + string(filepath.Separator)
	byLabel := map[string]*Row{}
	var order []string
	// A key starts with "s" for a skill and "m" for a server; "!" marks
	// an unmanaged entry and sorts it after the rest.
	row := func(key, name, label string, packs []string, columns [][2]string) *Row {
		if r, ok := byLabel[key]; ok {
			return r
		}
		r := &Row{Name: name, Label: label, Packs: packs, Agents: map[string]string{}, Unmanaged: key[1] == '!'}
		for _, col := range columns {
			r.Agents[col[0]] = ""
		}
		byLabel[key] = r
		order = append(order, key)
		return r
	}
	for _, action := range slices.Concat(report.Actions, report.Kept) {
		var r *Row
		if action.Op == link.OpDelete {
			r = row("s!"+action.Name, action.Name, action.Name, []string{}, dirs)
		} else {
			label := strings.TrimPrefix(action.Target, repos)
			if action.Target == "" {
				label = a.labelOf(action.Name)
			}
			r = row("s "+label, action.Name, label, a.packsOf(action.Name, false), dirs)
		}
		for _, col := range dirs {
			if col[1] == action.Dir && rank(action.Op) > rank(r.Agents[col[0]]) {
				r.Agents[col[0]] = action.Op
			}
		}
	}
	for _, action := range slices.Concat(report.MCP, report.KeptMCP) {
		var r *Row
		if action.Op == mcp.OpDelete {
			r = row("m!"+action.Name, action.Name, "", []string{}, files)
		} else {
			r = row("m "+action.Name, action.Name, "", a.packsOf(action.Name, true), files)
		}
		for _, col := range files {
			if col[1] == action.File && rank(action.Op) > rank(r.Agents[col[0]]) {
				r.Agents[col[0]] = action.Op
			}
		}
	}
	slices.Sort(order)
	for _, key := range order {
		if key[0] == 's' {
			skills = append(skills, *byLabel[key])
		} else {
			servers = append(servers, *byLabel[key])
		}
	}
	return skills, servers
}

// labelOf names a catalog skill by its folder inside the clones, or by its
// name when no clone holds it.
func (a *App) labelOf(name string) string {
	if found, ok := a.Index.Lookup(a.Catalog, name); ok {
		return strings.TrimPrefix(found.Dir, a.Paths.ReposDir()+string(filepath.Separator))
	}
	return name
}

// rank orders ops so that the most telling one of a cell wins.
func rank(op string) int {
	switch op {
	case "":
		return 0
	case link.OpKeep, mcp.OpKeep:
		return 1
	case link.OpRelink, mcp.OpUpdate:
		return 2
	case link.OpLink, link.OpReplace, mcp.OpAdd:
		return 3
	case link.OpUnlink, link.OpDelete, mcp.OpRemove, mcp.OpDelete:
		return 4
	}
	return 5 // conflict
}

// packsOf lists the packs that hold a skill or server, sorted.
func (a *App) packsOf(name string, server bool) []string {
	packs := []string{}
	for _, pack := range a.Catalog.PackNames() {
		set := catalog.Set{Packs: []string{pack}}
		if server && slices.Contains(a.Catalog.ResolveMCPs(set), name) ||
			!server && slices.Contains(a.Catalog.Resolve(set), name) {
			packs = append(packs, pack)
		}
	}
	return packs
}

// Status is the state of a scope's skills and servers per agent.
type Status struct {
	Scope  string `json:"scope"`
	Skills []Row  `json:"skills"`
	MCPs   []Row  `json:"mcps"`
	// OffPacks lists the packs switched off in the scope's config file.
	OffPacks []string `json:"off_packs"`
	// Report is the dry run the status comes from.
	Report SyncReport `json:"-"`
}

// Status reads what a sync of the scope would do, without changing
// anything, and turns it into a state per skill or server and agent. Every
// member of a pack that is off has a row, also when it is not on the disk.
// With unmanaged, the entries that skillet does not manage have rows too.
func (a *App) Status(scope Scope, unmanaged bool) (Status, error) {
	report, err := a.Sync(scope, SyncOptions{DryRun: true, Purge: unmanaged})
	status := Status{Scope: scope.String(), Report: report, Skills: []Row{}, MCPs: []Row{}, OffPacks: []string{}}
	if err != nil {
		return status, err
	}
	skills, servers := a.Rows(report)
	shown := map[string]bool{}
	for i, rows := range [][]Row{skills, servers} {
		for _, r := range rows {
			name := r.Name
			if i == 1 {
				name = catalog.MCPPrefix + name
			}
			shown[name] = shown[name] || !r.Unmanaged
			extra := slices.Contains(report.Extra, name)
			for agent, op := range r.Agents {
				r.Agents[agent] = stateOf(op, extra)
			}
		}
	}
	dirs, files := a.Columns(scope)
	// An off row names the pack that is off first, so that it is listed
	// under that pack.
	off := func(name, label, pack string, server bool, columns [][2]string) Row {
		others := slices.DeleteFunc(a.packsOf(name, server), func(other string) bool { return other == pack })
		r := Row{Name: name, Label: label, Packs: append([]string{pack}, others...), Agents: map[string]string{}}
		for _, col := range columns {
			r.Agents[col[0]] = StateOff
		}
		return r
	}
	for _, pack := range a.Catalog.PackNames() {
		if a.Catalog.Packs[pack].On() || scope.Project != (a.Catalog.PackOrigin[pack] == paths.ManifestName) {
			continue
		}
		status.OffPacks = append(status.OffPacks, pack)
		set := catalog.Set{Packs: []string{pack}}
		for _, skill := range a.Catalog.Resolve(set) {
			if !shown[skill] {
				shown[skill] = true
				skills = append(skills, off(skill, a.labelOf(skill), pack, false, dirs))
			}
		}
		// Servers are global: a project enables none.
		for _, server := range a.Catalog.ResolveMCPs(set) {
			if !scope.Project && !shown[catalog.MCPPrefix+server] {
				shown[catalog.MCPPrefix+server] = true
				servers = append(servers, off(server, "", pack, true, files))
			}
		}
	}
	byLabel := func(x, y Row) int { return strings.Compare(cmp.Or(x.Label, x.Name), cmp.Or(y.Label, y.Name)) }
	slices.SortStableFunc(skills, byLabel)
	slices.SortStableFunc(servers, byLabel)
	status.Skills, status.MCPs = append(status.Skills, skills...), append(status.MCPs, servers...)
	return status, nil
}

func stateOf(op string, extra bool) string {
	switch op {
	case "":
		return StateAbsent
	case link.OpKeep, mcp.OpKeep:
		if extra {
			return StateExtra
		}
		return StateOn
	case link.OpRelink, mcp.OpUpdate:
		return StateRepair
	case link.OpLink, link.OpReplace, mcp.OpAdd:
		return StateDrift
	case link.OpConflict:
		return StateConflict
	case link.OpDelete, mcp.OpDelete:
		return StateUnmanaged
	}
	return StateAbsent
}
