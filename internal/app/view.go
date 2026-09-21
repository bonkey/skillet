package app

import "slices"

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
	From        string   `json:"from,omitempty"` // the included gist it comes from
	Ref         string   `json:"ref,omitempty"`  // what the source tracks; empty for the default branch
	Commit      string   `json:"commit,omitempty"`
}

type PackView struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Skills      []string `json:"skills"`
	From        string   `json:"from,omitempty"` // the included gist it comes from
}

type View struct {
	Project string               `json:"project,omitempty"` // project root, when inside one
	Packs   []PackView           `json:"packs"`
	Skills  map[string]SkillView `json:"skills"`
}

// View describes the catalog with the enabled state of the global scope and
// of the surrounding project, if there is one.
func (a *App) View() (View, error) {
	view := View{Skills: map[string]SkillView{}}
	global, err := a.Enabled(a.Global())
	if err != nil {
		return view, err
	}
	var project []string
	if root, ok := a.Paths.ProjectRoot(); ok {
		view.Project = root
		if project, err = a.Enabled(Scope{Project: true, Root: root}); err != nil {
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
	for _, name := range a.Catalog.PackNames() {
		pack := a.Catalog.Packs[name]
		view.Packs = append(view.Packs, PackView{Name: name, Description: pack.Description,
			Skills: slices.Clone(pack.Skills), From: a.Catalog.PackOrigin[name]})
	}
	if len(loose) > 0 {
		view.Packs = append(view.Packs, PackView{Name: NoPack, Description: "Skills that belong to no pack", Skills: loose})
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
