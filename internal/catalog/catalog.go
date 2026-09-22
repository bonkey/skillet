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

// Source is a git repository of skills. In the catalog it goes by its
// name: the explicit one, or else the repository name from the URL.
type Source struct {
	// Name is the explicit name of the source; empty for a derived one.
	Name string `toml:"name,omitempty"`
	URL  string `toml:"url"`
	Ref  string `toml:"ref,omitempty"`
	// Enabled switches the source and all its skills off when false.
	Enabled *bool `toml:"enabled,omitempty"`
	// Skills names the skills taken from the source: the entries of `only`
	// in the file. Without an entry that is on, the source takes all the
	// skills it offers.
	Skills []string `toml:"-"`
	// Disabled lists the entries of `only` that are switched off.
	Disabled []string `toml:"-"`
	// All marks, in a merged catalog, a source that takes all skills. Its
	// Skills then hold what ExpandAll found on offer.
	All bool `toml:"-"`
}

type Pack struct {
	Description string `toml:"description"`
	// Enabled switches the pack and what it holds off when false.
	Enabled *bool `toml:"enabled,omitempty"`
	// Skills holds skill references and source names; a source name puts
	// every catalog skill of the source into the pack.
	Skills []string `toml:"skills,omitempty"`
	MCPs   []string `toml:"mcps,omitempty"`
}

// Set names what is enabled: what the links and MCP entries of a scope
// hold, or what a `run` session adds.
type Set struct {
	Packs  []string `toml:"packs,omitempty"`
	Skills []string `toml:"skills,omitempty"`
	MCPs   []string `toml:"mcps,omitempty"`
}

type Catalog struct {
	// Gist is the gist this catalog is pushed to and pulled from.
	Gist string `toml:"gist,omitempty"`
	// Includes lists gists whose catalogs are merged into this one.
	Includes []string `toml:"includes,omitempty"`
	// Secrets lists 1Password items whose fields are the values of ${NAME}
	// placeholders. Only the local catalog's list is used; an included
	// catalog never chooses where secrets come from.
	Secrets []SecretItem `toml:"secrets,omitempty"`
	Agents  []string     `toml:"agents"`
	// Sources, MCPs and Packs are keyed by the effective name of each
	// entry. The file holds them as arrays of tables; see fileCatalog.
	Sources map[string]*Source `toml:"-"`
	MCPs    map[string]*MCP    `toml:"-"`
	Packs   map[string]*Pack   `toml:"-"`
	// Enabled and Disabled are the name lists of an overlay; see Override.
	Enabled  []string `toml:"-"`
	Disabled []string `toml:"-"`

	// SkillOrigin, SourceOrigin, MCPOrigin and PackOrigin name the gist an
	// entry of a merged catalog was included from. Entries of the local
	// catalog are absent.
	SkillOrigin  map[string]string `toml:"-"`
	SourceOrigin map[string]string `toml:"-"`
	MCPOrigin    map[string]string `toml:"-"`
	PackOrigin   map[string]string `toml:"-"`
}

func New() *Catalog {
	return &Catalog{
		Agents:  []string{"claude-code"},
		Sources: map[string]*Source{},
		MCPs:    map[string]*MCP{},
		Packs:   map[string]*Pack{},
	}
}

// fileCatalog is the catalog as its file holds it: packs under [[packs]],
// each with a name, sources under [[skills]] and servers under [[mcps]],
// each named by an optional key.
type fileCatalog struct {
	Gist     string        `toml:"gist,omitempty"`
	Includes []string      `toml:"includes,omitempty"`
	Secrets  []SecretItem  `toml:"secrets,omitempty"`
	Agents   []string      `toml:"agents"`
	Packs    []*filePack   `toml:"packs,omitempty"`
	Skills   []*fileSource `toml:"skills,omitempty"`
	MCPs     []*MCP        `toml:"mcps,omitempty"`
	Enabled  []string      `toml:"enabled,omitempty"`
	Disabled []string      `toml:"disabled,omitempty"`
}

// filePack is a [[packs]] entry: a pack with the name it goes by.
type filePack struct {
	Name string `toml:"name"`
	Pack
}

