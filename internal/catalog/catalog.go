// Package catalog holds the declarative state: which skills are known, how
// they are grouped into packs, and which of them a scope has enabled.
package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Source struct {
	URL    string   `yaml:"url"`
	Ref    string   `yaml:"ref,omitempty"`
	Skills []string `yaml:"skills"`
}

type Pack struct {
	Description string   `yaml:"description"`
	Skills      []string `yaml:"skills"`
}

// Set is the enabled state of one scope.
type Set struct {
	Packs  []string `yaml:"packs,omitempty"`
	Skills []string `yaml:"skills,omitempty"`
	Except []string `yaml:"except,omitempty"`

	// Inherited lists skills that included catalogs enable. It is never
	// saved; Except switches such a skill off.
	Inherited []string `yaml:"-"`
}

type Catalog struct {
	// Gist is the gist this catalog is pushed to and pulled from.
	Gist string `yaml:"gist,omitempty"`
	// Includes lists gists whose catalogs are merged into this one.
	Includes []string           `yaml:"includes,omitempty"`
	Agents   []string           `yaml:"agents"`
	Sources  map[string]*Source `yaml:"sources"`
	Packs    map[string]*Pack   `yaml:"packs"`
	Enabled  Set                `yaml:"enabled"`

	// Known widens the skills a pack may hold beyond those of this
	// catalog, for packs that group skills of included catalogs.
	Known func(skill string) bool `yaml:"-"`
	// SkillOrigin and PackOrigin name the gist an entry of a merged catalog
	// was included from. Entries of the local catalog are absent.
	SkillOrigin map[string]string `yaml:"-"`
	PackOrigin  map[string]string `yaml:"-"`
}

func New() *Catalog {
	return &Catalog{
		Agents:  []string{"claude-code"},
		Sources: map[string]*Source{},
		Packs:   map[string]*Pack{},
	}
}

func Load(file string) (*Catalog, error) {
	c := New()
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if c.Sources == nil {
		c.Sources = map[string]*Source{}
	}
	if c.Packs == nil {
		c.Packs = map[string]*Pack{}
	}
	return c, nil
}

func (c *Catalog) Save(file string) error { return writeYAML(file, c) }

func LoadSet(file string) (Set, error) {
	var s Set
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := yaml.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("%s: %w", file, err)
	}
	return s, nil
}

func (s Set) Save(file string) error { return writeYAML(file, s) }

func (s Set) Empty() bool { return len(s.Packs)+len(s.Skills)+len(s.Except) == 0 }

