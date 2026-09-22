package catalog

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var repoTail = regexp.MustCompile(`([\w.-]+)/([\w.-]+?)(\.git)?/?$`)

// RepoParts finds the owner and the repository in a git URL, an scp-style
// address or a local path: its last two path segments, without ".git".
func RepoParts(rawURL string) (owner, repo string, ok bool) {
	m := repoTail.FindStringSubmatch(strings.ReplaceAll(rawURL, ":", "/"))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// sourceNames gives every source its name: the explicit one, else the
// repository name from the URL, or "owner-repo" for every source without
// a name whose repository name another one shares. The result does not
// depend on the order of the sources.
func sourceNames(sources []*Source) ([]string, error) {
	names, owners := make([]string, len(sources)), make([]string, len(sources))
	count := map[string]int{}
	for i, src := range sources {
		if src.Name != "" {
			names[i] = src.Name
			continue
		}
		owner, repo, ok := RepoParts(src.URL)
		if !ok {
			return nil, fmt.Errorf("source %q: no name found in the url; set name", src.URL)
		}
		names[i], owners[i] = repo, owner
		count[repo]++
	}
	seen := map[string]int{}
	for i, src := range sources {
		if src.Name == "" && count[names[i]] > 1 {
			names[i] = owners[i] + "-" + names[i]
		}
		if j, dup := seen[names[i]]; dup {
			return nil, fmt.Errorf("sources %q and %q share the name %q; set name on one", sources[j].URL, src.URL, names[i])
		}
		seen[names[i]] = i
	}
	return names, nil
}

var (
	runners     = map[string]bool{"npx": true, "bunx": true, "pnpx": true, "uvx": true}
	runnerPairs = map[string]string{"uv": "run", "pnpm": "dlx", "yarn": "dlx", "pipx": "run", "docker": "run"}
	// valued lists the options of the runners that take the next argument
	// as their value.
	valued = map[string]bool{
		"-e": true, "--env": true, "-v": true, "--volume": true, "-p": true, "--publish": true,
		"--name": true, "--network": true, "--entrypoint": true, "--platform": true,
		"-u": true, "--user": true, "-w": true, "--workdir": true, "--mount": true,
		"--from": true, "--with": true, "--python": true, "--directory": true,
	}
	interpreters = map[string]bool{"node": true, "python": true, "python3": true, "bun": true}
)

// name gives the server its name: the explicit one, else one guessed from
// the command or the URL.
func (m *MCP) name() (string, error) {
	var name string
	switch {
	case m.Name != "":
		return m.Name, nil
	case len(m.Command) > 0:
		name = commandName(m.Command)
	case m.URL != "":
		name = hostName(m.URL)
	}
	if name == "" {
		return "", fmt.Errorf("mcp server %s: no name found; set name", strings.Join(append(m.Command, m.URL), " "))
	}
	return name, nil
}

// commandName guesses a server's name from its command: the package a
// runner such as npx or uvx starts, the image docker runs, the module or
// script an interpreter runs, or else the command itself.
func commandName(command []string) string {
	cmd, args := filepath.Base(command[0]), command[1:]
	switch {
	case runners[cmd]:
	case runnerPairs[cmd] != "" && len(args) > 0 && args[0] == runnerPairs[cmd]:
		args = args[1:]
	case interpreters[cmd]:
		if i := slices.Index(args, "-m"); i >= 0 && i+1 < len(args) {
			return args[i+1]
		}
		if script := firstOperand(args); script != "" {
			return strings.TrimSuffix(filepath.Base(script), filepath.Ext(script))
		}
		return cmd
	default:
		return cmd
	}
	return packageName(firstOperand(args))
}

// firstOperand skips options and the values they take.
func firstOperand(args []string) string {
	for i := 0; i < len(args); i++ {
		switch {
		case valued[args[i]]:
			i++
		case strings.HasPrefix(args[i], "-"):
		default:
			return args[i]
		}
	}
	return ""
}

// packageName reads the name out of a package or image spec: without the
// scope or registry path in front, and without the version, tag, digest
// or extras behind.
func packageName(spec string) string {
	spec = spec[strings.LastIndex(spec, "/")+1:]
	if i := strings.IndexAny(spec, "@:=["); i >= 0 {
		spec = spec[:i]
	}
	return spec
}

// hostName is the label before the top-level domain of the URL's host.
func hostName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	labels := strings.Split(u.Hostname(), ".")
	if len(labels) >= 2 {
		return labels[len(labels)-2]
	}
	return labels[0]
}