// fileSource is a [[skills]] entry. Only holds names and, for a skill that
// is switched off, tables of the shape { name = "x", enabled = false }.
type fileSource struct {
	Name    string `toml:"name,omitempty"`
	URL     string `toml:"url"`
	Ref     string `toml:"ref,omitempty"`
	Enabled *bool  `toml:"enabled,omitempty"`
	Only    []any  `toml:"only,omitempty,inline"`
}

type onlyFlag struct {
	Name    string `toml:"name"`
	Enabled bool   `toml:"enabled"`
}

func (f *fileSource) source() (*Source, error) {
	src := &Source{Name: f.Name, URL: f.URL, Ref: f.Ref, Enabled: f.Enabled}
	for _, entry := range f.Only {
		switch v := entry.(type) {
		case string:
			src.Skills = append(src.Skills, v)
		case map[string]any:
			name, _ := v["name"].(string)
			enabled, isBool := v["enabled"].(bool)
			if name == "" || !isBool {
				return nil, fmt.Errorf("source %s: only holds %v; use a name or { name, enabled }", f.URL, entry)
			}
			src.Skills = append(src.Skills, name)
			if !enabled {
				src.Disabled = append(src.Disabled, name)
			}
		default:
			return nil, fmt.Errorf("source %s: only holds %v; use a name or { name, enabled }", f.URL, entry)
		}
	}
	return src, nil
}

func fileSourceOf(src *Source) *fileSource {
	f := &fileSource{Name: src.Name, URL: src.URL, Ref: src.Ref, Enabled: src.Enabled}
	for _, skill := range src.Skills {
		if src.SkillOn(skill) {
			f.Only = append(f.Only, skill)
		} else {
			f.Only = append(f.Only, onlyFlag{Name: skill})
		}
	}
	return f
}

func Load(file string) (*Catalog, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, err
	}
	c, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return c, nil
}

// LoadOverlay reads the file that overrides the catalog on this machine;
// nil without one.
func LoadOverlay(file string) (*Catalog, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c, err := parse(data, true)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return c, nil
}

// Parse reads a catalog file. Sources and servers get their names, and
// servers are validated.
func Parse(data []byte) (*Catalog, error) { return parse(data, false) }

// parse reads a catalog file, or an overlay: the same shape, plus the
// `enabled` and `disabled` name lists, without `gist` and `includes`, and
// with `agents` absent when the file has none.
func parse(data []byte, overlay bool) (*Catalog, error) {
	var f fileCatalog
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	c := New()
	c.Gist, c.Includes, c.Secrets = f.Gist, f.Includes, f.Secrets
	if f.Agents != nil {
		c.Agents = f.Agents
	}
	if overlay {
		if f.Gist != "" || len(f.Includes) > 0 {
			return nil, errors.New("gist and includes belong in config.toml")
		}
		c.Agents, c.Enabled, c.Disabled = f.Agents, f.Enabled, f.Disabled
	} else if len(f.Enabled)+len(f.Disabled) > 0 {
		return nil, errors.New("the enabled and disabled lists belong in config.local.toml")
	}
	for i, entry := range f.Packs {
		if entry.Name == "" {
			return nil, fmt.Errorf("[[packs]] entry %d has no name", i+1)
		}
		if _, dup := c.Packs[entry.Name]; dup {
			return nil, fmt.Errorf("two packs are named %q", entry.Name)
		}
		pack := entry.Pack
		c.Packs[entry.Name] = &pack
	}
	sources := make([]*Source, len(f.Skills))
	for i, entry := range f.Skills {
		src, err := entry.source()
		if err != nil {
			return nil, err
		}
		sources[i] = src
	}
	names, err := sourceNames(sources)
	if err != nil {
		return nil, err
	}
	for i, src := range sources {
		c.Sources[names[i]] = src
	}
	for _, def := range f.MCPs {
		name, err := def.name()
		if err != nil {
			return nil, err
		}
		if err := def.Validate(name); err != nil {
			return nil, err
		}
		if _, dup := c.MCPs[name]; dup {
			return nil, fmt.Errorf("two mcp servers are named %q; set name on one", name)
		}
		c.MCPs[name] = def
	}
	return c, nil
}

