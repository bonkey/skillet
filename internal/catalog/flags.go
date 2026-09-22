package catalog

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// The kinds of name a command accepts: "@name" is a pack, "mcp:name" a
// server, "skills:name" a source, and anything else a skill reference.
const (
	PackPrefix   = "@"
	MCPPrefix    = "mcp:"
	SourcePrefix = "skills:"
)

type kind int

const (
	kindSkill kind = iota
	kindPack
	kindMCP
	kindSource
)

// kindOf tells what a name stands for and strips its prefix.
func kindOf(name string) (kind, string) {
	if rest, ok := strings.CutPrefix(name, PackPrefix); ok {
		return kindPack, rest
	}
	if rest, ok := strings.CutPrefix(name, MCPPrefix); ok {
		return kindMCP, rest
	}
	if rest, ok := strings.CutPrefix(name, SourcePrefix); ok {
		return kindSource, rest
	}
	return kindSkill, name
}

// IsSource reports whether a name is written "skills:name".
func IsSource(name string) bool { return strings.HasPrefix(name, SourcePrefix) }

func on(flag *bool) bool { return flag == nil || *flag }

// flag is the stored form of an enabled flag: absent when on.
func flag(on bool) *bool {
	if on {
		return nil
	}
	off := false
	return &off
}

// On reports whether the source is switched on.
func (s *Source) On() bool { return on(s.Enabled) }

// SkillOn reports whether a skill of the source is switched on, the source
// itself aside.
func (s *Source) SkillOn(skill string) bool { return !slices.Contains(s.Disabled, skill) }

// listed names the skills of the source that `only` switches on.
func (s *Source) listed() []string {
	var names []string
	for _, skill := range s.Skills {
		if s.SkillOn(skill) {
			names = append(names, skill)
		}
	}
	return names
}

// takesAll reports whether the source takes every skill it offers: `only`
// names none that is on.
func (s *Source) takesAll() bool { return len(s.listed()) == 0 }

// setSkill switches one skill of the source on or off in `only`. A source
// that takes all keeps taking all: a skill switched on there leaves the
// list, one switched off joins it as an entry that is off. A listed source
// keeps at least one skill on, since a list without one would take all.
func (s *Source) setSkill(skill string, on bool) error {
	all := s.takesAll()
	if on {
		s.Disabled = remove(s.Disabled, skill)
		if all {
			s.Skills = remove(s.Skills, skill)
		} else {
			s.Skills = add(s.Skills, skill)
		}
		return nil
	}
	if listed := s.listed(); !all && len(listed) == 1 && listed[0] == skill {
		return fmt.Errorf("%s is the last skill of the list that is on; switch the source off instead", skill)
	}
	s.Skills = add(s.Skills, skill)
	s.Disabled = add(s.Disabled, skill)
	return nil
}

// On reports whether the pack is switched on.
func (p *Pack) On() bool { return on(p.Enabled) }

// On reports whether the server is switched on.
func (m *MCP) On() bool { return on(m.Enabled) }

// Declared lists the skills and servers the flags switch on. Everything is
// on unless switched off: a skill needs its source, itself and, when packs
// hold it, one of those packs on; a server needs itself and, when packs
// hold it, one of them. from tells which origins take part: packs and
// sources of other origins neither hold nor contribute anything. The local
// catalog's entries have the origin "".
func (c *Catalog) Declared(from func(origin string) bool) Set {
	members := map[string][]string{}
	for name := range c.Packs {
		if from(c.PackOrigin[name]) {
			members[name] = c.PackSkills(name)
		}
	}
	holds := func(item string, in func(pack string) []string) (held, on bool) {
		for name := range members {
			if slices.Contains(in(name), item) {
				held = true
				on = on || c.Packs[name].On()
			}
		}
		return held, on
	}
	var set Set
	for _, skill := range c.SkillNames() {
		source, _ := c.SourceOf(skill)
		src := c.Sources[source]
		if !src.On() || !src.SkillOn(skill) {
			continue
		}
		held, on := holds(skill, func(pack string) []string { return members[pack] })
		if held && on || !held && from(c.SourceOrigin[source]) {
			set.Skills = append(set.Skills, skill)
		}
	}
	for _, server := range c.MCPNames() {
		if !c.MCPs[server].On() {
			continue
		}
		held, on := holds(server, func(pack string) []string { return c.Packs[pack].MCPs })
		if held && on || !held && from(c.MCPOrigin[server]) {
			set.MCPs = append(set.MCPs, server)
		}
	}
	sort.Strings(set.Skills)
	sort.Strings(set.MCPs)
	return set
}

// SetFlag switches a pack ("@name"), a server ("mcp:name") or a source
// ("skills:name") on or off. A skill goes through SetSkillFlag.
func (c *Catalog) SetFlag(name string, on bool) error {
	switch kind, rest := kindOf(name); kind {
	case kindPack:
		pack, ok := c.Packs[rest]
		if !ok {
			return fmt.Errorf("unknown pack %q", rest)
		}
		pack.Enabled = flag(on)
	case kindMCP:
		def, ok := c.MCPs[rest]
		if !ok {
			return fmt.Errorf("unknown mcp server %q", rest)
		}
		def.Enabled = flag(on)
	case kindSource:
		src, ok := c.Sources[rest]
		if !ok {
			return fmt.Errorf("unknown source %q", rest)
		}
		src.Enabled = flag(on)
	default:
		return fmt.Errorf("skill %q: name its source", name)
	}
	return nil
}

// SetSkillFlag switches one skill of a source on or off.
func (c *Catalog) SetSkillFlag(source, skill string, on bool) error {
	src, ok := c.Sources[source]
	if !ok {
		return fmt.Errorf("unknown source %q", source)
	}
	if err := src.setSkill(skill, on); err != nil {
		return fmt.Errorf("%w; `skillet disable --save %s%s`", err, SourcePrefix, source)
	}
	return nil
}
