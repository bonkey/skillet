package app

import (
	"regexp"
	"slices"
	"strings"

	"github.com/bonkey/skillet/internal/paths"
	"github.com/bonkey/skillet/internal/secrets"
)

// OriginName describes the `from` of a view entry.
func OriginName(from string) string {
	if from == paths.ManifestName {
		return "project"
	}
	return "gist " + from
}

// NoPack groups the skills that belong to no pack.
const NoPack = ""

type SkillView struct {
	Name        string   `json:"name"`
	Source      string   `json:"source"`
	Packs       []string `json:"packs"`
	Description string   `json:"description"`
	Global      bool     `json:"global"`
	Project     bool     `json:"project"`
	Missing     bool     `json:"missing"`        // not found in the source's clone
	From        string   `json:"from,omitempty"` // the included gist or the manifest it comes from
	Ref         string   `json:"ref,omitempty"`  // what the source tracks; empty for the default branch
	Commit      string   `json:"commit,omitempty"`
}

// MCPView describes one MCP server. Servers exist in the global scope only.
type MCPView struct {
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	Target         string   `json:"target"` // the command line or the URL, placeholders unexpanded
	Packs          []string `json:"packs"`
	Global         bool     `json:"global"`
	MissingSecrets []string `json:"missing_secrets,omitempty"`
	From           string   `json:"from,omitempty"` // the included gist or the manifest it comes from
}

type PackView struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Skills      []string `json:"skills"`
	MCPs        []string `json:"mcps,omitempty"`
	From        string   `json:"from,omitempty"` // the included gist or the manifest it comes from
}

type View struct {
	Project string               `json:"project,omitempty"` // project root; absent in the home directory
	Packs   []PackView           `json:"packs"`
	Skills  map[string]SkillView `json:"skills"`
	MCPs    map[string]MCPView   `json:"mcps,omitempty"`
}

// View describes the catalog with the enabled state of the global scope and
// of the surrounding project, if there is one.
func (a *App) View() (View, error) {
	view := View{Skills: map[string]SkillView{}, MCPs: map[string]MCPView{}}
	globalSet, err := a.Set(a.Global())
	if err != nil {
		return view, err
	}
	global := a.Catalog.Resolve(globalSet)
	var project []string
	if scope, err := a.ProjectScope(); err == nil {
		view.Project = scope.Root
		if project, err = a.Enabled(scope); err != nil {
			return view, err
		}
	}
	var loose []string
	for _, name := range a.Catalog.SkillNames() {
		found, ok := a.Index.Lookup(a.Catalog, name)
		source, _ := a.Catalog.SourceOf(name)
		skill := SkillView{
			Name: name, Source: source, Packs: a.Catalog.PacksOf(name), Description: found.Description,
			Global: slices.Contains(global, name), Project: slices.Contains(project, name), Missing: !ok,
			From: a.Catalog.SkillOrigin[name], Ref: a.Catalog.Sources[source].Ref,
		}
		if indexed, ok := a.Index.Sources[source]; ok {
			skill.Commit = indexed.Head
		}
		view.Skills[name] = skill
		if len(skill.Packs) == 0 {
			loose = append(loose, name)
		}
	}
	store, err := secrets.Load(a.Paths.SecretsFile())
	if err != nil {
		return view, err
	}
	enabledMCPs := a.Catalog.ResolveMCPs(globalSet)
	var looseMCPs []string
	for _, name := range a.Catalog.MCPNames() {
		def := a.Catalog.MCPs[name]
		// With 1Password items configured, a name outside the local store may
		// still resolve, and a listing does not read items to find out.
		var missing []string
		if len(a.Catalog.Secrets) == 0 {
			_, missing = expandMCP(*def, store.Expand)
		}
		server := MCPView{
			Name: name, Type: def.Type, Target: def.URL, Packs: a.Catalog.MCPPacksOf(name),
			Global: slices.Contains(enabledMCPs, name), MissingSecrets: missing, From: a.Catalog.MCPOrigin[name],
		}
		if def.Type == "local" {
			server.Target = strings.Join(def.Command, " ")
		}
		view.MCPs[name] = server
		if len(server.Packs) == 0 {
			looseMCPs = append(looseMCPs, name)
		}
	}
	for _, name := range a.Catalog.PackNames() {
		pack := a.Catalog.Packs[name]
		view.Packs = append(view.Packs, PackView{Name: name, Description: pack.Description,
			Skills: a.Catalog.PackSkills(name), MCPs: slices.Clone(pack.MCPs), From: a.Catalog.PackOrigin[name]})
	}
	if len(loose)+len(looseMCPs) > 0 {
		view.Packs = append(view.Packs, PackView{Name: NoPack,
			Description: "Skills and servers that belong to no pack", Skills: loose, MCPs: looseMCPs})
	}
	return view, nil
}

type SourceView struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Ref    string `json:"ref,omitempty"`
	Commit string `json:"commit,omitempty"` // empty when the source is not cloned
	Skills int    `json:"skills"`
}

// Sources describes every source with the commit its clone is at.
func (a *App) Sources() []SourceView {
	var out []SourceView
	for _, name := range a.SourceNames() {
		src := a.Catalog.Sources[name]
		view := SourceView{Name: name, URL: src.URL, Ref: src.Ref, Skills: len(src.Skills)}
		if indexed, ok := a.Index.Sources[name]; ok {
			view.Commit = indexed.Head
		}
		out = append(out, view)
	}
	return out
}

// Filter narrows a view to one pack, to enabled entries, and to skills and
// servers that match every term. A term is a case-insensitive regular
// expression, or plain text when it does not compile. It is matched against
// the entry's name and description (for a server: its command or URL) and
// the name and description of its pack. Packs left empty are dropped once
// any filter applies.
func (v View) Filter(pack string, enabledOnly bool, terms []string) View {
	var patterns []*regexp.Regexp
	for _, term := range terms {
		re, err := regexp.Compile("(?i)" + term)
		if err != nil {
			re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(term))
		}
		patterns = append(patterns, re)
	}
	matches := func(fields ...string) bool {
		text := strings.Join(fields, " ")
		return !slices.ContainsFunc(patterns, func(re *regexp.Regexp) bool { return !re.MatchString(text) })
	}
	filtering := enabledOnly || len(patterns) > 0
	out := View{Project: v.Project, Skills: map[string]SkillView{}, MCPs: map[string]MCPView{}}
	for _, p := range v.Packs {
		if pack != "" && p.Name != pack {
			continue
		}
		var skills, servers []string
		for _, name := range p.Skills {
			skill := v.Skills[name]
			if enabledOnly && !skill.Global && !skill.Project {
				continue
			}
			if matches(skill.Name, skill.Description, p.Name, p.Description) {
				skills = append(skills, name)
				out.Skills[name] = skill
			}
		}
		for _, name := range p.MCPs {
			server := v.MCPs[name]
			if enabledOnly && !server.Global {
				continue
			}
			if matches(server.Name, "mcp", server.Target, p.Name, p.Description) {
				servers = append(servers, name)
				out.MCPs[name] = server
			}
		}
		if len(skills)+len(servers) > 0 || !filtering {
			p.Skills, p.MCPs = skills, servers
			out.Packs = append(out.Packs, p)
		}
	}
	return out
}
