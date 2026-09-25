package app

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/importer"
	"github.com/bonkey/skillet/internal/mcp"
	"github.com/bonkey/skillet/internal/paths"
	"github.com/bonkey/skillet/internal/secrets"
	"github.com/bonkey/skillet/internal/source"
)

// AddRequest names what Add puts into the catalog.
type AddRequest struct {
	Arg     string   // a source, or the URL of a server
	Command []string // a `skills add` command, or the command that starts a server
	Skills  []string // the skills to take; none for every skill of the source
	Name    string   // the server's name; empty for the one its command or URL gives
	MCP     bool     // a server, without telling it from a source
}

// AddReport tells what Add found and wrote. A whole source is written
// "skills:source", a skill "skill@source" and a server "mcp:name".
type AddReport struct {
	File    string   // the config file written; empty when nothing changed
	Added   []string // what the config file holds now
	Present []string // what the catalog held already
	Secrets []string // placeholders whose values went into secrets.toml
	Notes   []string
	cloned  []string // the clones Add made, removed again when it fails
}

// Add puts skill sources or a server into the scope's config file, keeping
// the file's text (see catalog.Amend). It links nothing and writes no agent
// config; the next sync enables what it added.
// A source is cloned to check the skills it offers. Unless MCP is set, a URL
// is a source when git can read it and a server when an MCP server answers
// there, and a command other than `skills add` starts a server.
func (a *App) Add(scope Scope, req AddRequest) (AddReport, error) {
	var report AddReport
	var specs []source.Spec
	skills, server := req.Skills, req.MCP
	switch {
	case req.Arg == "" && len(req.Command) == 0:
		return report, errors.New("name a source or a server's URL, or give a command after --")
	case server:
	case len(req.Command) > 0:
		cmd, ok, err := importer.ParseCommand(req.Command)
		if err != nil {
			return report, err
		}
		server = !ok
		for _, arg := range cmd.Sources {
			spec, err := source.ParseSpec(arg)
			if err != nil {
				return report, err
			}
			if !spec.Repo && !source.IsRepo(spec.URL) {
				return report, fmt.Errorf("%s is not a git repository", arg)
			}
			specs = append(specs, spec)
		}
		skills = append(slices.Clone(skills), cmd.Skills...)
	default:
		spec, err := source.ParseSpec(req.Arg)
		if err != nil {
			return report, err
		}
		web := strings.HasPrefix(req.Arg, "http://") || strings.HasPrefix(req.Arg, "https://")
		switch {
		case spec.Repo || source.IsRepo(spec.URL):
			specs = append(specs, spec)
		case !web:
			return report, fmt.Errorf("%s is not a git repository", req.Arg)
		case mcp.Answers(req.Arg):
			server = true
		default:
			return report, fmt.Errorf("%s is neither a git repository nor an MCP server; `skillet add mcp %[1]s` adds it as a server anyway", req.Arg)
		}
	}
	if server {
		return a.addServer(scope, req)
	}
	if req.Name != "" {
		return report, errors.New("--name names a server; a source goes by the name of its repository")
	}

	target, file := a.Local, a.Paths.ConfigFile()
	if scope.Project {
		target, file = a.Project, filepath.Join(scope.Root, paths.ManifestName)
		if target == nil {
			target = catalog.New()
		}
	}
	fail := func(err error) (AddReport, error) {
		for _, dir := range report.cloned {
			os.RemoveAll(dir)
		}
		return AddReport{}, err
	}
	for _, spec := range specs {
		if err := a.addSource(scope, target, spec, skills, &report); err != nil {
			return fail(err)
		}
	}
	if len(report.Added) == 0 {
		return report, nil
	}
	err := target.Amend(file)
	if err == nil && scope.Project {
		a.Project = target
		err = a.merge()
	} else if err == nil {
		err = a.reload(false)
	}
	if err != nil {
		return fail(err)
	}
	report.File = file
	return report, a.Reindex()
}

