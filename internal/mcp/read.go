package mcp

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/bonkey/skillet/internal/catalog"
	"github.com/pelletier/go-toml/v2"
)

// parsers turn an agent's entry back into a definition, for the agents
// whose shapes import reads. The keys are those of Targets.
var parsers = map[string]func(map[string]any) catalog.MCP{
	"claude-code": parseClaude,
	"codex":       parseCodex,
	"gemini-cli":  parseGemini,
}

// Readable lists the agents whose config import reads servers from.
func Readable() []string {
	names := make([]string, 0, len(parsers))
	for name := range parsers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Read returns the servers in an agent's config as definitions, by name,
// and the names of entries that make no valid definition. A missing file
// holds nothing.
func Read(home, agent string) (defs map[string]catalog.MCP, invalid []string, err error) {
	target, ok := Targets[agent]
	parse := parsers[agent]
	if !ok || parse == nil {
		return nil, nil, nil
	}
	file := filepath.Join(home, filepath.FromSlash(target.File))
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	entries, err := rawEntries(target, data)
	if err != nil {
		return nil, nil, err
	}
	defs = map[string]catalog.MCP{}
	for _, name := range sortedEntryNames(entries) {
		def := parse(entries[name])
		if def.Validate(name) != nil {
			invalid = append(invalid, name)
			continue
		}
		defs[name] = def
	}
	return defs, invalid, nil
}

// rawEntries decodes the servers node of a config into plain maps.
func rawEntries(target Target, data []byte) (map[string]map[string]any, error) {
	entries := map[string]map[string]any{}
	if target.TOML {
		var doc map[string]any
		if err := toml.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		for _, key := range target.Node {
			doc, _ = doc[key].(map[string]any)
		}
		for name, value := range doc {
			if entry, ok := value.(map[string]any); ok {
				entries[name] = entry
			}
		}
		return entries, nil
	}
	f, err := parseJSONFile(data)
	if err != nil {
		return nil, err
	}
	names, err := f.names(target.Node)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		value, _, err := f.get(target.Node, name)
		if err != nil {
			return nil, err
		}
		if entry, ok := value.(map[string]any); ok {
			entries[name] = entry
		}
	}
	return entries, nil
}

func sortedEntryNames(entries map[string]map[string]any) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func parseClaude(m map[string]any) catalog.MCP {
	def := catalog.MCP{Command: command(m), URL: str(m, "url"), Environment: strMap(m, "env"), Headers: strMap(m, "headers"), Timeout: num(m, "timeout") / 1000}
	if str(m, "type") == "sse" {
		def.Transport = "sse"
	}
	return def
}

func parseCodex(m map[string]any) catalog.MCP {
	return catalog.MCP{Command: command(m), URL: str(m, "url"), Environment: strMap(m, "env"), Headers: strMap(m, "http_headers"),
		Timeout: num(m, "tool_timeout_sec"), DisabledTools: strList(m, "disabled_tools")}
}

func parseGemini(m map[string]any) catalog.MCP {
	def := catalog.MCP{Command: command(m), URL: str(m, "httpUrl"), Environment: strMap(m, "env"), Headers: strMap(m, "headers"),
		Timeout: num(m, "timeout") / 1000, DisabledTools: strList(m, "excludeTools")}
	if def.URL == "" && str(m, "url") != "" {
		def.URL, def.Transport = str(m, "url"), "sse"
	}
	return def
}

// command joins the command and its args of a stdio entry.
func command(m map[string]any) []string {
	cmd := str(m, "command")
	if cmd == "" {
		return nil
	}
	return append([]string{cmd}, strList(m, "args")...)
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func num(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	}
	return 0
}

func strList(m map[string]any, key string) []string {
	items, _ := m[key].([]any)
	var out []string
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func strMap(m map[string]any, key string) map[string]string {
	values, _ := m[key].(map[string]any)
	if len(values) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range values {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}
