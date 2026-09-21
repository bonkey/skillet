package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/bonkey/skillet/internal/catalog"
)

type MCPImportReport struct {
	Servers []string          // definitions added to the catalog
	Packs   []string          // packs created or extended from presets
	Skipped map[string]string // server name to reason
}

// MCPSetupConfig is the config file of the mcp-setup script.
func (a *App) MCPSetupConfig() string {
	return filepath.Join(a.Paths.Home, ".config", "mcp-setup", "config.json")
}

// ImportMCP copies the server definitions and presets of an mcp-setup
// config into the local catalog, values included. Nothing becomes enabled.
func (a *App) ImportMCP(file string) (MCPImportReport, error) {
	report := MCPImportReport{Skipped: map[string]string{}}
	data, err := os.ReadFile(file)
	if err != nil {
		return report, err
	}
	var config struct {
		Presets map[string][]string `json:"presets"`
		MCPs    map[string]struct {
			Type          string            `json:"type"`
			Command       []string          `json:"command"`
			URL           string            `json:"url"`
			Transport     string            `json:"transport"`
			Environment   map[string]string `json:"environment"`
			Headers       map[string]string `json:"headers"`
			Timeout       float64           `json:"timeout"`
			DisabledTools []string          `json:"disabled_tools"`
		} `json:"mcps"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return report, fmt.Errorf("%s: %w", file, err)
	}

	names := make([]string, 0, len(config.MCPs))
	for name := range config.MCPs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, exists := a.Local.MCPs[name]; exists {
			report.Skipped[name] = "already in the catalog"
			continue
		}
		in := config.MCPs[name]
		def := catalog.MCP{Type: in.Type, Command: in.Command, URL: in.URL, Transport: in.Transport,
			Environment: in.Environment, Headers: in.Headers, Timeout: in.Timeout, DisabledTools: in.DisabledTools}
		if err := def.Validate(name); err != nil {
			report.Skipped[name] = err.Error()
			continue
		}
		a.Local.MCPs[name] = &def
		report.Servers = append(report.Servers, name)
	}

	presets := make([]string, 0, len(config.Presets))
	for preset := range config.Presets {
		presets = append(presets, preset)
	}
	sort.Strings(presets)
	for _, preset := range presets {
		var members []string
		for _, server := range config.Presets[preset] {
			if a.Local.MCPs[server] != nil {
				members = append(members, catalog.MCPPrefix+server)
			}
		}
		if len(members) == 0 {
			continue
		}
		if _, exists := a.Local.Packs[preset]; !exists {
			if err := a.Local.CreatePack(preset, "MCP servers of the mcp-setup preset "+preset, nil); err != nil {
				return report, err
			}
		}
		if err := a.Local.PackAdd(preset, members); err != nil {
			return report, err
		}
		report.Packs = append(report.Packs, preset)
	}
	return report, a.Save()
}