// Encode renders the catalog as its file, packs, sources and servers sorted
// by name.
func (c *Catalog) Encode() ([]byte, error) {
	f := fileCatalog{Gist: c.Gist, Includes: c.Includes, Secrets: c.Secrets, Agents: c.Agents}
	for _, name := range c.PackNames() {
		f.Packs = append(f.Packs, &filePack{Name: name, Pack: *c.Packs[name]})
	}
	for _, name := range sortedKeys(c.Sources) {
		f.Skills = append(f.Skills, fileSourceOf(c.Sources[name]))
	}
	for _, name := range c.MCPNames() {
		f.MCPs = append(f.MCPs, c.MCPs[name])
	}
	data, err := toml.Marshal(f)
	if err != nil {
		return nil, err
	}
	return tidy(data), nil
}

func (c *Catalog) Save(file string) error {
	data, err := c.Encode()
	if err != nil {
		return err
	}
	return writeFile(file, data)
}

var nameList = regexp.MustCompile(`(?m)^(skills|mcps|packs|except|only) = \[(.*)\]$`)

// tidy makes the encoder's output pleasant to edit: a long list of names
// gets one name per line.
func tidy(data []byte) []byte {
	text := nameList.ReplaceAllStringFunc(string(data), func(line string) string {
		m := nameList.FindStringSubmatch(line)
		if len(line) <= 100 || strings.Contains(m[2], "{") {
			return line
		}
		return m[1] + " = [\n  " + strings.ReplaceAll(m[2], ", ", ",\n  ") + ",\n]"
	})
	return []byte(text)
}

