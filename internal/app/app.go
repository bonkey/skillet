// Package app implements the operations shared by the command line and the
// TUI.
package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/gist"
	"github.com/bonkey/skillet/internal/link"
	"github.com/bonkey/skillet/internal/mcp"
	"github.com/bonkey/skillet/internal/paths"
	"github.com/bonkey/skillet/internal/secrets"
	"github.com/bonkey/skillet/internal/session"
	"github.com/bonkey/skillet/internal/source"
)

type App struct {
	Paths paths.Paths
	// Local is the user's own catalog: the only one that is edited and saved.
	Local *catalog.Catalog
	// Project is the catalog in the project's manifest, or nil without one.
	Project *catalog.Catalog
	// Catalog is Local merged with the included gists and with Project.
	// Everything that reads the catalog reads this one.
	Catalog *catalog.Catalog
	Index   *source.Index
	Gists   gist.Client
	// OnePassword reads the items listed under `secrets` in the catalog.
	OnePassword secrets.Reader

	// Force lets every sync delete unmanaged entries that stand where an
	// enabled skill goes.
	Force bool
	// Agents, when set, takes the place of the catalog's list of agents.
	Agents []string

	LastSync SyncReport

	Included []Included // the included gists, flattened in merge order
	Warnings []string   // problems with included gists
}

func Open(p paths.Paths) (*App, error) { return OpenWith(p, gist.GH{}) }

func OpenWith(p paths.Paths, client gist.Client) (*App, error) {
	local, err := catalog.Load(p.ConfigFile())
	if err != nil {
		return nil, err
	}
	a := &App{Paths: p, Local: local, Gists: client, OnePassword: secrets.OP{}}
	if root, ok := p.ProjectRoot(); ok {
		manifest := filepath.Join(root, paths.ManifestName)
		if _, err := os.Stat(manifest); err == nil {
			if a.Project, err = catalog.Load(manifest); err != nil {
				return nil, fmt.Errorf("%w\nA project file is a catalog like config.toml", err)
			}
		}
	}
	if err := a.reload(false); err != nil {
		return nil, err
	}
	return a, a.Reindex()
}

// Save writes the local catalog and rebuilds the merged one.
func (a *App) Save() error {
	if err := a.Local.Save(a.Paths.ConfigFile()); err != nil {
		return err
	}
	return a.reload(false)
}

func (a *App) Reindex() error {
	idx, err := source.LoadIndex(a.Paths, a.Catalog)
	a.Index = idx
	a.expand()
	return err
}

// expand gives the sources that take all skills the names their clones offer.
func (a *App) expand() {
	if a.Index == nil {
		return
	}
	offered := map[string][]string{}
	for name, indexed := range a.Index.Sources {
		for skill := range indexed.Skills {
			offered[name] = append(offered[name], skill)
		}
	}
	a.Catalog.ExpandAll(offered)
}

// Scope is where skills get enabled: globally, or in one project.
type Scope struct {
	Project bool
	Root    string
}

func (s Scope) String() string {
	if s.Project {
		return "project " + s.Root
	}
	return "global"
}

func (a *App) Global() Scope { return Scope{Root: a.Paths.Home} }

// ProjectScope is the project around the working directory.
func (a *App) ProjectScope() (Scope, error) {
	root, ok := a.Paths.ProjectRoot()
	if !ok {
		return Scope{}, errors.New("the home directory cannot be a project")
	}
	return Scope{Project: true, Root: root}, nil
}

// UseAgents makes every operation act on the given agents only.
func (a *App) UseAgents(agents []string) {
	a.Agents, a.Catalog.Agents = agents, agents
}

// sessionsDir holds the running `skillet run` sessions of a scope.
func (a *App) sessionsDir(scope Scope) string {
	if scope.Project {
		return session.ProjectDir(scope.Root)
	}
	return a.Paths.SessionsDir()
}

