package source

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/paths"
)

// Index caches what Discover found in each clone. It is rebuilt per source
// whenever the clone's HEAD or the source's path differs from the cached one.
type Index struct {
	Sources map[string]*SourceIndex `json:"sources"`
	paths   paths.Paths
}

type SourceIndex struct {
	Head   string           `json:"head"`
	Path   string           `json:"path,omitempty"` // the source's path key when it was indexed
	Skills map[string]Skill `json:"skills"`
}

// Resolved is a catalog skill located on disk.
type Resolved struct {
	Skill
	Source string
	Dir    string // absolute skill folder inside the clone
}

// LoadIndex returns an index that matches the current clones of the
// catalog's sources, refreshing and saving the cache where needed.
func LoadIndex(p paths.Paths, c *catalog.Catalog) (*Index, error) {
	idx := &Index{Sources: map[string]*SourceIndex{}, paths: p}
	if data, err := os.ReadFile(p.IndexFile()); err == nil {
		if json.Unmarshal(data, idx) != nil || idx.Sources == nil {
			idx.Sources = map[string]*SourceIndex{}
		}
	}
	dirty := false
	for name := range idx.Sources {
		if _, ok := c.Sources[name]; !ok {
			delete(idx.Sources, name)
			dirty = true
		}
	}
	for name, src := range c.Sources {
		dir := p.RepoDir(name)
		head, err := Head(dir)
		if err != nil {
			if _, ok := idx.Sources[name]; ok {
				delete(idx.Sources, name)
				dirty = true
			}
			continue
		}
		if cached, ok := idx.Sources[name]; ok && cached.Head == head && cached.Path == src.Path {
			continue
		}
		skills, err := Discover(dir, src.Path)
		if err != nil {
			return nil, err
		}
		idx.Sources[name] = &SourceIndex{Head: head, Path: src.Path, Skills: skills}
		dirty = true
	}
	if dirty {
		if err := idx.save(); err != nil {
			return nil, err
		}
	}
	return idx, nil
}

func (idx *Index) save() error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	file := idx.paths.IndexFile()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o644)
}

// Lookup locates a catalog skill. It fails when the source is not cloned or
// its clone does not contain the skill.
func (idx *Index) Lookup(c *catalog.Catalog, skill string) (Resolved, bool) {
	name, ok := c.SourceOf(skill)
	if !ok {
		return Resolved{}, false
	}
	src, ok := idx.Sources[name]
	if !ok {
		return Resolved{}, false
	}
	found, ok := src.Skills[skill]
	if !ok {
		return Resolved{}, false
	}
	dir := filepath.Join(idx.paths.RepoDir(name), filepath.FromSlash(found.Path))
	return Resolved{Skill: found, Source: name, Dir: dir}, true
}
