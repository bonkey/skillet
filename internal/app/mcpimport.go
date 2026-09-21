package app

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/mcp"
	"github.com/bonkey/skillet/internal/secrets"
)

type MCPImportReport struct {
	Servers     []string          // definitions added to the catalog
	Packs       []string          // packs created or extended from presets
	Secrets     []string          // secret names written to the secrets file
	Skipped     map[string]string // server name to reason
	OtherAgents []string          // agents that hold imported servers but are not in `agents`
	Sync        SyncReport
}

// MCPSetupConfig is the config file of the mcp-setup script.
func (a *App) MCPSetupConfig() string {
	return filepath.Join(a.Paths.Home, ".config", "mcp-setup", "config.json")
}

var (
	secretish  = regexp.MustCompile(`(?i)(key|token|secret|auth|password)`)
	camelBreak = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	notName    = regexp.MustCompile(`[^A-Za-z0-9]+`)
	authScheme = regexp.MustCompile(`^(Bearer|Basic|Token) (.+)$`)
	wholeRef   = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)
	flagValue  = regexp.MustCompile(`^(--?[A-Za-z][\w-]*)=(.+)$`)
)

// secretName spells words as an upper-case name: "tavilyApiKey" becomes
// TAVILY_API_KEY.
func secretName(words ...string) string {
	joined := camelBreak.ReplaceAllString(strings.Join(words, "_"), "${1}_${2}")
	name := strings.Trim(notName.ReplaceAllString(joined, "_"), "_")
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		name = "SECRET_" + name
	}
	return strings.ToUpper(name)
}

// ImportMCP takes the server definitions and presets of an mcp-setup config
// into the local catalog. Secret values move to the secrets file and leave
// ${NAME} placeholders behind: every environment and header value, and URL
// parameters and --flag=value arguments whose name looks like a credential.
// Nothing becomes enabled, and agent configs stay as they are, unless adopt
// is set: then the entries that agents hold under an imported name are
// handed to skillet, and the following sync removes those that are not
// enabled.
func (a *App) ImportMCP(file string, dryRun, adopt bool) (MCPImportReport, error) {
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
	store, err := secrets.Load(a.Paths.SecretsFile())
	if err != nil {
		return report, err
	}
	// keep stores a value under a free name and returns its placeholder.
	keep := func(server, name, value string) string {
		if value == "" || wholeRef.MatchString(value) {
			return value
		}
		if old, taken := store[name]; taken && old != value {
			name = secretName(server, name)
		}
		store[name] = value
		if !slices.Contains(report.Secrets, name) {
			report.Secrets = append(report.Secrets, name)
		}
		return "${" + name + "}"
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
		def := catalog.MCP{Type: in.Type, URL: in.URL, Transport: in.Transport,
			Timeout: in.Timeout, DisabledTools: in.DisabledTools}
		for _, arg := range in.Command {
			if m := flagValue.FindStringSubmatch(arg); m != nil && secretish.MatchString(m[1]) {
				arg = m[1] + "=" + keep(name, secretName(name, m[1]), m[2])
			}
			def.Command = append(def.Command, arg)
		}
		if parsed, err := url.Parse(in.URL); err == nil && parsed.RawQuery != "" {
			params := strings.Split(parsed.RawQuery, "&")
			for i, param := range params {
				key, value, _ := strings.Cut(param, "=")
				if secretish.MatchString(key) {
					params[i] = key + "=" + keep(name, secretName(key), value)
				}
			}
			def.URL = strings.Replace(in.URL, parsed.RawQuery, strings.Join(params, "&"), 1)
		}
		for key, value := range in.Environment {
			if def.Environment == nil {
				def.Environment = map[string]string{}
			}
			def.Environment[key] = keep(name, secretName(key), value)
		}
		for key, value := range in.Headers {
			if def.Headers == nil {
				def.Headers = map[string]string{}
			}
			scheme := ""
			if m := authScheme.FindStringSubmatch(value); m != nil {
				scheme, value = m[1]+" ", m[2]
			}
			def.Headers[key] = scheme + keep(name, secretName(name, key), value)
		}
		if err := def.Validate(name); err != nil {
			report.Skipped[name] = err.Error()
			continue
		}
		a.Local.MCPs[name] = &def
		report.Servers = append(report.Servers, name)
	}
	sort.Strings(report.Secrets)

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

	for agent := range mcp.Targets {
		present, err := mcp.Present(a.Paths.Home, agent)
		if err != nil {
			a.Warnings = append(a.Warnings, err.Error())
			continue
		}
		for _, server := range report.Servers {
			if _, found := present[server]; found && !slices.Contains(a.Local.Agents, agent) &&
				!slices.Contains(report.OtherAgents, agent) {
				report.OtherAgents = append(report.OtherAgents, agent)
			}
		}
	}
	sort.Strings(report.OtherAgents)

	if adopt {
		a.adoptMCPs = report.Servers
	}
	defer func() { a.adoptMCPs = nil }()
	if dryRun {
		a.dryRunSecrets = store
		defer func() { a.dryRunSecrets = nil }()
		err = a.merge()
	} else {
		if err = store.Save(a.Paths.SecretsFile()); err == nil {
			err = a.Save()
		}
	}
	if err != nil {
		return report, err
	}
	report.Sync, err = a.Sync(a.Global(), SyncOptions{DryRun: dryRun})
	return report, err
}