// Declared is what the flags of a scope's config file switch on: the
// entries of the catalog file and the included gists for the global scope,
// the manifest's own entries for a project.
func (a *App) Declared(scope Scope) catalog.Set {
	switch {
	case !scope.Project:
		return a.Catalog.Declared(func(origin string) bool { return origin != paths.ManifestName })
	case a.Project != nil:
		set := a.Catalog.Declared(func(origin string) bool { return origin == paths.ManifestName })
		set.MCPs = nil
		return set
	}
	return catalog.Set{}
}

// Set reads what is enabled in a scope from the disk: the skills with a
// managed link in one of the scope's agent directories and, globally, the
// servers skillet manages in the agents' configs. What a `run` session added
// is not part of it.
func (a *App) Set(scope Scope) (catalog.Set, error) {
	dirs, err := link.Dirs(a.Catalog.Agents, scope.Root, scope.Project)
	if err != nil {
		return catalog.Set{}, err
	}
	set := catalog.Set{Skills: link.Linked(dirs, a.Paths.ReposDir())}
	if !scope.Project {
		state, err := mcp.LoadState(a.Paths.MCPStateFile())
		if err != nil {
			return set, err
		}
		set.MCPs = mcp.Enabled(a.Paths.Home, a.Catalog.Agents, state)
	}
	live, dead := session.Read(a.sessionsDir(scope))
	for _, s := range append(live, dead...) {
		for _, name := range s.Added {
			if server, ok := strings.CutPrefix(name, catalog.MCPPrefix); ok {
				set.MCPs = slices.DeleteFunc(set.MCPs, func(other string) bool { return other == server })
			} else {
				set.Skills = slices.DeleteFunc(set.Skills, func(other string) bool { return other == name })
			}
		}
	}
	return set, nil
}

// Enabled lists the catalog skills that are enabled in a scope.
func (a *App) Enabled(scope Scope) ([]string, error) {
	set, err := a.Set(scope)
	return a.Catalog.Resolve(set), err
}

type SyncReport struct {
	Scope   Scope
	Actions []link.Action
	Missing []string // enabled skills that are not available in their source's clone; their links stay
	// Extra lists the skills, and servers as "mcp:name", that are enabled
	// without being declared in the scope's config file.
	Extra []string

	MCP []mcp.Action
	// MissingSecrets maps servers that were left alone to the ${NAME}
	// placeholders without a value.
	MissingSecrets map[string][]string
	// Notes are remarks for the user, such as servers a project cannot enable.
	Notes []string
}

type SyncOptions struct {
	DryRun bool
	// Remove disables what is enabled on the disk but not declared.
	Remove bool
	// Replace allows deleting an unmanaged entry that stands where the
	// named skill goes. App.Force allows it for every skill.
	Replace func(name string) bool
}

// Sync enables what the scope's config file declares and repairs the rest:
// every agent directory gets the links of the enabled skills, and a link
// follows a skill that moved inside its source. What is enabled without being
// declared stays and is reported as extra, unless opt.Remove is set. A
// project without a manifest has no extras. In the
// global scope the same happens to the servers in the agents' configs. The
// report of the latest global sync stays in LastSync for operations that
// sync as their last step.
func (a *App) Sync(scope Scope, opt SyncOptions) (SyncReport, error) {
	var failed []string
	if !opt.DryRun {
		var err error
		if failed, err = a.cloneMissing(); err != nil {
			return SyncReport{Scope: scope}, err
		}
	}
	own, err := a.Set(scope)
	if err != nil {
		return SyncReport{Scope: scope}, err
	}
	// A project without a manifest declares nothing: all its links stay.
	if scope.Project && a.Project == nil {
		opt.Remove = false
	}
	declared := a.Declared(scope)
	skills, servers := a.Catalog.Resolve(declared), a.Catalog.ResolveMCPs(declared)
	var extra []string
	for _, skill := range own.Skills {
		if !slices.Contains(skills, skill) {
			extra = append(extra, skill)
		}
	}
	for _, server := range own.MCPs {
		if !slices.Contains(servers, server) {
			extra = append(extra, catalog.MCPPrefix+server)
		}
	}
	if !opt.Remove {
		declared = own
		skills = append(skills, a.Catalog.Resolve(own)...)
		servers = append(servers, a.Catalog.ResolveMCPs(own)...)
	}
	report, err := a.apply(scope, enabling{declared, skills, servers}, opt)
	if !opt.Remove && (!scope.Project || a.Project != nil) {
		report.Extra = extra
	}
	report.Notes = append(report.Notes, failed...)
	return report, err
}

