package catalog

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// MCPPrefix marks an MCP server where skills and servers share a list, as in
// command arguments and in Set.Except.
const MCPPrefix = "mcp:"

// MCP defines one MCP server. String fields may hold ${NAME} placeholders
// for secrets.
type MCP struct {
	Type          string            `yaml:"type"` // "local" or "remote"
	Command       []string          `yaml:"command,omitempty,flow"`
	URL           string            `yaml:"url,omitempty"`
	Transport     string            `yaml:"transport,omitempty"` // "http" (default) or "sse", for remote servers
	Environment   map[string]string `yaml:"environment,omitempty"`
	Headers       map[string]string `yaml:"headers,omitempty"`
	Timeout       float64           `yaml:"timeout,omitempty"` // seconds
	DisabledTools []string          `yaml:"disabled_tools,omitempty,flow"`
}

func (m *MCP) Validate(name string) error {
	switch {
	case m.Type == "local" && len(m.Command) == 0:
		return fmt.Errorf("mcp %q: a local server needs a command", name)
	case m.Type == "local" && m.URL != "":
		return fmt.Errorf("mcp %q: a local server has no url", name)
	case m.Type == "remote" && m.URL == "":
		return fmt.Errorf("mcp %q: a remote server needs a url", name)
	case m.Type != "local" && m.Type != "remote":
		return fmt.Errorf("mcp %q: type must be local or remote", name)
	case m.Transport != "" && m.Transport != "http" && m.Transport != "sse":
		return fmt.Errorf("mcp %q: transport must be http or sse", name)
	}
	return nil
}

func (c *Catalog) MCPNames() []string {
	names := make([]string, 0, len(c.MCPs))
	for name := range c.MCPs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// MCPPacksOf lists the packs a server belongs to.
func (c *Catalog) MCPPacksOf(server string) []string {
	var packs []string
	for _, name := range c.PackNames() {
		if slices.Contains(c.Packs[name].MCPs, server) {
			packs = append(packs, name)
		}
	}
	return packs
}

// ResolveMCPs expands a set to the sorted servers it enables. Names the
// catalog does not define are dropped.
func (c *Catalog) ResolveMCPs(s Set) []string {
	on := map[string]bool{}
	for _, pack := range s.Packs {
		if p, ok := c.Packs[pack]; ok {
			for _, server := range p.MCPs {
				on[server] = true
			}
		}
	}
	for _, server := range s.MCPs {
		on[server] = true
	}
	for _, server := range s.InheritedMCPs {
		on[server] = true
	}
	for _, entry := range s.Except {
		if server, ok := strings.CutPrefix(entry, MCPPrefix); ok {
			delete(on, server)
		}
	}
	var out []string
	for server := range on {
		if _, ok := c.MCPs[server]; ok {
			out = append(out, server)
		}
	}
	sort.Strings(out)
	return out
}

func (c *Catalog) enableMCP(s *Set, server string) {
	s.Except = remove(s.Except, MCPPrefix+server)
	if !slices.Contains(c.ResolveMCPs(*s), server) {
		s.MCPs = add(s.MCPs, server)
	}
}

func (c *Catalog) disableMCP(s *Set, server string) {
	s.MCPs = remove(s.MCPs, server)
	s.Except = remove(s.Except, MCPPrefix+server)
	if slices.Contains(c.ResolveMCPs(*s), server) {
		s.Except = add(s.Except, MCPPrefix+server)
	}
}

// RemoveMCP drops a server definition, its pack memberships and its place in
// the global set.
func (c *Catalog) RemoveMCP(server string) {
	delete(c.MCPs, server)
	for _, pack := range c.Packs {
		pack.MCPs = remove(pack.MCPs, server)
	}
	c.Enabled.MCPs = remove(c.Enabled.MCPs, server)
	c.Enabled.Except = remove(c.Enabled.Except, MCPPrefix+server)
}
