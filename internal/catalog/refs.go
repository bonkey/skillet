package catalog

import (
	"slices"
	"strings"
)

// A skill reference is a skill name, optionally followed by "@" and the
// source it comes from: "pr@bonkey/skills". skillet records references with
// their source. One that names another source than the skill's does not
// resolve.

// SplitRef parts a reference into the skill name and the source, which is
// empty for a bare name.
func SplitRef(ref string) (name, source string) {
	name, source, _ = strings.Cut(ref, "@")
	return name, source
}

// resolveRef returns the skill a reference stands for.
func (c *Catalog) resolveRef(ref string) (string, bool) {
	name, source := SplitRef(ref)
	owner, ok := c.SourceOf(name)
	return name, ok && (source == "" || source == owner)
}

// Qualify spells a reference with the skill's source.
func (c *Catalog) Qualify(ref string) string {
	name, _ := SplitRef(ref)
	if owner, ok := c.SourceOf(name); ok {
		return name + "@" + owner
	}
	return ref
}

// addRef records a skill once, replacing another spelling of it.
func addRef(list []string, ref string) []string {
	name, _ := SplitRef(ref)
	return add(removeRef(list, name), ref)
}

// removeRef drops every spelling of a skill from a list.
func removeRef(list []string, name string) []string {
	out := slices.DeleteFunc(slices.Clone(list), func(entry string) bool {
		entryName, _ := SplitRef(entry)
		return entryName == name && !strings.HasPrefix(entry, MCPPrefix)
	})
	if len(out) == 0 {
		return nil
	}
	return out
}