// cloneMissing clones the sources that have no clone yet, so that a catalog
// file alone is enough to enable its skills. It returns the failures.
func (a *App) cloneMissing() (failed []string, err error) {
	var missing []string
	for _, name := range a.SourceNames() {
		if _, err := os.Stat(a.Paths.RepoDir(name)); os.IsNotExist(err) {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	errs := make([]error, len(missing))
	parallel(len(missing), func(i int) {
		src := a.Catalog.Sources[missing[i]]
		errs[i] = source.Clone(src.URL, src.Ref, a.Paths.RepoDir(missing[i]))
	})
	for i, err := range errs {
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s could not be cloned: %v", missing[i], err))
		}
	}
	return failed, a.Reindex()
}

// enabling is what apply makes a scope hold. set names the links that may
// stay although the catalog does not know their skill.
type enabling struct {
	set     catalog.Set
	skills  []string
	servers []string
}

func (a *App) enablingOf(set catalog.Set) enabling {
	return enabling{set, a.Catalog.Resolve(set), a.Catalog.ResolveMCPs(set)}
}

// apply makes a scope hold what is given plus the sets of its running
// sessions.
func (a *App) apply(scope Scope, want enabling, opt SyncOptions) (SyncReport, error) {
	report, err := a.link(scope, want, opt)
	if !scope.Project {
		a.LastSync = report
	}
	return report, err
}

func (a *App) link(scope Scope, want enabling, opt SyncOptions) (SyncReport, error) {
	report := SyncReport{Scope: scope}
	live, _ := session.Read(a.sessionsDir(scope))
	names := slices.Clone(want.skills)
	for _, s := range live {
		names = append(names, a.Catalog.Resolve(s.Set)...)
	}
	desired, keep := map[string]string{}, map[string]bool{}
	for _, name := range names {
		if found, ok := a.Index.Lookup(a.Catalog, name); ok {
			desired[name] = found.Dir
		} else if !keep[name] {
			keep[name] = true
			report.Missing = append(report.Missing, name)
		}
	}
	dirs, err := link.Dirs(a.Catalog.Agents, scope.Root, scope.Project)
	if err != nil {
		return report, err
	}
	// A link to a skill the catalog does not know stays while it leads somewhere.
	for _, name := range want.set.Skills {
		if !a.Catalog.HasSkill(name) && slices.ContainsFunc(dirs, func(dir string) bool {
			_, err := os.Stat(filepath.Join(dir, name))
			return err == nil
		}) {
			keep[name] = true
		}
	}
	if a.Force {
		opt.Replace = func(string) bool { return true }
	}
	report.Actions, err = link.Sync(dirs, desired, link.Options{
		ReposDir: a.Paths.ReposDir(), Keep: keep, Replace: opt.Replace, DryRun: opt.DryRun,
	})
	if err != nil {
		return report, err
	}
	if scope.Project {
		if len(want.servers) > 0 {
			report.Notes = append(report.Notes, fmt.Sprintf(
				"MCP servers are global: %s are not enabled by a project; enable them globally or use `skillet run`",
				strings.Join(want.servers, ", ")))
		}
	} else if err := a.syncMCP(&report, want.servers, live, opt.DryRun); err != nil {
		return report, err
	}
	if !opt.DryRun {
		session.Prune(a.sessionsDir(scope))
	}
	return report, nil
}

// syncMCP writes the enabled servers into the user configs of the
// configured agents. A running session adds its servers for the agent it
// started, or for every agent when it started none of them. A server whose
// secrets are not all known is left as it is. Secrets are looked up only for
// entries that may need writing.
func (a *App) syncMCP(report *SyncReport, enabled []string, sessions []session.Session, dryRun bool) error {
	resolver, err := a.resolver()
	if err != nil {
		return err
	}
	state, err := mcp.LoadState(a.Paths.MCPStateFile())
	if err != nil {
		return err
	}
	agents := slices.Clone(a.Catalog.Agents)
	for _, live := range sessions {
		if live.Agent != "" && !slices.Contains(agents, live.Agent) {
			agents = append(agents, live.Agent)
		}
	}
	// An agent outside the catalog's list that still holds entries gets cleaned up.
	if a.Agents == nil {
		for _, agent := range mcp.Managed(a.Paths.Home, state) {
			if !slices.Contains(agents, agent) {
				agents = append(agents, agent)
			}
		}
	}
	if slices.Contains(agents, "crush") {
		if _, err := os.Stat(filepath.Join(a.Paths.Home, ".config", "crush", "crushrc")); err == nil {
			report.Notes = append(report.Notes,
				"crush prefers ~/.config/crush/crushrc over crush.json, where skillet writes its servers")
		}
	}
	expand := func(def catalog.MCP) (catalog.MCP, []string) { return expandMCP(def, resolver.Expand) }
	for _, agent := range agents {
		var servers []string
		if slices.Contains(a.Catalog.Agents, agent) {
			servers = slices.Clone(enabled)
		}
		for _, live := range sessions {
			if live.Agent == "" || live.Agent == agent {
				servers = append(servers, a.Catalog.ResolveMCPs(live.Set)...)
			}
		}
		desired := map[string]catalog.MCP{}
		for _, name := range servers {
			desired[name] = *a.Catalog.MCPs[name]
		}
		actions, missing, err := mcp.Sync(a.Paths.Home, []string{agent}, desired, expand, state, mcp.Options{DryRun: dryRun})
		report.MCP = append(report.MCP, actions...)
		for name, names := range missing {
			if report.MissingSecrets == nil {
				report.MissingSecrets = map[string][]string{}
			}
			report.MissingSecrets[name] = names
		}
		if err != nil {
			return err
		}
	}
	for _, problem := range resolver.Problems {
		report.Notes = append(report.Notes, "1Password: "+problem)
	}
	if dryRun {
		return nil
	}
	return state.Save(a.Paths.MCPStateFile())
}

// resolver looks secrets up in the local store and then in the 1Password
// items of the local catalog.
func (a *App) resolver() (*secrets.Resolver, error) {
	local, err := secrets.Load(a.Paths.SecretsFile())
	if err != nil {
		return nil, err
	}
	r := &secrets.Resolver{Local: local, Reader: a.OnePassword}
	for _, item := range a.Local.Secrets {
		r.Items = append(r.Items, secrets.Item{Account: item.Account, Vault: item.Vault, Item: item.Item})
	}
	return r, nil
}

// expandMCP fills in the secrets of a definition and lists the placeholders
// that have no value.
func expandMCP(def catalog.MCP, expandText func(string) (string, []string)) (catalog.MCP, []string) {
	var missing []string
	expand := func(text string) string {
		out, names := expandText(text)
		for _, name := range names {
			if !slices.Contains(missing, name) {
				missing = append(missing, name)
			}
		}
		return out
	}
	expandMap := func(in map[string]string) map[string]string {
		if in == nil {
			return nil
		}
		out := make(map[string]string, len(in))
		for key, value := range in {
			out[key] = expand(value)
		}
		return out
	}
	def.URL = expand(def.URL)
	command := make([]string, len(def.Command))
	for i, arg := range def.Command {
		command[i] = expand(arg)
	}
	def.Command = command
	def.Environment, def.Headers = expandMap(def.Environment), expandMap(def.Headers)
	sort.Strings(missing)
	return def, missing
}

// Toggle enables or disables skills, servers ("mcp:name"), packs ("@name")
// and sources ("skills:name") in a scope. Servers exist in the global scope
// only. With save, the entries' flags in the scope's config file change too.
func (a *App) Toggle(scope Scope, enable, save bool, names ...string) (SyncReport, error) {
	if scope.Project {
		for _, name := range names {
			if strings.HasPrefix(name, catalog.MCPPrefix) {
				return SyncReport{}, fmt.Errorf("%s: MCP servers are global; drop -p, or use `skillet run`", name)
			}
		}
	}
	if save {
		if err := a.saveFlags(scope, enable, names); err != nil {
			return SyncReport{}, err
		}
	}
	set, err := a.Set(scope)
	if err != nil {
		return SyncReport{}, err
	}
	before := a.enablingOf(set)
	if enable {
		err = a.Catalog.Enable(&set, names...)
	} else {
		err = a.Catalog.Disable(&set, names...)
	}
	if err != nil {
		return SyncReport{}, err
	}
	after := a.enablingOf(set)
	report, err := a.apply(scope, after, SyncOptions{})
	if !enable {
		// What the config file declares comes back with the next sync.
		declared := a.enablingOf(a.Declared(scope))
		var back []string
		for _, skill := range before.skills {
			if !slices.Contains(after.skills, skill) && slices.Contains(declared.skills, skill) {
				back = append(back, skill)
			}
		}
		for _, server := range before.servers {
			if !slices.Contains(after.servers, server) && slices.Contains(declared.servers, server) {
				back = append(back, catalog.MCPPrefix+server)
			}
		}
		if len(back) > 0 {
			report.Notes = append(report.Notes, fmt.Sprintf("%s: on in %s, so `skillet sync` enables them again; `disable --save` switches them off there",
				strings.Join(back, ", "), a.configOf(scope)))
		}
	}
	return report, err
}

// saveFlags switches the named entries on or off in the scope's config
// file. The entries must be the file's own: globally those of config.toml,
// in a project those of its manifest. Switching on an entry that stays off
// through its source or its packs is refused, with the command that works.
func (a *App) saveFlags(scope Scope, on bool, names []string) error {
	if err := a.Catalog.Check(names); err != nil {
		return err
	}
	target, file, own := a.Local, a.Paths.ConfigFile(), ""
	if scope.Project {
		if a.Project == nil {
			return fmt.Errorf("%s has no %s to save to", scope.Root, paths.ManifestName)
		}
		target, file, own = a.Project, a.configOf(scope), paths.ManifestName
	}
	for _, name := range names {
		if origin := a.originOf(name); origin != own {
			switch {
			case origin != "" && origin != paths.ManifestName:
				return fmt.Errorf("%s comes from gist %s; its flag lives in that catalog", name, origin)
			case scope.Project:
				return fmt.Errorf("%s is not defined in %s; a project flags only its own entries", name, file)
			default:
				return fmt.Errorf("%s is defined in the project's %s; use -p", name, paths.ManifestName)
			}
		}
	}
	trial, err := copyOf(target)
	if err != nil {
		return err
	}
	if err := a.setFlags(trial, on, names); err != nil {
		return err
	}
	if on {
		for _, name := range names {
			if err := a.staysOff(trial, name); err != nil {
				return err
			}
		}
	}
	if err := a.setFlags(target, on, names); err != nil {
		return err
	}
	if !scope.Project {
		return a.Save()
	}
	if err := target.Save(file); err != nil {
		return err
	}
	return a.merge()
}

// originOf names where an entry comes from: "" for the catalog file, the
// manifest's name, or a gist id.
func (a *App) originOf(name string) string {
	switch {
	case strings.HasPrefix(name, catalog.PackPrefix):
		return a.Catalog.PackOrigin[strings.TrimPrefix(name, catalog.PackPrefix)]
	case strings.HasPrefix(name, catalog.MCPPrefix):
		return a.Catalog.MCPOrigin[strings.TrimPrefix(name, catalog.MCPPrefix)]
	case catalog.IsSource(name):
		return a.Catalog.SourceOrigin[strings.TrimPrefix(name, catalog.SourcePrefix)]
	}
	skill, _ := catalog.SplitRef(name)
	source, _ := a.Catalog.SourceOf(skill)
	return a.Catalog.SourceOrigin[source]
}

// setFlags applies the flags to a catalog; a skill's source is looked up in
// the merged catalog, which knows the skills of sources that take all.
func (a *App) setFlags(c *catalog.Catalog, on bool, names []string) error {
	for _, name := range names {
		if strings.HasPrefix(name, catalog.PackPrefix) || strings.HasPrefix(name, catalog.MCPPrefix) || catalog.IsSource(name) {
			if err := c.SetFlag(name, on); err != nil {
				return err
			}
			continue
		}
		skill, _ := catalog.SplitRef(name)
		source, _ := a.Catalog.SourceOf(skill)
		if err := c.SetSkillFlag(source, skill, on); err != nil {
			return err
		}
	}
	return nil
}

// staysOff explains why a skill or server switched on in c is still off:
// its source, or every pack of c that holds it, is switched off.
func (a *App) staysOff(c *catalog.Catalog, name string) error {
	var item string
	var holds func(pack string) []string
	switch {
	case strings.HasPrefix(name, catalog.PackPrefix), catalog.IsSource(name):
		return nil
	case strings.HasPrefix(name, catalog.MCPPrefix):
		item = strings.TrimPrefix(name, catalog.MCPPrefix)
		holds = func(pack string) []string { return c.Packs[pack].MCPs }
	default:
		item, _ = catalog.SplitRef(name)
		source, _ := a.Catalog.SourceOf(item)
		if src, ok := c.Sources[source]; ok && !src.On() {
			return fmt.Errorf("%s stays off: its source %s is switched off; `skillet enable --save %s%s` switches it on",
				item, source, catalog.SourcePrefix, source)
		}
		holds = func(pack string) []string { return a.Catalog.PackSkills(pack) }
	}
	var packs []string
	on := false
	for _, pack := range c.PackNames() {
		if slices.Contains(holds(pack), item) {
			packs = append(packs, pack)
			on = on || c.Packs[pack].On()
		}
	}
	if len(packs) > 0 && !on {
		return fmt.Errorf("%s stays off: every pack that holds it is switched off (%s); `skillet enable --save @%s` switches one on",
			item, strings.Join(packs, ", "), packs[0])
	}
	return nil
}

// copyOf clones a catalog through its file form.
func copyOf(c *catalog.Catalog) (*catalog.Catalog, error) {
	data, err := c.Encode()
	if err != nil {
		return nil, err
	}
	return catalog.Parse(data)
}

// configOf names the file that declares what a scope enables.
func (a *App) configOf(scope Scope) string {
	if scope.Project {
		return filepath.Join(scope.Root, paths.ManifestName)
	}
	return a.Paths.ConfigFile()
}

// DropUnusedClone deletes the clone of a source the catalog does not list.
func (a *App) DropUnusedClone(name string) {
	if _, ok := a.Catalog.Sources[name]; !ok {
		os.RemoveAll(a.Paths.RepoDir(name))
	}
}

// saveAndSync persists a change of the local catalog and brings the index
// and the global links in line with it.
func (a *App) saveAndSync() error {
	if err := a.Save(); err != nil {
		return err
	}
	if err := a.Reindex(); err != nil {
		return err
	}
	_, err := a.Sync(a.Global(), SyncOptions{})
	return err
}

func (a *App) SourceNames() []string {
	names := make([]string, 0, len(a.Catalog.Sources))
	for name := range a.Catalog.Sources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type SourceUpdate struct {
	Source  string
	Cloned  bool     // the clone was missing and has been created
	Changed []string // catalog skills with upstream changes
	Other   bool     // upstream moved without touching catalog skills
	Err     error
}

// Update fetches every source and reports upstream changes. Unless check is
// set, clones are moved to the fetched commit. Missing clones are created
// either way.
func (a *App) Update(check bool) ([]SourceUpdate, error) {
	if err := a.reload(true); err != nil {
		return nil, err
	}
	names := a.SourceNames()
	updates := make([]SourceUpdate, len(names))
	parallel(len(names), func(i int) {
		updates[i] = a.updateSource(names[i], check)
	})
	if err := a.Reindex(); err != nil {
		return updates, err
	}
	_, err := a.Sync(a.Global(), SyncOptions{})
	return updates, err
}

func (a *App) updateSource(name string, check bool) SourceUpdate {
	update := SourceUpdate{Source: name}
	src, dir := a.Catalog.Sources[name], a.Paths.RepoDir(name)
	cloned, err := fetchLatest(src.URL, src.Ref, dir, true)
	if cloned || err != nil {
		update.Cloned, update.Err = cloned, err
		return update
	}
	old, err := source.Head(dir)
	if err != nil {
		update.Err = err
		return update
	}
	latest, err := source.Fetch(dir, src.Ref)
	if err != nil || latest == old {
		update.Err = err
		return update
	}
	for _, skill := range src.Skills {
		found, ok := a.Index.Lookup(a.Catalog, skill)
		if !ok {
			continue
		}
		if changed, err := source.Changed(dir, old, latest, found.Path); err == nil && changed {
			update.Changed = append(update.Changed, skill)
		}
	}
	update.Other = len(update.Changed) == 0
	if !check {
		update.Err = source.Checkout(dir, latest)
	}
	return update
}

// fetchLatest clones a source that has no clone yet. With an existing clone
// it does nothing when keep is set, and otherwise moves it to the latest
// upstream commit.
func fetchLatest(url, ref, dir string, keep bool) (cloned bool, err error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return true, source.Clone(url, ref, dir)
	}
	if keep {
		return false, nil
	}
	latest, err := source.Fetch(dir, ref)
	if err != nil {
		return false, err
	}
	return false, source.Checkout(dir, latest)
}

// parallel runs fn for 0..n-1 with a few workers.
func parallel(n int, fn func(i int)) {
	var wg sync.WaitGroup
	slots := make(chan struct{}, 6)
	for i := range n {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			fn(i)
		}()
	}
	wg.Wait()
}