// addSource records the skills of a source in target, the catalog of the
// scope's config file. A clone of the repository is used to check what the
// source offers: one the catalog has, or else a new one, which becomes the
// source's clone. A path to one skill folder takes that skill from the whole
// repository; a path to a directory of skills becomes the source's path.
func (a *App) addSource(scope Scope, target *catalog.Catalog, spec source.Spec, skills []string, report *AddReport) error {
	dir := ""
	for _, name := range a.SourceNames() {
		clone := a.Paths.RepoDir(name)
		if _, err := os.Stat(clone); err == nil && source.SameRepo(a.Catalog.Sources[name].URL, spec.URL) {
			dir = clone
			break
		}
	}
	fresh := dir == ""
	if fresh {
		if err := os.MkdirAll(a.Paths.Data, 0o755); err != nil {
			return err
		}
		tmp, err := os.MkdirTemp(a.Paths.Data, "add-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		dir = filepath.Join(tmp, "clone")
		if err := source.Clone(spec.URL, spec.Ref, dir); err != nil {
			return err
		}
	}

	skills = slices.Clone(skills) // one list serves every source of a command
	sub := spec.Path
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(sub), "SKILL.md")); sub != "" && err == nil {
		one, err := source.Discover(dir, sub)
		if err != nil {
			return err
		}
		for name := range one {
			skills = append(skills, name)
		}
		sub = ""
	}
	if spec.Skill != "" {
		skills = append(skills, spec.Skill)
	}
	skills = slices.Compact(slices.Sorted(slices.Values(skills)))
	offered, err := source.Discover(dir, sub)
	if err != nil {
		return err
	}
	where := spec.URL
	if sub != "" {
		where += " in " + sub
	}
	if len(offered) == 0 {
		return fmt.Errorf("%s offers no skills", where)
	}
	for _, skill := range skills {
		if _, ok := offered[skill]; !ok {
			return fmt.Errorf("%s has no skill %q; it offers %s", where, skill, strings.Join(sortedKeys(offered), ", "))
		}
	}

	same := func(src *catalog.Source) bool { return source.SameRepo(src.URL, spec.URL) && src.Path == sub }
	if a.Overlay != nil {
		for name, src := range a.Overlay.Sources {
			if same(src) {
				return fmt.Errorf("%s%s is defined in %s; add to it there", catalog.SourcePrefix, name, a.Paths.LocalConfigFile())
			}
		}
	}
	name, src := "", (*catalog.Source)(nil)
	for n, s := range target.Sources {
		if same(s) {
			name, src = n, s
		}
	}
	var added, present []string // skill names; "" stands for the whole source
	whole := []string{""}
	switch {
	case src == nil:
		src = &catalog.Source{URL: spec.URL, Ref: spec.Ref, Path: sub, Skills: skills}
		if err := a.nameSource(target, src); err != nil {
			return err
		}
		if name, err = target.AddSource(src); err != nil {
			return err
		}
		added = skills
		if len(skills) == 0 {
			added = whole
		}
	case len(skills) == 0 && src.TakesAll():
		present = whole
	case len(skills) == 0:
		src.TakeAll()
		added = whole
	default:
		for _, skill := range skills {
			if src.SkillOn(skill) && (src.TakesAll() || slices.Contains(src.Skills, skill)) {
				present = append(present, skill)
				continue
			}
			if err := target.SetSkillFlag(name, skill, true); err != nil {
				return err
			}
			added = append(added, skill)
		}
	}
	entry := func(skill string) string {
		if skill == "" {
			return catalog.SourcePrefix + name
		}
		return skill + "@" + name
	}
	for _, skill := range added {
		report.Added = append(report.Added, entry(skill))
	}
	for _, skill := range present {
		report.Present = append(report.Present, entry(skill))
	}
	if !src.On() {
		report.Notes = append(report.Notes, fmt.Sprintf("%s%s is switched off in its config file; `skillet enable --save %[1]s%[2]s` switches it on",
			catalog.SourcePrefix, name))
	}
	clone := a.Paths.RepoDir(name)
	if _, err := os.Stat(clone); os.IsNotExist(err) && fresh {
		if err := os.MkdirAll(a.Paths.ReposDir(), 0o755); err != nil {
			return err
		}
		if err := os.Rename(dir, clone); err != nil {
			return err
		}
		report.cloned = append(report.cloned, clone)
	}
	return nil
}

