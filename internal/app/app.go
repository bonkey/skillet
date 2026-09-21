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
	// Catalog is Local merged with the included gists. Everything that
	// reads the catalog reads this one.
	Catalog *catalog.Catalog
	Index   *source.Index
	Gists   gist.Client
	// OnePassword reads the items listed under `secrets` in the catalog.
	OnePassword secrets.Reader

	// Force lets every sync delete unmanaged entries that stand where an
	// enabled skill goes.
	Force bool

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

// ProjectScope is the project around the working directory. Without a
// manifest up the tree, the working directory itself is the project. The
// home directory is never one: its agent directories are the global scope.
func (a *App) ProjectScope() (Scope, error) {
	if root, ok := a.Paths.ProjectRoot(); ok {
		return Scope{Project: true, Root: root}, nil
	}
	if a.Paths.Cwd == a.Paths.Home {
		return Scope{}, errors.New("the home directory cannot be a project")
	}
	return Scope{Project: true, Root: a.Paths.Cwd}, nil
}

func (a *App) manifest(scope Scope) string { return filepath.Join(scope.Root, paths.ManifestName) }

func (a *App) Set(scope Scope) (catalog.Set, error) {
	if !scope.Project {
		return a.Catalog.Enabled, nil
	}
	return catalog.LoadSet(a.manifest(scope))
}

func (a *App) SaveSet(scope Scope, set catalog.Set) error {
	if !scope.Project {
		a.Local.Enabled = set
		return a.Save()
	}
	return set.Save(a.manifest(scope))
}

// Enabled lists the skills a scope's declared set switches on.
func (a *App) Enabled(scope Scope) ([]string, error) {
	set, err := a.Set(scope)
	return a.Catalog.Resolve(set), err
}

type SyncReport struct {
	Scope   Scope
	Actions []link.Action
	Missing []string // enabled skills that are not available in their source's clone

	MCP []mcp.Action
	// MissingSecrets maps servers that were left alone to the ${NAME}
	// placeholders without a value.
	MissingSecrets map[string][]string
	// Notes are remarks for the user, such as servers a project cannot enable.
	Notes []string
}

type SyncOptions struct {
	DryRun bool
	// Replace allows deleting an unmanaged entry that stands where the
	// named skill goes. App.Force allows it for every skill.
	Replace func(name string) bool
}

// Sync makes the scope's agent directories match its declared set plus, in
// a project, the sets of running sessions. In the global scope it also
// writes the enabled MCP servers. The report of the latest global sync stays
// in LastSync for operations that sync as their last step.
func (a *App) Sync(scope Scope, opt SyncOptions) (SyncReport, error) {
	report, err := a.sync(scope, opt)
	if !scope.Project {
		a.LastSync = report
	}
	return report, err
}

func (a *App) sync(scope Scope, opt SyncOptions) (SyncReport, error) {
	report := SyncReport{Scope: scope}
	names, err := a.Enabled(scope)
	if err != nil {
		return report, err
	}
	if scope.Project {
		for _, live := range session.Live(session.ProjectDir(scope.Root)) {
			names = append(names, a.Catalog.Resolve(live.Set)...)
		}
	}
	desired := map[string]string{}
	for _, name := range names {
		if found, ok := a.Index.Lookup(a.Catalog, name); ok {
			desired[name] = found.Dir
		} else if !slices.Contains(report.Missing, name) {
			report.Missing = append(report.Missing, name)
		}
	}
	dirs, err := link.Dirs(a.Catalog.Agents, scope.Root, scope.Project)
	if err != nil {
		return report, err
	}
	if a.Force {
		opt.Replace = func(string) bool { return true }
	}
	report.Actions, err = link.Sync(link.Canonical(scope.Root), dirs, desired, link.Options{
		ReposDir: a.Paths.ReposDir(), Replace: opt.Replace, DryRun: opt.DryRun,
	})
	if err != nil {
		return report, err
	}
	if scope.Project {
		set, _ := a.Set(scope)
		if servers := a.Catalog.ResolveMCPs(set); len(servers) > 0 {
			report.Notes = append(report.Notes, fmt.Sprintf(
				"MCP servers are global: %s are not enabled by a project; enable them globally or use `skillet run`",
				strings.Join(servers, ", ")))
		}
		return report, nil
	}
	return report, a.syncMCP(&report, opt.DryRun)
}

