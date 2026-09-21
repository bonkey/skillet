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
	"github.com/bonkey/skillet/internal/paths"
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

	Included []Included // the included gists, flattened in merge order
	Warnings []string   // problems with included gists
}

func Open(p paths.Paths) (*App, error) { return OpenWith(p, gist.GH{}) }

func OpenWith(p paths.Paths, client gist.Client) (*App, error) {
	local, err := catalog.Load(p.CatalogFile())
	if err != nil {
		return nil, err
	}
	a := &App{Paths: p, Local: local, Gists: client}
	if err := a.reload(false); err != nil {
		return nil, err
	}
	return a, a.Reindex()
}

// Save writes the local catalog and rebuilds the merged one.
func (a *App) Save() error {
	if err := a.Local.Save(a.Paths.CatalogFile()); err != nil {
		return err
	}
	return a.reload(false)
}

func (a *App) Reindex() error {
	idx, err := source.LoadIndex(a.Paths, a.Catalog)
	a.Index = idx
	return err
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
}

type SyncOptions struct {
	DryRun bool
	// Replace allows deleting an unmanaged entry of the canonical skills
	// directory that stands where the named skill goes.
	Replace func(name string) bool
}

// Sync makes the scope's agent directories match its declared set plus, in
// a project, the sets of running sessions.
func (a *App) Sync(scope Scope, opt SyncOptions) (SyncReport, error) {
	report := SyncReport{Scope: scope}
	names, err := a.Enabled(scope)
	if err != nil {
		return report, err
	}
	if scope.Project {
		for _, set := range session.Live(scope.Root) {
			names = append(names, a.Catalog.Resolve(set)...)
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
	report.Actions, err = link.Sync(link.Canonical(scope.Root), dirs, desired, link.Options{
		ReposDir: a.Paths.ReposDir(), Replace: opt.Replace, DryRun: opt.DryRun,
	})
	return report, err
}

// Toggle enables or disables skills and packs ("@name") in a scope and syncs it.
func (a *App) Toggle(scope Scope, enable bool, names ...string) (SyncReport, error) {
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
	Skills           []string
	Pack             string // optional pack to put the skills in
	PackDescription  string // needed when Pack does not exist yet
	Enable           bool
}

// Add records skills of an already fetched source in the local catalog.
func (a *App) Add(req AddRequest) error {
	if req.Pack != "" {
		if _, ok := a.Local.Packs[req.Pack]; !ok && strings.TrimSpace(req.PackDescription) == "" {
			return fmt.Errorf("pack %q is new and needs a description", req.Pack)
		}
	}
	if err := a.Local.AddSkills(req.Source, req.URL, req.Ref, req.Skills); err != nil {
		return err
	}
	if req.Pack != "" {
		if _, ok := a.Local.Packs[req.Pack]; !ok {
			if err := a.Local.CreatePack(req.Pack, req.PackDescription, nil); err != nil {
				return err
			}
		}
		if err := a.Local.PackAdd(req.Pack, req.Skills); err != nil {
			return err
		}
	}
	if req.Enable {
		if err := a.Local.Enable(&a.Local.Enabled, req.Skills...); err != nil {
			return err
		}
	}
	return a.saveAndSync()
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
	a.Local.Known = a.Catalog.HasSkill
	if err := edit(a.Local); err != nil {
		return err
	}
	return a.saveAndSync()
}

// Remove deletes skills from the local catalog. "@pack" deletes the pack
// together with its skills. Entries of included gists cannot be removed.
// Clones of sources the catalog does not list are deleted.
func (a *App) Remove(names ...string) (SyncReport, error) {
	var skills []string
	for _, name := range names {
		pack, isPack := strings.CutPrefix(name, "@")
		switch {
		case isPack && a.Local.Packs[pack] != nil:
			for _, skill := range a.Local.Packs[pack].Skills {
				if a.Local.HasSkill(skill) {
					skills = append(skills, skill)
				}
			}
		case !isPack && a.Local.HasSkill(name):
			skills = append(skills, name)
		case isPack && a.Catalog.Packs[pack] != nil:
			return SyncReport{}, fmt.Errorf("pack %q comes from gist %s and cannot be removed here", pack, a.Catalog.PackOrigin[pack])
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

// Run enables a set in the project for as long as a command runs.
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
	if err := session.Write(scope.Root, pid, set); err != nil {
		return 1, err
	}
	defer func() {
		session.Remove(scope.Root, pid)
		a.Sync(scope, SyncOptions{})
	}()
	if _, err := a.Sync(scope, SyncOptions{}); err != nil {
		return 1, err
	}
	return session.Run(argv)
}
