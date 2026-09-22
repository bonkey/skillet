package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/importer"
	"github.com/bonkey/skillet/internal/source"
)

type ImportReport struct {
	Imported []string          // catalog skill names
	Renamed  map[string]string // lock name to catalog name, where they differ
	Skipped  map[string]string // lock name to reason
	Sync     SyncReport
}

// LegacyLock is the global lock file of the `skills` npm CLI.
func (a *App) LegacyLock() string { return filepath.Join(a.Paths.Home, ".agents", ".skill-lock.json") }

// Import seeds the catalog from a `skills` CLI lock file. Only the
// definitions come from the lock; the content of every source is fetched
// fresh. The locked skills are added, put in one pack per source and enabled
// globally, and the folders the `skills` CLI installed in ~/.agents/skills
// are deleted: links into the clones take their place where a configured
// agent reads that directory. A dry run fetches too, but writes neither the
// catalog nor any link.
func (a *App) Import(lockFile string, dryRun bool) (ImportReport, error) {
	report := ImportReport{Renamed: map[string]string{}, Skipped: map[string]string{}}
	entries, skipped, err := importer.Read(lockFile)
	if err != nil {
		return report, err
	}
	for _, name := range skipped {
		report.Skipped[name] = "not a git source"
	}

	bySource := map[string][]importer.Entry{}
	for _, entry := range entries {
		bySource[entry.Source] = append(bySource[entry.Source], entry)
	}
	sources := make([]string, 0, len(bySource))
	for name := range bySource {
		sources = append(sources, name)
	}
	sort.Strings(sources)

	// The clones go where the sources will live once they are all in the
	// catalog; a new repository name can rename an existing source too.
	before := a.SourceNames()
	entriesToName := make([]*catalog.Source, len(sources))
	for i, name := range sources {
		first := bySource[name][0]
		entriesToName[i] = &catalog.Source{URL: first.URL, Ref: first.Ref}
	}
	names, err := a.Local.NamesFor(entriesToName)
	if err != nil {
		return report, err
	}
	failed := make([]error, len(sources))
	parallel(len(sources), func(i int) {
		first := bySource[sources[i]][0]
		_, failed[i] = fetchLatest(first.URL, first.Ref, a.Paths.RepoDir(names[i]), false)
	})

	for i, name := range sources {
		group := bySource[name]
		if failed[i] != nil {
			for _, entry := range group {
				report.Skipped[entry.Name] = failed[i].Error()
			}
			continue
		}
		found, err := source.Discover(a.Paths.RepoDir(names[i]))
		if err != nil {
			return report, err
		}
		byPath := map[string]string{}
		for skill, info := range found {
			byPath[info.Path] = skill
		}
		var added []string
		for _, entry := range group {
			skill, ok := byPath[entry.Path]
			if _, sameName := found[entry.Name]; !ok && sameName {
				skill, ok = entry.Name, true
			}
			if !ok {
				report.Skipped[entry.Name] = fmt.Sprintf("no skill at %s in %s", entry.Path, name)
				continue
			}
			if _, err := a.Local.AddSkills(entry.URL, entry.Ref, []string{skill}); err != nil {
				report.Skipped[entry.Name] = err.Error()
				continue
			}
			if skill != entry.Name {
				report.Renamed[entry.Name] = skill
			}
			added = append(added, skill)
		}
		if len(added) == 0 {
			continue
		}
		pack := importer.PackName(name)
		if _, ok := a.Local.Packs[pack]; !ok {
			if err := a.Local.CreatePack(pack, "Skills imported from "+name, nil); err != nil {
				return report, err
			}
		}
		if err := a.Local.PackAdd(pack, added); err != nil {
			return report, err
		}
		report.Imported = append(report.Imported, added...)
	}
	sort.Strings(report.Imported)

	if dryRun {
		err = a.merge()
	} else {
		err = a.Save()
	}
	if err != nil {
		return report, err
	}
	if !dryRun {
		// A source renamed by the import, or one whose skills were all
		// skipped, leaves a clone under a name the catalog no longer holds.
		for _, name := range append(before, names...) {
			a.DropUnusedClone(name)
		}
	}
	if err := a.Reindex(); err != nil {
		return report, err
	}
	report.Sync, err = a.Sync(a.Global(), SyncOptions{
		DryRun:  dryRun,
		Replace: func(name string) bool { return slices.Contains(report.Imported, name) },
	})
	if err != nil || dryRun {
		return report, err
	}
	for _, name := range report.Imported {
		installed := filepath.Join(a.Paths.Home, ".agents", "skills", name)
		if info, statErr := os.Lstat(installed); statErr == nil && info.IsDir() {
			if err := os.RemoveAll(installed); err != nil {
				return report, err
			}
		}
	}
	return report, nil
}
