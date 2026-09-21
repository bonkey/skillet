package mcp

import "github.com/bonkey/skillet/internal/catalog"

// Target is where and how one agent keeps its user-level MCP servers.
type Target struct {
	File   string   // relative to the home directory
	Node   []string // path of the object, or TOML table, that holds the servers
	TOML   bool
	Render func(def catalog.MCP) Entry
}

// Targets lists the agents that skillet can configure, by agent name. The
// shapes follow each agent's documentation or config source:
//
//	claude-code  https://code.claude.com/docs/en/mcp
//	codex        codex-rs/config/src/mcp_types.rs in github.com/openai/codex
//	crush        schema.json in github.com/charmbracelet/crush ($defs.MCPConfig)
//	cursor       https://cursor.com/docs/context/mcp
//	gemini-cli   docs/tools/mcp-server.md in github.com/google-gemini/gemini-cli
//	opencode     https://opencode.ai/config.json (McpLocalConfig, McpRemoteConfig)
//	zed          crates/settings_content/src/project.rs in github.com/zed-industries/zed
//
// Codex, opencode and crush reject unknown keys, so an entry holds only
// documented ones. Timeouts are seconds in the catalog; each agent gets its
// own unit.
var Targets = map[string]Target{
	"claude-code": {File: ".claude.json", Node: []string{"mcpServers"}, Render: renderClaude},
	"codex":       {File: ".codex/config.toml", Node: []string{"mcp_servers"}, TOML: true, Render: renderCodex},
	"crush":       {File: ".config/crush/crush.json", Node: []string{"mcp"}, Render: renderCrush},
	"cursor":      {File: ".cursor/mcp.json", Node: []string{"mcpServers"}, Render: renderCursor},
	"gemini-cli":  {File: ".gemini/settings.json", Node: []string{"mcpServers"}, Render: renderGemini},
	"opencode":    {File: ".config/opencode/opencode.json", Node: []string{"mcp"}, Render: renderOpencode},
	"zed":         {File: ".config/zed/settings.json", Node: []string{"context_servers"}, Render: renderZed},
}

func transport(def catalog.MCP) string {
	if def.Transport == "sse" {
		return "sse"
	}
	return "http"
}

// commandArgsEnv is the stdio shape most agents share.
func commandArgsEnv(e *Entry, def catalog.MCP, envKey string) {
	e.add("command", def.Command[0])
	e.add("args", append([]string{}, def.Command[1:]...))
	e.addMap(envKey, def.Environment)
}

func renderClaude(def catalog.MCP) Entry {
	var e Entry
	if def.Type == "local" {
		e.add("type", "stdio")
		commandArgsEnv(&e, def, "env")
	} else {
		e.add("type", transport(def)) // required for a remote server
		e.add("url", def.URL)
		e.addMap("headers", def.Headers)
	}
	if def.Timeout > 0 {
		e.add("timeout", def.Timeout*1000) // milliseconds
	}
	return e
}

func renderCursor(def catalog.MCP) Entry {
	var e Entry
	if def.Type == "local" {
		commandArgsEnv(&e, def, "env")
		return e
	}
	e.add("url", def.URL)
	e.addMap("headers", def.Headers)
	return e
}

// renderGemini uses the documented keys: httpUrl for streamable HTTP, url
// for SSE.
func renderGemini(def catalog.MCP) Entry {
	var e Entry
	switch {
	case def.Type == "local":
		commandArgsEnv(&e, def, "env")
	case transport(def) == "sse":
		e.add("url", def.URL)
		e.addMap("headers", def.Headers)
	default:
		e.add("httpUrl", def.URL)
		e.addMap("headers", def.Headers)
	}
	if def.Timeout > 0 {
		e.add("timeout", def.Timeout*1000) // milliseconds
	}
	if len(def.DisabledTools) > 0 {
		e.add("excludeTools", def.DisabledTools)
	}
	return e
}

func renderOpencode(def catalog.MCP) Entry {
	var e Entry
	e.add("type", def.Type)
	if def.Type == "local" {
		e.add("command", def.Command)
		e.addMap("environment", def.Environment)
	} else {
		e.add("url", def.URL)
		e.addMap("headers", def.Headers)
	}
	if def.Timeout > 0 {
		e.add("timeout", def.Timeout*1000) // milliseconds
	}
	e.add("enabled", true)
	return e
}

func renderCodex(def catalog.MCP) Entry {
	var e Entry
	if def.Type == "local" {
		commandArgsEnv(&e, def, "env")
	} else {
		e.add("url", def.URL) // streamable HTTP; Codex has no SSE transport
		e.addMap("http_headers", def.Headers)
	}
	if def.Timeout > 0 {
		e.add("tool_timeout_sec", def.Timeout)
	}
	if len(def.DisabledTools) > 0 {
		e.add("disabled_tools", def.DisabledTools)
	}
	return e
}

func renderCrush(def catalog.MCP) Entry {
	var e Entry
	if def.Type == "local" {
		e.add("type", "stdio")
		commandArgsEnv(&e, def, "env")
	} else {
		e.add("type", transport(def))
		e.add("url", def.URL)
		e.addMap("headers", def.Headers)
	}
	if def.Timeout > 0 {
		e.add("timeout", def.Timeout)
	}
	if len(def.DisabledTools) > 0 {
		e.add("disabled_tools", def.DisabledTools)
	}
	e.add("disabled", false)
	return e
}

func renderZed(def catalog.MCP) Entry {
	var e Entry
	e.add("enabled", true)
	if def.Type == "local" {
		commandArgsEnv(&e, def, "env")
	} else {
		e.add("url", def.URL)
		e.addMap("headers", def.Headers)
	}
	if def.Timeout > 0 {
		e.add("timeout", def.Timeout) // seconds
	}
	return e
}
