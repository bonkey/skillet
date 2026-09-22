package catalog

import (
	"fmt"
	"slices"
	"sort"
)

// SecretItem names a 1Password item.
type SecretItem struct {
	Account string `toml:"account"` // sign-in address or account id
	Vault   string `toml:"vault"`
	Item    string `toml:"item"`
}

// MCP defines one MCP server. String fields may hold ${NAME} placeholders
// for secrets.
type MCP struct {
	// Name is the explicit name of the server. Without one, the server is
	// named after its command or URL.
	Name          string            `toml:"name,omitempty"`
	Command       []string          `toml:"command,omitempty"`
	URL           string            `toml:"url,omitempty"`
	Transport     string            `toml:"transport,omitempty"` // "http" (default) or "sse", for remote servers
	Environment   map[string]string `toml:"environment,omitempty,inline"`
	Headers       map[string]string `toml:"headers,omitempty,inline"`
	Timeout       float64           `toml:"timeout,omitempty"` // seconds
	DisabledTools []string          `toml:"disabled_tools,omitempty"`
	// Enabled switches the server off when false.
	Enabled *bool `toml:"enabled,omitempty"`
	// Type is "local" for a server with a command and "remote" for one
	// with a URL. Validate sets it.
	Type string `toml:"-"`
}

// Validate checks the definition and sets its Type.
func (m *MCP) Validate(name string) error {
	switch {
	case len(m.Command) > 0 && m.URL != "":
		return fmt.Errorf("mcp %q: a server has a command or a url, not both", name)
	case len(m.Command) == 0 && m.URL == "":
		return fmt.Errorf("mcp %q: a server needs a command or a url", name)
	case m.Transport != "" && m.Transport != "http" && m.Transport != "sse":
		return fmt.Errorf("mcp %q: transport must be http or sse", name)
	}
	m.Type = "remote"
	if len(m.Command) > 0 {
		m.Type = "local"
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
	var out []string
	for server := range on {
		if _, ok := c.MCPs[server]; ok {
			out = append(out, server)
		}
	}
	sort.Strings(out)
	return out
}
