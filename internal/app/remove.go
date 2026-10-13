package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/paths"
	"github.com/bonkey/skillet/internal/source"
)

// RemoveReport tells what Remove took out of the config file.
type RemoveReport struct {
	File    string   // the config file written
	Removed []string // "skills:source" and "mcp:name"
	Notes   []string
	Sync    *SyncReport // the disabling of what was removed; nil without sync
}

// Remove takes whole sources and servers out of the scope's config file,
// keeping the file's text (see catalog.Amend). An argument is "skills:name",
// "mcp:name", the name of a source or a server, a skill that is the only
// one its source takes, or the URL, owner/repo or path of a source or the
// URL of a server. What was removed stays enabled unless sync is set, which
// disables it in the scope as `disable` does.
func (a *App) Remove(scope Scope, sync bool, args ...string) (RemoveReport, error) {
	var report RemoveReport
	if len(args) == 0 {
		return report, errors.New("name a source or a server, or give its URL")
	}
	target, file := a.Local, a.Paths.ConfigFile()
	if scope.Project {
		target, file = a.Project, a.configOf(scope)
	}
	for _, arg := range args {
		entry, err := a.removal(arg)
		if err == nil {
			err = a.removable(scope, target, entry)
		}
		if err != nil {
			return report, err
		}
		if !slices.Contains(report.Removed, entry) {
			report.Removed = append(report.Removed, entry)
		}
	}
	trial, err := copyOf(target)
	if err != nil {
		return report, err
	}
	for _, entry := range report.Removed {
		var packs []string
		if server, ok := strings.CutPrefix(entry, catalog.MCPPrefix); ok {
			delete(trial.MCPs, server)
			packs = a.packsOf(server, true)
		} else {
			name := strings.TrimPrefix(entry, catalog.SourcePrefix)
			delete(trial.Sources, name)
			for _, skill := range a.Catalog.Sources[name].Skills {
				packs = append(packs, a.packsOf(skill, false)...)
			}
		}
		if packs = slices.Compact(slices.Sorted(slices.Values(packs))); len(packs) > 0 {
			report.Notes = append(report.Notes, fmt.Sprintf("%s is still named in %s%s; `skillet edit` takes it out",
				entry, catalog.PackPrefix, strings.Join(packs, ", "+catalog.PackPrefix)))
		}
	}
	if err := trial.Amend(file); err != nil {
		return report, err
	}
	report.File = file
	if sync {
		// The catalog in memory still knows what was removed.
		set, err := a.Set(scope)
		if err == nil {
			err = a.Catalog.Disable(&set, report.Removed...)
		}
		if err != nil {
			return report, err
		}
		disabled, err := a.apply(scope, a.enablingOf(set), SyncOptions{})
		report.Sync = &disabled
		if err != nil {
			return report, err
		}
	}
	if scope.Project {
		a.Project = trial
		err = a.merge()
	} else {
		a.Local = trial
		err = a.reload(false)
	}
	if err != nil {
		return report, err
	}
	return report, a.Reindex()
}

// removal names the source or server an argument of Remove stands for.
func (a *App) removal(arg string) (string, error) {
	if strings.HasPrefix(arg, catalog.MCPPrefix) || catalog.IsSource(arg) {
		return arg, nil
	}
	var found []string
	if strings.ContainsAny(arg, "/:") {
		if spec, err := source.ParseSpec(arg); err == nil {
			for _, name := range a.SourceNames() {
				if source.SameRepo(a.Catalog.Sources[name].URL, spec.URL) {
					if spec.Skill != "" {
						return a.removal(spec.Skill + "@" + name)
					}
					found = append(found, catalog.SourcePrefix+name)
				}
			}
		}
		if len(found) == 0 && (strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://")) {
			// A URL in the catalog may hold a ${NAME} placeholder where arg
			// holds the secret; the name guessed from the URL finds it then.
			guess, _ := (&catalog.MCP{URL: arg}).GuessName()
			for _, name := range a.Catalog.MCPNames() {
				if a.Catalog.MCPs[name].URL == arg || name == guess {
					found = append(found, catalog.MCPPrefix+name)
				}
			}
		}
		if len(found) == 0 {
			// The query of a URL may hold a secret.
			shown, _, _ := strings.Cut(arg, "?")
			return "", fmt.Errorf("no source or server in the catalog has the URL %s", shown)
		}
	} else {
		if _, ok := a.Catalog.MCPs[arg]; ok {
			found = append(found, catalog.MCPPrefix+arg)
		}
		if _, ok := a.Catalog.Sources[arg]; ok {
			found = append(found, catalog.SourcePrefix+arg)
		}
		skill, owner := catalog.SplitRef(arg)
		if from, ok := a.Catalog.SourceOf(skill); ok && (owner == "" || owner == from) {
			src := a.Catalog.Sources[from]
			if len(src.Skills) > 1 || len(src.Disabled) > 0 {
				return "", fmt.Errorf("%s comes from %s%s, which takes other skills too; `skillet remove %[2]s%[3]s` removes the source, `skillet disable --save %[1]s` switches the skill off",
					skill, catalog.SourcePrefix, from)
			}
			if entry := catalog.SourcePrefix + from; !slices.Contains(found, entry) {
				found = append(found, entry)
			}
		}
		if len(found) == 0 {
			return "", fmt.Errorf("no skill, source or server is named %q", arg)
		}
	}
	if len(found) > 1 {
		return "", fmt.Errorf("%s stands for %s; name one of them", arg, strings.Join(found, " and "))
	}
	return found[0], nil
}

// removable checks that the scope's config file defines an entry and that
// config.local.toml does not name it.
func (a *App) removable(scope Scope, target *catalog.Catalog, entry string) error {
	server, isServer := strings.CutPrefix(entry, catalog.MCPPrefix)
	name := strings.TrimPrefix(entry, catalog.SourcePrefix)
	defined := false
	if target != nil && isServer {
		_, defined = target.MCPs[server]
	} else if target != nil {
		_, defined = target.Sources[name]
	}
	if !defined {
		_, isKnown := a.Catalog.Sources[name]
		if isServer {
			_, isKnown = a.Catalog.MCPs[server]
		}
		local := a.Overlay != nil && (a.Overlay.Sources[name] != nil || isServer && a.Overlay.MCPs[server] != nil)
		switch origin := a.originOf(entry); {
		case local:
			return fmt.Errorf("%s is defined in %s; remove it there", entry, a.Paths.LocalConfigFile())
		case !isKnown:
			return fmt.Errorf("%s is not in the catalog", entry)
		case origin != "" && origin != paths.ManifestName:
			return fmt.Errorf("%s comes from gist %s; remove it in that catalog", entry, origin)
		case scope.Project:
			return fmt.Errorf("%s is not defined in %s; drop -p", entry, a.configOf(scope))
		default:
			return fmt.Errorf("%s is defined in the project's %s; use -p", entry, paths.ManifestName)
		}
	}
	if a.Overlay == nil {
		return nil
	}
	for _, listed := range append(slices.Clone(a.Overlay.Enabled), a.Overlay.Disabled...) {
		owner := "" // the source of a listed skill
		if !strings.HasPrefix(listed, catalog.PackPrefix) && !strings.HasPrefix(listed, catalog.MCPPrefix) && !catalog.IsSource(listed) {
			skill, from := catalog.SplitRef(listed)
			if from == "" {
				from, _ = a.Catalog.SourceOf(skill)
			}
			owner = from
		}
		if listed == entry || !isServer && owner == name {
			return fmt.Errorf("%s names %s; take it out there first", a.Paths.LocalConfigFile(), listed)
		}
	}
	return nil
}
