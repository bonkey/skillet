// Package catalog holds the declarative state: which skills are known, how
// they are grouped into packs, and which of them a scope has enabled.
package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Source struct {
	URL string `toml:"url"`
	Ref string `toml:"ref,omitempty"`
	// Skills names the skills taken from the source. Without any, the
	// source takes all the skills it offers.
	Skills []string `toml:"skills,omitempty"`
	// All marks, in a merged catalog, a source that takes all skills. Its
	// Skills then hold what ExpandAll found on offer.
	All bool `toml:"-"`
}

type Pack struct {
	Description string `toml:"description"`
	// Sources puts every catalog skill of these sources into the pack.
	Sources []string `toml:"sources,omitempty"`
	Skills  []string `toml:"skills,omitempty"`
	MCPs    []string `toml:"mcps,omitempty"`
}

// Set names what to enable: what a config file declares for its scope, what
// the links of a scope hold, or what a `run` session adds.
type Set struct {
	Packs  []string `toml:"packs,omitempty"`
	Skills []string `toml:"skills,omitempty"`
	MCPs   []string `toml:"mcps,omitempty"`
	// Except switches off single skills, and servers written "mcp:name",
	// that a pack or an included catalog enables.
	Except []string `toml:"except,omitempty"`

	// Inherited and InheritedMCPs list what included catalogs enable. They
	// are never written to a file.
	Inherited     []string `toml:"-"`
	InheritedMCPs []string `toml:"-"`
}

type Catalog struct {
	// Gist is the gist this catalog is pushed to and pulled from.
	Gist string `toml:"gist,omitempty"`
	// Includes lists gists whose catalogs are merged into this one.
	Includes []string `toml:"includes,omitempty"`
	// Secrets lists 1Password items whose fields are the values of ${NAME}
	// placeholders. Only the local catalog's list is used; an included
	// catalog never chooses where secrets come from.
	Secrets []SecretItem       `toml:"secrets,omitempty"`
	Agents  []string           `toml:"agents"`
	Sources map[string]*Source `toml:"sources"`
	MCPs    map[string]*MCP    `toml:"mcps,omitempty"`
	Packs   map[string]*Pack   `toml:"packs"`
	Enabled Set                `toml:"enabled"`

	// SkillOrigin, MCPOrigin and PackOrigin name the gist an entry of a
	// merged catalog was included from. Entries of the local catalog are absent.
	SkillOrigin map[string]string `toml:"-"`
	MCPOrigin   map[string]string `toml:"-"`
	PackOrigin  map[string]string `toml:"-"`
}