func writeYAML(file string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// SourceOf names the source a skill comes from.
func (c *Catalog) SourceOf(skill string) (string, bool) {
	for name, src := range c.Sources {
		if slices.Contains(src.Skills, skill) {
			return name, true
		}
	}
	return "", false
}

func (c *Catalog) HasSkill(skill string) bool {
	_, ok := c.SourceOf(skill)
	return ok
}

func (c *Catalog) SkillNames() []string {
	var names []string
	for _, src := range c.Sources {
		names = append(names, src.Skills...)
	}
	sort.Strings(names)
	return names
}

func (c *Catalog) PackNames() []string {
	names := make([]string, 0, len(c.Packs))
	for name := range c.Packs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// PacksOf lists the packs a skill belongs to.
func (c *Catalog) PacksOf(skill string) []string {
	var packs []string
	for _, name := range c.PackNames() {
		if slices.Contains(c.Packs[name].Skills, skill) {
			packs = append(packs, name)
		}
	}
	return packs
}

// Resolve expands a set to the sorted skills it enables. Names the catalog
// does not know are dropped.
func (c *Catalog) Resolve(s Set) []string {
	on := map[string]bool{}
	for _, pack := range s.Packs {
		if p, ok := c.Packs[pack]; ok {
			for _, skill := range p.Skills {
				on[skill] = true
			}
		}
	}
	for _, skill := range s.Skills {
		on[skill] = true
	}
	for _, skill := range s.Inherited {
		on[skill] = true
	}
	for _, skill := range s.Except {
		delete(on, skill)
	}
	var out []string
	for skill := range on {
		if c.HasSkill(skill) {
			out = append(out, skill)
		}
	}
	sort.Strings(out)
	return out
}

// Enable switches on skills and packs ("@name") in a set.
func (c *Catalog) Enable(s *Set, names ...string) error {
	if err := c.check(names); err != nil {
		return err
	}
	for _, name := range names {
		if pack, ok := strings.CutPrefix(name, "@"); ok {
			s.Packs = add(s.Packs, pack)
			for _, skill := range c.Packs[pack].Skills {
				s.Except = remove(s.Except, skill)
			}
			continue
		}
		s.Except = remove(s.Except, name)
		if !slices.Contains(c.Resolve(*s), name) {
			s.Skills = add(s.Skills, name)
		}
	}
	return nil
}

// Disable switches off skills and packs ("@name") in a set. A skill that a
// still-enabled pack provides becomes an exception.
func (c *Catalog) Disable(s *Set, names ...string) error {
	if err := c.check(names); err != nil {
		return err
	}
	for _, name := range names {
		if pack, ok := strings.CutPrefix(name, "@"); ok {
			s.Packs = remove(s.Packs, pack)
			for _, skill := range c.Packs[pack].Skills {
				c.disableSkill(s, skill)
			}
			continue
		}
		c.disableSkill(s, name)
	}
	return nil
}

func (c *Catalog) disableSkill(s *Set, skill string) {
	s.Skills = remove(s.Skills, skill)
	s.Except = remove(s.Except, skill)
	if slices.Contains(c.Resolve(*s), skill) {
		s.Except = add(s.Except, skill)
	}
}

func (c *Catalog) check(names []string) error {
	for _, name := range names {
		if pack, ok := strings.CutPrefix(name, "@"); ok {
			if _, ok := c.Packs[pack]; !ok {
				return fmt.Errorf("unknown pack %q", pack)
			}
		} else if !c.HasSkill(name) {
			return fmt.Errorf("unknown skill %q", name)
		}
	}
	return nil
}

// AddSkills records skills under a source, creating the source when needed.
func (c *Catalog) AddSkills(source, url, ref string, skills []string) error {
	for _, skill := range skills {
		if owner, ok := c.SourceOf(skill); ok && owner != source {
			return fmt.Errorf("skill %q is already in the catalog from %s", skill, owner)
		}
	}
	src, ok := c.Sources[source]
	if !ok {
		src = &Source{URL: url, Ref: ref}
		c.Sources[source] = src
	}
	for _, skill := range skills {
		src.Skills = add(src.Skills, skill)
	}
	return nil
}

// RemoveSkill drops a skill from its source, all packs and the global set.
// A source left without skills is dropped too.
func (c *Catalog) RemoveSkill(skill string) {
	for name, src := range c.Sources {
		src.Skills = remove(src.Skills, skill)
		if len(src.Skills) == 0 {
			delete(c.Sources, name)
		}
	}
	for _, pack := range c.Packs {
		pack.Skills = remove(pack.Skills, skill)
	}
	c.Enabled.Skills = remove(c.Enabled.Skills, skill)
	c.Enabled.Except = remove(c.Enabled.Except, skill)
}

func (c *Catalog) CreatePack(name, description string, skills []string) error {
	if _, ok := c.Packs[name]; ok {
		return fmt.Errorf("pack %q already exists", name)
	}
	if strings.TrimSpace(description) == "" {
		return fmt.Errorf("pack %q needs a description", name)
	}
	c.Packs[name] = &Pack{Description: description}
	if err := c.PackAdd(name, skills); err != nil {
		delete(c.Packs, name)
		return err
	}
	return nil
}

func (c *Catalog) PackAdd(name string, skills []string) error {
	pack, ok := c.Packs[name]
	if !ok {
		return fmt.Errorf("unknown pack %q", name)
	}
	for _, skill := range skills {
		if !c.HasSkill(skill) && (c.Known == nil || !c.Known(skill)) {
			return fmt.Errorf("unknown skill %q", skill)
		}
	}
	for _, skill := range skills {
		pack.Skills = add(pack.Skills, skill)
	}
	return nil
}

func (c *Catalog) PackRemove(name string, skills []string) error {
	pack, ok := c.Packs[name]
	if !ok {
		return fmt.Errorf("unknown pack %q", name)
	}
	for _, skill := range skills {
		pack.Skills = remove(pack.Skills, skill)
	}
	return nil
}

// RemovePack deletes a pack. Its skills stay in the catalog.
func (c *Catalog) RemovePack(name string) {
	delete(c.Packs, name)
	c.Enabled.Packs = remove(c.Enabled.Packs, name)
}

func add(list []string, item string) []string {
	if slices.Contains(list, item) {
		return list
	}
	list = append(list, item)
	sort.Strings(list)
	return list
}

func remove(list []string, item string) []string {
	out := slices.DeleteFunc(slices.Clone(list), func(s string) bool { return s == item })
	if len(out) == 0 {
		return nil
	}
	return out
}

// Merge returns the catalog that results from including others in local, in
// the given order. The local catalog wins a name clash, then the earlier
// include: a skill keeps its first source, a source its first URL and ref,
// a pack its first definition. What the included catalogs enable becomes
// the inherited part of the merged enabled set.
func Merge(local *Catalog, ids []string, included []*Catalog) (*Catalog, error) {
	data, err := yaml.Marshal(local)
	if err != nil {
		return nil, err
	}
	merged := New()
	if err := yaml.Unmarshal(data, merged); err != nil {
		return nil, err
	}
	merged.SkillOrigin, merged.PackOrigin = map[string]string{}, map[string]string{}
	for i, inc := range included {
		for _, name := range sortedKeys(inc.Sources) {
			src := inc.Sources[name]
			for _, skill := range src.Skills {
				if merged.HasSkill(skill) {
					continue
				}
				if _, ok := merged.Sources[name]; !ok {
					merged.Sources[name] = &Source{URL: src.URL, Ref: src.Ref}
				}
				merged.Sources[name].Skills = add(merged.Sources[name].Skills, skill)
				merged.SkillOrigin[skill] = ids[i]
			}
		}
		for _, name := range sortedKeys(inc.Packs) {
			if _, ok := merged.Packs[name]; !ok {
				pack := inc.Packs[name]
				merged.Packs[name] = &Pack{Description: pack.Description, Skills: slices.Clone(pack.Skills)}
				merged.PackOrigin[name] = ids[i]
			}
		}
	}
	for _, inc := range included {
		set := inc.Enabled
		set.Inherited = nil
		for _, skill := range merged.Resolve(set) {
			merged.Enabled.Inherited = add(merged.Enabled.Inherited, skill)
		}
	}
	return merged, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