// agentOf names the agent that a command starts, or "" for other commands.
func agentOf(command string) string {
	switch filepath.Base(command) {
	case "claude":
		return "claude-code"
	case "gemini":
		return "gemini-cli"
	case "cursor", "cursor-agent":
		return "cursor"
	case "codex", "crush", "opencode", "zed":
		return filepath.Base(command)
	}
	return ""
}

// Run enables a set for as long as a command runs: its skills in the
// project, its servers in the user config of the agent that the command
// starts, or of every configured agent when it starts none of them. What
// was enabled before stays enabled afterwards.
func (a *App) Run(names []string, argv []string) (int, error) {
	var set catalog.Set
	if err := a.Catalog.Enable(&set, names...); err != nil {
		return 1, err
	}
	project, err := a.ProjectScope()
	if err != nil {
		return 1, err
	}
	scopes := []Scope{project}
	if len(a.Catalog.ResolveMCPs(set)) > 0 {
		scopes = append(scopes, a.Global())
	}
	live := session.Session{Set: set, Agent: agentOf(argv[0])}
	for _, scope := range scopes {
		own, err := a.Set(scope)
		if err != nil {
			return 1, err
		}
		if scope.Project {
			for _, skill := range a.Catalog.Resolve(set) {
				if !slices.Contains(own.Skills, skill) {
					live.Added = append(live.Added, skill)
				}
			}
		} else {
			for _, server := range a.Catalog.ResolveMCPs(set) {
				if !slices.Contains(own.MCPs, server) {
					live.Added = append(live.Added, catalog.MCPPrefix+server)
				}
			}
		}
	}
	pid := os.Getpid()
	for _, scope := range scopes {
		if err := session.Write(a.sessionsDir(scope), pid, live); err != nil {
			return 1, err
		}
	}
	defer func() {
		for _, scope := range scopes {
			own, err := a.Set(scope)
			session.Remove(a.sessionsDir(scope), pid)
			if err == nil {
				a.apply(scope, a.enablingOf(own), SyncOptions{})
			}
		}
	}()
	for _, scope := range scopes {
		if _, err := a.Sync(scope, SyncOptions{}); err != nil {
			return 1, err
		}
	}
	return session.Run(argv)
}