func New() *Catalog {
	return &Catalog{
		Agents:  []string{"claude-code"},
		Sources: map[string]*Source{},
		MCPs:    map[string]*MCP{},
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
	if err := toml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if c.Sources == nil {
		c.Sources = map[string]*Source{}
	}
	if c.Packs == nil {
		c.Packs = map[string]*Pack{}
	}
	if c.MCPs == nil {
		c.MCPs = map[string]*MCP{}
	}
	for _, name := range c.MCPNames() {
		if err := c.MCPs[name].Validate(name); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
	}
	return c, nil
}

func (c *Catalog) Save(file string) error { return writeTOML(file, c) }

var (
	// A table header that only introduces its sub-tables.
	parentHeader = regexp.MustCompile(`(?m)^\[([^\]\n]+)\]\n(\[([^\]\n]+)\]\n)`)
	nameList     = regexp.MustCompile(`(?m)^(skills|mcps|packs|except|sources) = \[(.*)\]$`)
)

// tidy makes the encoder's output pleasant to edit: parent tables without
// keys lose their header, and a long list of names gets one name per line.
func tidy(data []byte) []byte {
	text := string(data)
	for {
		next := parentHeader.ReplaceAllStringFunc(text, func(match string) string {
			m := parentHeader.FindStringSubmatch(match)
			if strings.HasPrefix(m[3], m[1]+".") {
				return m[2]
			}
			return match
		})
		if next == text {
			break
		}
		text = next
	}
	text = nameList.ReplaceAllStringFunc(text, func(line string) string {
		m := nameList.FindStringSubmatch(line)
		if len(line) <= 100 {
			return line
		}
		return m[1] + " = [\n  " + strings.ReplaceAll(m[2], ", ", ",\n  ") + ",\n]"
	})
	return []byte(text)
}

func writeTOML(file string, v any) error {
	data, err := toml.Marshal(v)
	if err != nil {
		return err
	}
	data = tidy(data)
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

// PackSkills lists the skills of a pack: those it names and those of the
// sources it names.
func (c *Catalog) PackSkills(name string) []string {
	pack, ok := c.Packs[name]
	if !ok {
		return nil
	}
	var skills []string
	for _, ref := range pack.Skills {
		if skill, ok := c.resolveRef(ref); ok {
			skills = add(skills, skill)
		}
	}
	for _, source := range pack.Sources {
		if src, ok := c.Sources[source]; ok {
			for _, skill := range src.Skills {
				skills = add(skills, skill)
			}
		}
	}
	sort.Strings(skills)
	return skills
}

// PacksOf lists the packs a skill belongs to.
func (c *Catalog) PacksOf(skill string) []string {
	var packs []string
	for _, name := range c.PackNames() {
		if slices.Contains(c.PackSkills(name), skill) {
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
		for _, skill := range c.PackSkills(pack) {
			on[skill] = true
		}
	}
	for _, ref := range s.Skills {
		if skill, ok := c.resolveRef(ref); ok {
			on[skill] = true
		}
	}
	for _, skill := range s.Inherited {
		on[skill] = true
	}
	for _, ref := range s.Except {
		if skill, ok := c.resolveRef(ref); ok {
			delete(on, skill)
		}
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

// Enable switches on skills, servers ("mcp:name") and packs ("@name") in a set.
func (c *Catalog) Enable(s *Set, names ...string) error {
	if err := c.check(names); err != nil {
		return err
	}
	for _, name := range names {
		if pack, ok := strings.CutPrefix(name, "@"); ok {
			s.Packs = add(s.Packs, pack)
			for _, skill := range c.PackSkills(pack) {
				s.Except = removeRef(s.Except, skill)
			}
			for _, server := range c.Packs[pack].MCPs {
				s.Except = remove(s.Except, MCPPrefix+server)
			}
			continue
		}
		if server, ok := strings.CutPrefix(name, MCPPrefix); ok {
			c.enableMCP(s, server)
			continue
		}
		skill, _ := SplitRef(name)
		s.Except = removeRef(s.Except, skill)
		if !slices.Contains(c.Resolve(*s), skill) {
			s.Skills = addRef(s.Skills, c.Qualify(skill))
		}
	}
	return nil
}

// Disable switches off skills, servers ("mcp:name") and packs ("@name") in a
// set. An entry that a still-enabled pack provides becomes an exception.
func (c *Catalog) Disable(s *Set, names ...string) error {
	if err := c.check(names); err != nil {
		return err
	}
	for _, name := range names {
		if pack, ok := strings.CutPrefix(name, "@"); ok {
			s.Packs = remove(s.Packs, pack)
			for _, skill := range c.PackSkills(pack) {
				c.disableSkill(s, skill)
			}
			for _, server := range c.Packs[pack].MCPs {
				c.disableMCP(s, server)
			}
			continue
		}
		if server, ok := strings.CutPrefix(name, MCPPrefix); ok {
			c.disableMCP(s, server)
			continue
		}
		skill, _ := SplitRef(name)
		c.disableSkill(s, skill)
	}
	return nil
}

func (c *Catalog) disableSkill(s *Set, skill string) {
	s.Skills = removeRef(s.Skills, skill)
	s.Except = removeRef(s.Except, skill)
	if slices.Contains(c.Resolve(*s), skill) {
		s.Except = addRef(s.Except, c.Qualify(skill))
	}
}

func (c *Catalog) check(names []string) error {
	for _, name := range names {
		if pack, ok := strings.CutPrefix(name, "@"); ok {
			if _, ok := c.Packs[pack]; !ok {
				return fmt.Errorf("unknown pack %q", pack)
			}
		} else if server, ok := strings.CutPrefix(name, MCPPrefix); ok {
			if _, ok := c.MCPs[server]; !ok {
				return fmt.Errorf("unknown mcp server %q", server)
			}
		} else if _, ok := c.resolveRef(name); !ok {
			if skill, source := SplitRef(name); source != "" && c.HasSkill(skill) {
				owner, _ := c.SourceOf(skill)
				return fmt.Errorf("skill %q comes from %s, not from %s", skill, owner, source)
			}
			return fmt.Errorf("unknown skill %q", name)
		}
	}
	return nil
}

// AddSource records a source that takes all the skills it offers.
// ExpandAll gives every source that takes all skills the names on offer in
// its clone. A name stays with a source that lists it, and otherwise goes to
// the first of the sources that offer it.
func (c *Catalog) ExpandAll(offered map[string][]string) {
	taken := map[string]bool{}
	for _, src := range c.Sources {
		if !src.All {
			for _, skill := range src.Skills {
				taken[skill] = true
			}
		}
	}
	for _, name := range sortedKeys(c.Sources) {
		src := c.Sources[name]
		if !src.All {
			continue
		}
		src.Skills = nil
		for _, skill := range offered[name] {
			if !taken[skill] {
				taken[skill] = true
				src.Skills = add(src.Skills, skill)
			}
		}
	}
}

// AddSkills records skills under a source, creating the source when needed.
// A source that takes all skills needs no names and stays as it is.
func (c *Catalog) AddSkills(source, url, ref string, skills []string) error {
	if src, ok := c.Sources[source]; ok && len(src.Skills) == 0 {
		return nil
	}
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

// PackAdd puts skills, servers ("mcp:name") and whole sources
// ("owner/repo") into a pack. Skills are recorded with their source.
func (c *Catalog) PackAdd(name string, members []string) error {
	pack, ok := c.Packs[name]
	if !ok {
		return fmt.Errorf("unknown pack %q", name)
	}
	recorded := make([]string, len(members))
	for i, member := range members {
		server, isMCP := strings.CutPrefix(member, MCPPrefix)
		_, isSource := c.Sources[member]
		_, defined := c.MCPs[server]
		_, isSkill := c.resolveRef(member)
		switch {
		case isSource, isMCP && defined:
			recorded[i] = member
		case !isMCP && isSkill:
			recorded[i] = c.Qualify(member)
		case isMCP:
			return fmt.Errorf("unknown mcp server %q", server)
		default:
			return fmt.Errorf("unknown skill %q", member)
		}
	}
	for i, member := range recorded {
		if _, isSource := c.Sources[members[i]]; isSource {
			pack.Sources = add(pack.Sources, member)
		} else if server, ok := strings.CutPrefix(member, MCPPrefix); ok {
			pack.MCPs = add(pack.MCPs, server)
		} else if skill, _ := SplitRef(member); !slices.Contains(c.packSourceSkills(pack), skill) {
			pack.Skills = addRef(pack.Skills, member)
		}
	}
	return nil
}

// packSourceSkills lists the skills a pack holds through its sources.
func (c *Catalog) packSourceSkills(pack *Pack) []string {
	var skills []string
	for _, source := range pack.Sources {
		if src, ok := c.Sources[source]; ok {
			skills = append(skills, src.Skills...)
		}
	}
	return skills
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
// a server and a pack their first definition. What the included catalogs
// enable becomes the inherited part of the merged enabled set.
func Merge(local *Catalog, ids []string, included []*Catalog) (*Catalog, error) {
	data, err := toml.Marshal(local)
	if err != nil {
		return nil, err
	}
	merged := New()
	if err := toml.Unmarshal(data, merged); err != nil {
		return nil, err
	}
	merged.SkillOrigin, merged.MCPOrigin, merged.PackOrigin = map[string]string{}, map[string]string{}, map[string]string{}
	for _, src := range merged.Sources {
		src.All = len(src.Skills) == 0
	}
	for i, inc := range included {
		for _, name := range sortedKeys(inc.Sources) {
			src := inc.Sources[name]
			if own, ok := merged.Sources[name]; ok && own.All {
				continue
			}
			if len(src.Skills) == 0 {
				merged.Sources[name] = &Source{URL: src.URL, Ref: src.Ref, All: true}
				continue
			}
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
		for _, name := range sortedKeys(inc.MCPs) {
			if _, ok := merged.MCPs[name]; !ok {
				def := *inc.MCPs[name]
				merged.MCPs[name] = &def
				merged.MCPOrigin[name] = ids[i]
			}
		}
		for _, name := range sortedKeys(inc.Packs) {
			if _, ok := merged.Packs[name]; !ok {
				pack := inc.Packs[name]
				merged.Packs[name] = &Pack{Description: pack.Description, Sources: slices.Clone(pack.Sources),
					Skills: slices.Clone(pack.Skills), MCPs: slices.Clone(pack.MCPs)}
				merged.PackOrigin[name] = ids[i]
			}
		}
	}
	for _, inc := range included {
		set := inc.Enabled
		set.Inherited, set.InheritedMCPs = nil, nil
		for _, skill := range merged.Resolve(set) {
			merged.Enabled.Inherited = add(merged.Enabled.Inherited, skill)
		}
		for _, server := range merged.ResolveMCPs(set) {
			merged.Enabled.InheritedMCPs = add(merged.Enabled.InheritedMCPs, server)
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