// syncMCP writes the globally enabled servers into the user configs of the
// configured agents. A running session adds its servers for the agent it
// started, or for every agent when it started none of them. A server whose
// secrets are not all known is left as it is. Secrets are looked up only for
// entries that may need writing.
func (a *App) syncMCP(report *SyncReport, dryRun bool) error {
	resolver, err := a.resolver()
	if err != nil {
		return err
	}
	state, err := mcp.LoadState(a.Paths.MCPStateFile())
	if err != nil {
		return err
	}
	sessions := session.Live(a.Paths.SessionsDir())
	agents := slices.Clone(a.Catalog.Agents)
	for _, live := range sessions {
		if live.Agent != "" && !slices.Contains(agents, live.Agent) {
			agents = append(agents, live.Agent)
		}
	}
	// An agent outside `agents` that still holds session entries gets cleaned up.
	for _, agent := range mcp.Managed(a.Paths.Home, state) {
		if !slices.Contains(agents, agent) {
			agents = append(agents, agent)
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
			servers = a.Catalog.ResolveMCPs(a.Catalog.Enabled)
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

// Toggle enables or disables skills, servers ("mcp:name") and packs
// ("@name") in a scope and syncs it. Servers exist in the global scope only.
func (a *App) Toggle(scope Scope, enable bool, names ...string) (SyncReport, error) {
	if scope.Project {
		for _, name := range names {
			if strings.HasPrefix(name, catalog.MCPPrefix) {
				return SyncReport{}, fmt.Errorf("%s: MCP servers are global; drop -p, or use `skillet run`", name)
			}
		}
	}
	set, err := a.Set(scope)
	if err != nil {
		return SyncReport{}, err
	}
	if enable {
		err = a.Catalog.Enable(&set, names...)
	} else {
		err = a.Catalog.Disable(&set, names...)
	}
	if err != nil {
		return SyncReport{}, err
	}
	if err := a.SaveSet(scope, set); err != nil {
		return SyncReport{}, err
	}
	return a.Sync(scope, SyncOptions{})
}

// Fetch clones a source when needed and returns the skills it offers.
func (a *App) Fetch(arg, ref string) (name, url string, skills map[string]source.Skill, err error) {
	name, url, err = source.ParseName(arg)
	if err != nil {
		return
	}
	if src, ok := a.Catalog.Sources[name]; ok {
		url, ref = src.URL, src.Ref
	}
	dir := a.Paths.RepoDir(name)
	if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
		if err = source.Clone(url, ref, dir); err != nil {
			return
		}
	}
	skills, err = source.Discover(dir)
	return
}

// DropUnusedClone deletes the clone of a source the catalog does not list.
func (a *App) DropUnusedClone(name string) {
	if _, ok := a.Catalog.Sources[name]; !ok {
		os.RemoveAll(a.Paths.RepoDir(name))
		os.Remove(filepath.Dir(a.Paths.RepoDir(name))) // the owner folder; Remove only deletes an empty one
	}
}

type AddRequest struct {
	Source, URL, Ref string
	// All takes every skill the source offers, also those it gains later.
	// Skills then names what it offers today.
	All             bool
	Skills          []string
	Pack            string // optional pack to put the skills in
	PackDescription string // needed when Pack does not exist yet
	Enable          bool
}

// Add records skills of an already fetched source in the local catalog.
func (a *App) Add(req AddRequest) error {
	if req.Pack != "" {
		if _, ok := a.Local.Packs[req.Pack]; !ok && strings.TrimSpace(req.PackDescription) == "" {
			return fmt.Errorf("pack %q is new and needs a description", req.Pack)
		}
	}
	members := req.Skills
	if req.All {
		a.Local.AddSource(req.Source, req.URL, req.Ref)
		members = []string{req.Source}
	} else if err := a.Local.AddSkills(req.Source, req.URL, req.Ref, req.Skills); err != nil {
		return err
	}
	if req.Pack != "" {
		if _, ok := a.Local.Packs[req.Pack]; !ok {
			if err := a.Local.CreatePack(req.Pack, req.PackDescription, nil); err != nil {
				return err
			}
		}
		if err := a.Local.PackAdd(req.Pack, members); err != nil {
			return err
		}
	}
	if err := a.saveAndSync(); err != nil || !req.Enable {
		return err
	}
	// A whole source in a pack is enabled through the pack, so that the
	// skills it gains later are enabled too.
	names := req.Skills
	if req.All && req.Pack != "" {
		names = []string{"@" + req.Pack}
	}
	_, err := a.Toggle(a.Global(), true, names...)
	return err
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

// EditPack changes or creates a pack of the local catalog. A pack that only
// an included gist defines cannot be changed, but a new local pack of the
// same name overrides it.
func (a *App) EditPack(name string, create bool, edit func(local *catalog.Catalog) error) error {
	if _, own := a.Local.Packs[name]; !own && !create {
		if from, ok := a.Catalog.PackOrigin[name]; ok {
			return fmt.Errorf("pack %q comes from gist %s; create a pack of the same name to override it", name, from)
		}
	}
	a.Local.Lookup = func(member string) (string, bool) {
		if server, ok := strings.CutPrefix(member, catalog.MCPPrefix); ok {
			return member, a.Catalog.MCPs[server] != nil
		}
		skill, source := catalog.SplitRef(member)
		owner, ok := a.Catalog.SourceOf(skill)
		return skill + "@" + owner, ok && (source == "" || source == owner)
	}
	if err := edit(a.Local); err != nil {
		return err
	}
	return a.saveAndSync()
}

// ownsAll reports whether a skill belongs to a local source that takes all
// the skills it offers.
func (a *App) ownsAll(skill string) bool {
	owner, _ := a.Catalog.SourceOf(skill)
	src := a.Local.Sources[owner]
	return src != nil && len(src.Skills) == 0
}

// Remove deletes skills, servers ("mcp:name") and sources ("owner/repo")
// from the local catalog. "@pack" deletes the pack together with its
// skills, servers and sources. Entries of included gists cannot be removed.
// Clones of sources the catalog does not list are deleted.
func (a *App) Remove(names ...string) (SyncReport, error) {
	var skills, servers, sources []string
	for _, name := range names {
		pack, isPack := strings.CutPrefix(name, "@")
		server, isMCP := strings.CutPrefix(name, catalog.MCPPrefix)
		switch {
		case isMCP && a.Local.MCPs[server] != nil:
			servers = append(servers, server)
		case isMCP && a.Catalog.MCPs[server] != nil:
			return SyncReport{}, fmt.Errorf("mcp server %q comes from gist %s and cannot be removed here; disable it instead", server, a.Catalog.MCPOrigin[server])
		case isMCP:
			return SyncReport{}, fmt.Errorf("unknown mcp server %q", server)
		case a.Local.Sources[name] != nil:
			sources = append(sources, name)
		case isPack && a.Local.Packs[pack] != nil:
			for _, skill := range a.Local.PackSkills(pack) {
				if a.Local.HasSkill(skill) {
					skills = append(skills, skill)
				}
			}
			for _, server := range a.Local.Packs[pack].MCPs {
				if a.Local.MCPs[server] != nil {
					servers = append(servers, server)
				}
			}
			for _, src := range a.Local.Packs[pack].Sources {
				if a.Local.Sources[src] != nil {
					sources = append(sources, src)
				}
			}
		case !isPack && a.Local.HasSkill(name):
			skills = append(skills, name)
		case isPack && a.Catalog.Packs[pack] != nil:
			return SyncReport{}, fmt.Errorf("pack %q comes from gist %s and cannot be removed here", pack, a.Catalog.PackOrigin[pack])
		case !isPack && a.Catalog.HasSkill(name) && a.ownsAll(name):
			owner, _ := a.Catalog.SourceOf(name)
			return SyncReport{}, fmt.Errorf("skill %q comes with all of %s; disable it, or remove the source", name, owner)
		case !isPack && a.Catalog.HasSkill(name):
			return SyncReport{}, fmt.Errorf("skill %q comes from gist %s and cannot be removed here; disable it instead", name, a.Catalog.SkillOrigin[name])
		default:
			return SyncReport{}, fmt.Errorf("unknown skill or pack %q", name)
		}
	}
	before := a.SourceNames()
	for _, skill := range skills {
		a.Local.RemoveSkill(skill)
	}
	for _, server := range servers {
		a.Local.RemoveMCP(server)
	}
	for _, src := range sources {
		a.Local.RemoveSource(src)
	}
	for _, name := range names {
		if pack, ok := strings.CutPrefix(name, "@"); ok {
			a.Local.RemovePack(pack)
		}
	}
	if err := a.Save(); err != nil {
		return SyncReport{}, err
	}
	for _, name := range before {
		a.DropUnusedClone(name)
	}
	if err := a.Reindex(); err != nil {
		return SyncReport{}, err
	}
	return a.Sync(a.Global(), SyncOptions{})
}

// SetRef makes a source track another branch, tag or commit ("" for the
// default branch) and moves its clone there.
func (a *App) SetRef(name, ref string) error {
	src, ok := a.Local.Sources[name]
	if !ok {
		if _, included := a.Catalog.Sources[name]; included {
			return fmt.Errorf("source %q comes from an included gist; its ref is set there", name)
		}
		return fmt.Errorf("unknown source %q", name)
	}
	if _, err := fetchLatest(src.URL, ref, a.Paths.RepoDir(name), false); err != nil {
		return err
	}
	src.Ref = ref
	return a.saveAndSync()
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
// starts, or of every configured agent when it starts none of them.
func (a *App) Run(names []string, argv []string) (int, error) {
	var set catalog.Set
	if err := a.Catalog.Enable(&set, names...); err != nil {
		return 1, err
	}
	scope, err := a.ProjectScope()
	if err != nil {
		return 1, err
	}
	pid := os.Getpid()
	live := session.Session{Set: set, Agent: agentOf(argv[0])}
	dirs := []string{session.ProjectDir(scope.Root)}
	scopes := []Scope{scope}
	if len(a.Catalog.ResolveMCPs(set)) > 0 {
		dirs, scopes = append(dirs, a.Paths.SessionsDir()), append(scopes, a.Global())
	}
	for _, dir := range dirs {
		if err := session.Write(dir, pid, live); err != nil {
			return 1, err
		}
	}
	defer func() {
		for i, dir := range dirs {
			session.Remove(dir, pid)
			a.Sync(scopes[i], SyncOptions{})
		}
	}()
	for _, s := range scopes {
		if _, err := a.Sync(s, SyncOptions{}); err != nil {
			return 1, err
		}
	}
	return session.Run(argv)
}
