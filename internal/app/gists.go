package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/pelletier/go-toml/v2"

	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/gist"
	"github.com/bonkey/skillet/internal/paths"
)

// Included is one gist that takes part in the merged catalog.
type Included struct {
	ID      string
	Parent  string // the gist that includes it, "" for the local catalog
	Catalog *catalog.Catalog
	Skipped []string // includes of this gist that were already visited
}

func (a *App) gistCache(id string) string {
	return filepath.Join(a.Paths.Data, "gists", id+".toml")
}

// reload walks the includes of the local catalog depth first and rebuilds
// the merged catalog. Every gist takes part once: a gist met again, the
// catalog's own gist included, is skipped, which also ends every cycle.
// Cached copies are used unless refresh is set or the cache is missing.
func (a *App) reload(refresh bool) error {
	a.Included, a.Warnings = nil, nil
	visited := map[string]bool{}
	if own, err := gist.ID(a.Local.Gist); err == nil {
		visited[own] = true
	}
	var skippedAtRoot []string
	var walk func(refs []string, parent string, skipped *[]string)
	walk = func(refs []string, parent string, skipped *[]string) {
		for _, ref := range refs {
			id, err := gist.ID(ref)
			if err != nil {
				a.Warnings = append(a.Warnings, err.Error())
				continue
			}
			if visited[id] {
				*skipped = append(*skipped, id)
				continue
			}
			visited[id] = true
			c, err := a.readGist(id, refresh)
			if err != nil {
				a.Warnings = append(a.Warnings, fmt.Sprintf("gist %s: %v", id, err))
				continue
			}
			a.Included = append(a.Included, Included{ID: id, Parent: parent, Catalog: c})
			at := len(a.Included) - 1
			var nested []string
			walk(c.Includes, id, &nested)
			a.Included[at].Skipped = nested
		}
	}
	walk(a.Local.Includes, "", &skippedAtRoot)
	return a.merge()
}

func (a *App) merge() error {
	ids := make([]string, len(a.Included))
	catalogs := make([]*catalog.Catalog, len(a.Included))
	for i, inc := range a.Included {
		ids[i], catalogs[i] = inc.ID, inc.Catalog
	}
	if a.Project != nil {
		// The manifest enables for its project only.
		project := *a.Project
		project.Enabled = catalog.Set{}
		ids, catalogs = append(ids, paths.ManifestName), append(catalogs, &project)
	}
	merged, err := catalog.Merge(a.Local, ids, catalogs)
	a.Catalog = merged
	if err == nil {
		if a.Agents != nil {
			merged.Agents = a.Agents
		}
		a.expand()
	}
	return err
}

// readGist returns the catalog of a gist. A failed download falls back to
// the cached copy.
func (a *App) readGist(id string, refresh bool) (*catalog.Catalog, error) {
	file := a.gistCache(id)
	if _, err := os.Stat(file); refresh || err != nil {
		content, err := a.Gists.Read(id)
		if err == nil {
			if _, err = parse(content); err == nil {
				if err = os.MkdirAll(filepath.Dir(file), 0o755); err == nil {
					err = os.WriteFile(file, []byte(content), 0o644)
				}
			}
		}
		if err != nil {
			if _, statErr := os.Stat(file); statErr != nil {
				return nil, err
			}
			a.Warnings = append(a.Warnings, fmt.Sprintf("gist %s: using the cached copy: %v", id, err))
		}
	}
	return catalog.Load(file)
}

func parse(content string) (*catalog.Catalog, error) {
	c := catalog.New()
	err := toml.Unmarshal([]byte(content), c)
	return c, err
}

// Push uploads the local catalog to its gist. Without one, or when fresh is
// set, a new gist is created and remembered.
func (a *App) Push(fresh, public bool) (id string, created bool, err error) {
	id, _ = gist.ID(a.Local.Gist)
	if fresh || id == "" {
		// The gist must name itself so that a pull knows where it came from.
		if id, err = a.Gists.Create("agents = []\n", public); err != nil {
			return "", false, err
		}
		a.Local.Gist, created = id, true
		if err := a.Save(); err != nil {
			return id, created, err
		}
	}
	data, err := os.ReadFile(a.Paths.ConfigFile())
	if err != nil {
		return id, created, err
	}
	return id, created, a.Gists.Update(id, string(data))
}

// Pull replaces the local catalog with the one in a gist ("" for the
// catalog's own gist), keeps the previous file as config.toml.bak, fetches
// what is missing and syncs the global links.
func (a *App) Pull(ref string) (string, error) {
	if ref == "" {
		ref = a.Local.Gist
	}
	if ref == "" {
		return "", errors.New("this catalog has no gist yet; name the gist to pull")
	}
	id, err := gist.ID(ref)
	if err != nil {
		return "", err
	}
	content, err := a.Gists.Read(id)
	if err != nil {
		return id, err
	}
	pulled, err := parse(content)
	if err != nil {
		return id, fmt.Errorf("gist %s: %w", id, err)
	}
	if old, err := os.ReadFile(a.Paths.ConfigFile()); err == nil {
		if err := os.WriteFile(a.Paths.ConfigFile()+".bak", old, 0o644); err != nil {
			return id, err
		}
	}
	before := a.SourceNames()
	a.Local = pulled
	if err := a.Save(); err != nil {
		return id, err
	}
	for _, name := range before {
		a.DropUnusedClone(name)
	}
	_, err = a.Update(false)
	return id, err
}

// Include adds a gist to the catalog's includes and fetches its sources.
func (a *App) Include(ref string) (string, error) {
	id, err := gist.ID(ref)
	if err != nil {
		return "", err
	}
	if own, _ := gist.ID(a.Local.Gist); own == id {
		return id, errors.New("a catalog cannot include its own gist")
	}
	for _, existing := range a.Local.Includes {
		if other, _ := gist.ID(existing); other == id {
			return id, fmt.Errorf("gist %s is already included", id)
		}
	}
	if _, err := a.readGist(id, true); err != nil {
		return id, err
	}
	a.Local.Includes = append(a.Local.Includes, id)
	if err := a.Save(); err != nil {
		return id, err
	}
	_, err = a.Update(false)
	return id, err
}

// Exclude stops including a gist. Clones that only it needed are deleted.
func (a *App) Exclude(ref string) (string, error) {
	id, err := gist.ID(ref)
	if err != nil {
		return "", err
	}
	kept := slices.DeleteFunc(slices.Clone(a.Local.Includes), func(existing string) bool {
		other, _ := gist.ID(existing)
		return other == id
	})
	if len(kept) == len(a.Local.Includes) {
		return id, fmt.Errorf("gist %s is not included", id)
	}
	before := a.SourceNames()
	a.Local.Includes = kept
	if err := a.Save(); err != nil {
		return id, err
	}
	os.Remove(a.gistCache(id))
	for _, name := range before {
		a.DropUnusedClone(name)
	}
	return id, a.saveAndSync()
}