// nameSource gives a new source the name "owner-repo" when its repository
// name is taken, so that it renames no other source. The name of a source
// of the same repository and path elsewhere in the catalog is free: the
// config file's entry overrides that one.
func (a *App) nameSource(target *catalog.Catalog, src *catalog.Source) error {
	owner, repo, ok := catalog.RepoParts(src.URL)
	if !ok {
		return fmt.Errorf("source %q: no name found in the url", src.URL)
	}
	taken := func(name string) bool {
		if s, ok := a.Catalog.Sources[name]; ok && !(source.SameRepo(s.URL, src.URL) && s.Path == src.Path) {
			return true
		}
		for n, s := range target.Sources {
			if _, r, _ := catalog.RepoParts(s.URL); n == name || s.Name == "" && r == name {
				return true
			}
		}
		return false
	}
	names := []string{repo, owner + "-" + repo}
	if src.Path != "" {
		names = append(names, owner+"-"+repo+"-"+path.Base(src.Path))
	}
	for _, name := range names {
		if !taken(name) {
			if name != repo {
				src.Name = name
			}
			return nil
		}
	}
	return fmt.Errorf("the names %s are taken; add the source by hand with a name of its own", strings.Join(names, ", "))
}

// addServer records a server in config.toml, with the values of secret
// looking URL parameters moved to secrets.toml.
func (a *App) addServer(scope Scope, req AddRequest) (AddReport, error) {
	var report AddReport
	switch {
	case scope.Project:
		return report, errors.New("MCP servers are global; drop -p")
	case len(req.Skills) > 0:
		return report, errors.New("--skill takes skills; a server has none")
	case req.Arg != "" && len(req.Command) > 0:
		return report, errors.New("a server has a URL or a command, not both")
	}
	def := catalog.MCP{URL: req.Arg, Command: req.Command}
	guessed, err := def.GuessName()
	name := req.Name
	switch {
	case name == "" && err != nil:
		return report, fmt.Errorf("no name found in %s; give one with --name", strings.Join(append(req.Command, req.Arg), " "))
	case name == "":
		name = guessed
	case name != guessed:
		def.Name = name
	}
	if err := def.Validate(name); err != nil {
		return report, err
	}
	if a.Overlay != nil {
		if _, ok := a.Overlay.MCPs[name]; ok {
			return report, fmt.Errorf("%s%s is defined in %s; add it there", catalog.MCPPrefix, name, a.Paths.LocalConfigFile())
		}
	}
	store, err := secrets.Load(a.Paths.SecretsFile())
	if err != nil {
		return report, err
	}
	hidden := map[string]bool{}
	def = hideSecrets(name, def, store, hidden)
	for _, held := range a.Catalog.MCPNames() {
		if def.URL == a.Catalog.MCPs[held].URL && slices.Equal(def.Command, a.Catalog.MCPs[held].Command) {
			report.Present = []string{catalog.MCPPrefix + held}
			return report, nil
		}
	}
	if _, ok := a.Catalog.MCPs[name]; ok {
		return report, fmt.Errorf("%s%s is in the catalog already for another server; give this one a name with --name", catalog.MCPPrefix, name)
	}
	if len(hidden) > 0 {
		if err := os.MkdirAll(a.Paths.Config, 0o755); err != nil {
			return report, err
		}
		if err := store.Amend(a.Paths.SecretsFile()); err != nil {
			return report, err
		}
		report.Secrets = sortedKeys(hidden)
	}
	a.Local.MCPs[name] = &def
	if err := a.Local.Amend(a.Paths.ConfigFile()); err != nil {
		return report, err
	}
	if err := a.reload(false); err != nil {
		return report, err
	}
	report.File, report.Added = a.Paths.ConfigFile(), []string{catalog.MCPPrefix + name}
	return report, nil
}