func writeFile(file string, data []byte) error {
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

// PackSkills lists the skills of a pack: those it names and every skill
// of the sources it names.
func (c *Catalog) PackSkills(name string) []string {
	pack, ok := c.Packs[name]
	if !ok {
		return nil
	}
	var skills []string
	for _, member := range pack.Skills {
		if src, ok := c.Sources[member]; ok {
			for _, skill := range src.Skills {
				skills = add(skills, skill)
			}
		} else if skill, ok := c.resolveRef(member); ok {
			skills = add(skills, skill)
		}
	}
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
	var out []string
	for skill := range on {
		if c.HasSkill(skill) {
			out = append(out, skill)
		}
	}
	sort.Strings(out)
	return out
}

// Enable switches on skills, servers ("mcp:name"), packs ("@name") and
// whole sources ("skills:name") in a set.
func (c *Catalog) Enable(s *Set, names ...string) error {
	if err := c.Check(names); err != nil {
		return err
	}
	for _, name := range names {
		switch kind, rest := kindOf(name); kind {
		case kindPack:
			s.Packs = add(s.Packs, rest)
		case kindMCP:
			if !slices.Contains(c.ResolveMCPs(*s), rest) {
				s.MCPs = add(s.MCPs, rest)
			}
		case kindSource:
			for _, skill := range c.Sources[rest].Skills {
				c.enableSkill(s, skill)
			}
		default:
			skill, _ := SplitRef(name)
			c.enableSkill(s, skill)
		}
	}
	return nil
}

func (c *Catalog) enableSkill(s *Set, skill string) {
	if !slices.Contains(c.Resolve(*s), skill) {
		s.Skills = addRef(s.Skills, c.Qualify(skill))
	}
}

// Disable switches off skills, servers ("mcp:name"), packs ("@name") and
// whole sources ("skills:name") in a set.
func (c *Catalog) Disable(s *Set, names ...string) error {
	if err := c.Check(names); err != nil {
		return err
	}
	for _, name := range names {
		switch kind, rest := kindOf(name); kind {
		case kindPack:
			s.Packs = remove(s.Packs, rest)
			for _, skill := range c.PackSkills(rest) {
				s.Skills = removeRef(s.Skills, skill)
			}
			for _, server := range c.Packs[rest].MCPs {
				s.MCPs = remove(s.MCPs, server)
			}
		case kindMCP:
			s.MCPs = remove(s.MCPs, rest)
		case kindSource:
			for _, skill := range c.Sources[rest].Skills {
				s.Skills = removeRef(s.Skills, skill)
			}
		default:
			skill, _ := SplitRef(name)
			s.Skills = removeRef(s.Skills, skill)
		}
	}
	return nil
}

// Check rejects names the catalog does not know.
func (c *Catalog) Check(names []string) error {
	for _, name := range names {
		switch kind, rest := kindOf(name); kind {
		case kindPack:
			if _, ok := c.Packs[rest]; !ok {
				return fmt.Errorf("unknown pack %q", rest)
			}
		case kindMCP:
			if _, ok := c.MCPs[rest]; !ok {
				return fmt.Errorf("unknown mcp server %q", rest)
			}
		case kindSource:
			if _, ok := c.Sources[rest]; !ok {
				return fmt.Errorf("unknown source %q", rest)
			}
		default:
			if _, ok := c.resolveRef(name); !ok {
				if skill, source := SplitRef(name); source != "" && c.HasSkill(skill) {
					owner, _ := c.SourceOf(skill)
					return fmt.Errorf("skill %q comes from %s, not from %s", skill, owner, source)
				}
				return fmt.Errorf("unknown skill %q", name)
			}
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

// AddSkills records skills under the source at url, adding the source when
// needed, and returns the source's name. A source that takes all skills
// needs no names and stays as it is.
func (c *Catalog) AddSkills(url, ref string, skills []string) (string, error) {
	name, src := c.SourceAt(url)
	if src != nil && src.takesAll() {
		return name, nil
	}
	for _, skill := range skills {
		if owner, ok := c.SourceOf(skill); ok && owner != name {
			return "", fmt.Errorf("skill %q is already in the catalog from %s", skill, owner)
		}
	}
	if src == nil {
		src = &Source{URL: url, Ref: ref}
		if err := c.addSource(src); err != nil {
			return "", err
		}
		name, _ = c.SourceAt(url)
	}
	for _, skill := range skills {
		src.Skills = add(src.Skills, skill)
	}
	return name, nil
}

// SourceAt finds the source with a URL.
func (c *Catalog) SourceAt(url string) (string, *Source) {
	for _, name := range sortedKeys(c.Sources) {
		if c.Sources[name].URL == url {
			return name, c.Sources[name]
		}
	}
	return "", nil
}

// sourceList lists the sources in name order.
func (c *Catalog) sourceList() []*Source {
	var list []*Source
	for _, name := range sortedKeys(c.Sources) {
		list = append(list, c.Sources[name])
	}
	return list
}

// addSource records a source and names every source again: a new one may
// share a repository name with an existing one, which renames both.
func (c *Catalog) addSource(src *Source) error {
	list := append(c.sourceList(), src)
	names, err := sourceNames(list)
	if err != nil {
		return err
	}
	c.Sources = map[string]*Source{}
	for i, s := range list {
		c.Sources[names[i]] = s
	}
	return nil
}

// NamesFor gives the names sources would have once they are all in the
// catalog, since a new repository name can rename an existing source. One
// whose URL the catalog holds already keeps the name it has.
func (c *Catalog) NamesFor(sources []*Source) ([]string, error) {
	list := c.sourceList()
	known := map[string]string{}
	for _, name := range sortedKeys(c.Sources) {
		known[c.Sources[name].URL] = name
	}
	var added []*Source
	for _, src := range sources {
		if _, ok := known[src.URL]; !ok {
			added = append(added, src)
		}
	}
	names, err := sourceNames(append(list, added...))
	if err != nil {
		return nil, err
	}
	for i, src := range list {
		known[src.URL] = names[i]
	}
	for i, src := range added {
		known[src.URL] = names[len(list)+i]
	}
	out := make([]string, len(sources))
	for i, src := range sources {
		out[i] = known[src.URL]
	}
	return out, nil
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

// PackAdd puts skills, servers ("mcp:name") and whole sources into a pack.
// A member that names a source is that source; "name@source" is always a
// skill. Skills are recorded with their source.
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
	for _, member := range recorded {
		if _, isSource := c.Sources[member]; isSource {
			pack.Skills = add(pack.Skills, member)
		} else if server, ok := strings.CutPrefix(member, MCPPrefix); ok {
			pack.MCPs = add(pack.MCPs, server)
		} else if skill, _ := SplitRef(member); !slices.Contains(c.packSourceSkills(pack), skill) {
			pack.Skills = c.addPackRef(pack.Skills, member)
		}
	}
	return nil
}

// packSourceSkills lists the skills a pack holds through its sources.
func (c *Catalog) packSourceSkills(pack *Pack) []string {
	var skills []string
	for _, member := range pack.Skills {
		if src, ok := c.Sources[member]; ok {
			skills = append(skills, src.Skills...)
		}
	}
	return skills
}

// addPackRef records a skill once in a pack, replacing another spelling of
// it. A member that names a source stays.
func (c *Catalog) addPackRef(list []string, ref string) []string {
	name, _ := SplitRef(ref)
	out := slices.DeleteFunc(slices.Clone(list), func(entry string) bool {
		_, isSource := c.Sources[entry]
		entryName, _ := SplitRef(entry)
		return !isSource && entryName == name
	})
	return add(out, ref)
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
// include: a skill keeps its first source, a source its first URL, ref and
// flag, a server and a pack their first definition.
func Merge(local *Catalog, ids []string, included []*Catalog) (*Catalog, error) {
	data, err := local.Encode()
	if err != nil {
		return nil, err
	}
	merged, err := Parse(data)
	if err != nil {
		return nil, err
	}
	merged.SkillOrigin, merged.SourceOrigin = map[string]string{}, map[string]string{}
	merged.MCPOrigin, merged.PackOrigin = map[string]string{}, map[string]string{}
	for _, src := range merged.Sources {
		src.All = src.takesAll()
	}
	for i, inc := range included {
		for _, name := range sortedKeys(inc.Sources) {
			src := inc.Sources[name]
			if own, ok := merged.Sources[name]; ok && own.All {
				continue
			}
			if src.takesAll() {
				merged.Sources[name] = &Source{URL: src.URL, Ref: src.Ref, Enabled: src.Enabled,
					Skills: slices.Clone(src.Disabled), Disabled: slices.Clone(src.Disabled), All: true}
				merged.SourceOrigin[name] = ids[i]
				continue
			}
			for _, skill := range src.Skills {
				if merged.HasSkill(skill) {
					continue
				}
				if _, ok := merged.Sources[name]; !ok {
					merged.Sources[name] = &Source{URL: src.URL, Ref: src.Ref, Enabled: src.Enabled}
					merged.SourceOrigin[name] = ids[i]
				}
				merged.Sources[name].Skills = add(merged.Sources[name].Skills, skill)
				if !src.SkillOn(skill) {
					merged.Sources[name].Disabled = add(merged.Sources[name].Disabled, skill)
				}
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
				merged.Packs[name] = &Pack{Description: pack.Description, Enabled: pack.Enabled,
					Skills: slices.Clone(pack.Skills), MCPs: slices.Clone(pack.MCPs)}
				merged.PackOrigin[name] = ids[i]
			}
		}
	}
	return merged, nil
}

// Overlay returns base with the entries of over: a same-named source,
// server or pack replaces the one in base, the others are added, the
// secrets are appended, and agents are taken when over lists them.
func Overlay(base, over *Catalog) (*Catalog, error) {
	data, err := base.Encode()
	if err != nil {
		return nil, err
	}
	c, err := Parse(data)
	if err != nil {
		return nil, err
	}
	for name, src := range over.Sources {
		copied := *src
		copied.Skills, copied.Disabled = slices.Clone(src.Skills), slices.Clone(src.Disabled)
		c.Sources[name] = &copied
	}
	for name, def := range over.MCPs {
		copied := *def
		c.MCPs[name] = &copied
	}
	for name, pack := range over.Packs {
		c.Packs[name] = &Pack{Description: pack.Description, Enabled: pack.Enabled,
			Skills: slices.Clone(pack.Skills), MCPs: slices.Clone(pack.MCPs)}
	}
	c.Secrets = append(c.Secrets, over.Secrets...)
	if over.Agents != nil {
		c.Agents = over.Agents
	}
	return c, nil
}

// Override switches the named entries on, then off: skills, servers
// ("mcp:name"), packs ("@name") and sources ("skills:name"). A name in both
// lists ends up off. A name the catalog does not know is an error.
func (c *Catalog) Override(enabled, disabled []string) error {
	if err := c.Check(append(slices.Clone(enabled), disabled...)); err != nil {
		return err
	}
	for i, names := range [][]string{enabled, disabled} {
		on := i == 0
		for _, name := range names {
			if kind, _ := kindOf(name); kind != kindSkill {
				if err := c.SetFlag(name, on); err != nil {
					return err
				}
				continue
			}
			skill, _ := SplitRef(name)
			source, _ := c.SourceOf(skill)
			src := c.Sources[source]
			if on {
				src.Disabled = remove(src.Disabled, skill)
			} else {
				src.Disabled = add(src.Disabled, skill)
			}
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
