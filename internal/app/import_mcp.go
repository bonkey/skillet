package app

import (
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/mcp"
	"github.com/bonkey/skillet/internal/secrets"
)

// importServers takes the servers of the agents' configs into the catalog.
// Entries skillet manages already, and names the catalog holds, are left
// out. Values that look like secrets become ${NAME} placeholders whose
// values go into secrets.toml.
func (a *App) importServers(report *ImportReport, dryRun bool) error {
	state, err := mcp.LoadState(a.Paths.MCPStateFile())
	if err != nil {
		return err
	}
	store, err := secrets.Load(a.Paths.SecretsFile())
	if err != nil {
		return err
	}
	added := map[string]bool{}
	for _, agent := range mcp.Readable() {
		file := filepath.Join(a.Paths.Home, filepath.FromSlash(mcp.Targets[agent].File))
		defs, invalid, err := mcp.Read(a.Paths.Home, agent)
		if err != nil {
			return err
		}
		for _, name := range invalid {
			report.Skipped[catalog.MCPPrefix+name] = "not a usable entry in " + file
		}
		for _, name := range sortedKeys(defs) {
			key := catalog.MCPPrefix + name
			if _, managed := state[file][name]; managed {
				continue
			}
			if _, known := a.Local.MCPs[name]; known {
				if !slices.Contains(report.Servers, name) {
					report.Skipped[key] = "already in the catalog"
				}
				continue
			}
			def := hideSecrets(name, defs[name], store, added)
			if guessed, _ := def.GuessName(); guessed != name {
				def.Name = name
			}
			if err := def.Validate(name); err != nil {
				report.Skipped[key] = err.Error()
				continue
			}
			a.Local.MCPs[name] = &def
			report.Servers = append(report.Servers, name)
		}
	}
	report.Secrets = sortedKeys(added)
	// The values are known to this process either way, so a dry run's sync
	// does not report them as missing.
	a.pendingSecrets = secrets.Store{}
	for name := range added {
		a.pendingSecrets[name] = store[name]
	}
	if dryRun || len(added) == 0 {
		return nil
	}
	return store.Save(a.Paths.SecretsFile())
}

var secretKey = regexp.MustCompile(`(?i)key|token|secret|passw|auth|credential`)

// hideSecrets replaces the values of secret-looking environment variables,
// headers and URL query parameters with ${NAME} placeholders, and records
// the values in the store. added collects the names it wrote.
func hideSecrets(server string, def catalog.MCP, store secrets.Store, added map[string]bool) catalog.MCP {
	place := func(base, value string) string {
		name := upperSnake(base)
		if held, ok := store[name]; ok && held != value {
			name = upperSnake(server) + "_" + name
		}
		for held, ok := store[name]; ok && held != value; held, ok = store[name] {
			name += "_"
		}
		store[name], added[name] = value, true
		return "${" + name + "}"
	}
	isPlaceholder := func(value string) bool { return strings.Contains(value, "${") }
	if len(def.Environment) > 0 {
		env := map[string]string{}
		for k, v := range def.Environment {
			if secretKey.MatchString(k) && !isPlaceholder(v) {
				v = place(k, v)
			}
			env[k] = v
		}
		def.Environment = env
	}
	if len(def.Headers) > 0 {
		headers := map[string]string{}
		for k, v := range def.Headers {
			if secretKey.MatchString(k) && !isPlaceholder(v) {
				v = place(server+" "+k, v)
			}
			headers[k] = v
		}
		def.Headers = headers
	}
	if u, err := url.Parse(def.URL); err == nil && u.RawQuery != "" {
		var params []string
		for _, param := range strings.Split(u.RawQuery, "&") {
			k, v, has := strings.Cut(param, "=")
			if has && secretKey.MatchString(k) && !isPlaceholder(v) {
				if raw, err := url.QueryUnescape(v); err == nil {
					v = raw
				}
				param = k + "=" + place(k, v)
			}
			params = append(params, param)
		}
		u.RawQuery = strings.Join(params, "&")
		def.URL = u.String()
	}
	return def
}

// upperSnake turns "tavilyApiKey", "api-key" or "tavily Authorization" into
// TAVILY_API_KEY, API_KEY and TAVILY_AUTHORIZATION.
func upperSnake(s string) string {
	var b strings.Builder
	var prev rune
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			if prev >= 'a' && prev <= 'z' || prev >= '0' && prev <= '9' {
				b.WriteByte('_')
			}
			b.WriteRune(r)
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			if b.Len() > 0 && prev != '_' {
				b.WriteByte('_')
				r = '_'
			}
		}
		prev = r
	}
	return strings.Trim(b.String(), "_")
}

func sortedKeys[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
