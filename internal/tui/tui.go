// Package tui is the interactive front end. It keeps view state only and
// calls package app for every change.
package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bonkey/skillet/internal/app"
	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/importer"
	"github.com/bonkey/skillet/internal/paths"
	"github.com/bonkey/skillet/internal/source"
)

type mode int

const (
	modeList mode = iota
	modeFilter
	modeInput
	modeConfirm
	modePick
)

// row is a skill, an MCP server, or a pack header when both are empty.
type row struct{ pack, skill, mcp string }

// member names what a row stands for the way commands spell it.
func (r row) member() string {
	switch {
	case r.mcp != "":
		return catalog.MCPPrefix + r.mcp
	case r.skill != "":
		return r.skill
	}
	return "@" + r.pack
}

type picker struct {
	source, url string
	found       map[string]source.Skill
	names       []string
	picked      map[string]bool
	cursor      int
}

type Model struct {
	app   *app.App
	view  app.View
	scope app.Scope

	rows      []row
	cursor    int
	offset    int
	collapsed map[string]bool
	filter    string

	mode     mode
	input    textinput.Model
	label    string
	onSubmit func(*Model, string) tea.Cmd
	onYes    func(*Model) tea.Cmd
	pick     picker

	status string
	busy   bool
	width  int
	height int
}

type fetchedMsg struct {
	source, url string
	found       map[string]source.Skill
	err         error
}

type refMsg struct {
	source string
	err    error
}

type updatedMsg struct {
	updates []app.SourceUpdate
	err     error
}

func Run(a *app.App) error {
	m, err := New(a)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func New(a *app.App) (*Model, error) {
	m := &Model{app: a, scope: a.Global(), collapsed: map[string]bool{}, input: textinput.New(), width: 100, height: 30}
	return m, m.reload()
}

func (m *Model) Init() tea.Cmd { return nil }

// reload rereads the catalog view and rebuilds the visible rows.
func (m *Model) reload() error {
	view, err := m.app.View()
	if err != nil {
		return err
	}
	m.view = view
	m.rebuild()
	return nil
}

func (m *Model) rebuild() {
	needle := strings.ToLower(m.filter)
	m.rows = m.rows[:0]
	for _, pack := range m.view.Packs {
		packHit := needle != "" && strings.Contains(strings.ToLower(pack.Name), needle)
		var skills []string
		for _, name := range pack.Skills {
			skill := m.view.Skills[name]
			if needle == "" || packHit || strings.Contains(strings.ToLower(name+" "+skill.Description), needle) {
				skills = append(skills, name)
			}
		}
		var servers []string
		for _, name := range pack.MCPs {
			if needle == "" || packHit || strings.Contains(strings.ToLower("mcp:"+name+" "+m.view.MCPs[name].Target), needle) {
				servers = append(servers, name)
			}
		}
		if needle != "" && len(skills)+len(servers) == 0 {
			continue
		}
		m.rows = append(m.rows, row{pack: pack.Name})
		if needle == "" && m.collapsed[pack.Name] {
			continue
		}
		for _, name := range skills {
			m.rows = append(m.rows, row{pack: pack.Name, skill: name})
		}
		for _, name := range servers {
			m.rows = append(m.rows, row{pack: pack.Name, mcp: name})
		}
	}
	m.cursor = max(0, min(m.cursor, len(m.rows)-1))
}

func (m *Model) current() (row, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return row{}, false
	}
	return m.rows[m.cursor], true
}

func (m *Model) pack(name string) app.PackView {
	for _, pack := range m.view.Packs {
		if pack.Name == name {
			return pack
		}
	}
	return app.PackView{}
}

// enabled reports whether the active scope declares the skill enabled.
func (m *Model) enabled(skill string) bool {
	if m.scope.Project {
		return m.view.Skills[skill].Project
	}
	return m.view.Skills[skill].Global
}

// members counts what a pack holds in the active scope, and how much of it
// is enabled. Servers count in the global scope only.
func (m *Model) members(pack app.PackView) (enabled, total int) {
	for _, skill := range pack.Skills {
		total++
		if m.enabled(skill) {
			enabled++
		}
	}
	if !m.scope.Project {
		for _, server := range pack.MCPs {
			total++
			if m.view.MCPs[server].Global {
				enabled++
			}
		}
	}
	return enabled, total
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case fetchedMsg:
		m.busy, m.status = false, ""
		if msg.err != nil {
			m.status = "add failed: " + msg.err.Error()
			return m, nil
		}
		m.pick = picker{source: msg.source, url: msg.url, found: msg.found, picked: map[string]bool{}}
		for name := range msg.found {
			m.pick.names = append(m.pick.names, name)
		}
		sort.Strings(m.pick.names)
		m.mode = modePick
		return m, nil
	case refMsg:
		m.busy = false
		m.fail(m.reload())
		m.status = m.source(msg.source)
		m.fail(msg.err)
		return m, nil
	case updatedMsg:
		m.busy = false
		m.status = updateSummary(msg)
		m.fail(m.reload())
		return m, nil
	case tea.KeyMsg:
		if m.busy {
			return m, nil
		}
		switch m.mode {
		case modeFilter, modeInput:
			return m, m.updateInput(msg)
		case modeConfirm:
			return m, m.updateConfirm(msg)
		case modePick:
			return m, m.updatePick(msg)
		}
		return m, m.updateList(msg)
	}
	return m, nil
}

func (m *Model) fail(err error) {
	if err != nil {
		m.status = "error: " + err.Error()
	}
}

func (m *Model) updateList(msg tea.KeyMsg) tea.Cmd {
	m.status = ""
	switch msg.String() {
	case "q", "ctrl+c":
		return tea.Quit
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = min(len(m.rows)-1, m.cursor+1)
	case "pgup":
		m.cursor = max(0, m.cursor-m.listHeight())
	case "pgdown":
		m.cursor = min(len(m.rows)-1, m.cursor+m.listHeight())
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(m.rows) - 1
	case "left", "right", "enter":
		if r, ok := m.current(); ok {
			m.collapsed[r.pack] = msg.String() == "left" || (msg.String() == "enter" && !m.collapsed[r.pack])
			m.rebuild()
			for i, candidate := range m.rows {
				if candidate.pack == r.pack && candidate.skill == "" && m.collapsed[r.pack] {
					m.cursor = i
				}
			}
		}
	case " ":
		m.toggle()
	case "tab":
		m.switchScope()
	case "/":
		m.ask(modeFilter, "filter", m.filter, nil)
	case "esc":
		m.filter = ""
		m.rebuild()
	case "a":
		m.ask(modeInput, "add source (owner/repo or git URL)", "", func(m *Model, value string) tea.Cmd {
			if value == "" {
				return nil
			}
			m.busy, m.status = true, "cloning "+value+"…"
			return func() tea.Msg {
				name, url, found, err := m.app.Fetch(value, "")
				return fetchedMsg{source: name, url: url, found: found, err: err}
			}
		})
	case "d":
		m.askRemove()
	case "p":
		m.askPack()
	case "e":
		m.askDescribe()
	case "r":
		m.askRef()
	case "u":
		m.busy, m.status = true, "updating sources…"
		return func() tea.Msg {
			updates, err := m.app.Update(false)
			return updatedMsg{updates: updates, err: err}
		}
	}
	return nil
}

func (m *Model) toggle() {
	r, ok := m.current()
	if !ok {
		return
	}
	var names []string
	var enable bool
	switch {
	case r.mcp != "" && m.scope.Project:
		m.status = "MCP servers are global: switch the scope with tab, or use `skillet run`"
		return
	case r.mcp != "":
		names, enable = []string{r.member()}, !m.view.MCPs[r.mcp].Global
	case r.skill != "":
		names, enable = []string{r.skill}, !m.enabled(r.skill)
	default:
		pack := m.pack(r.pack)
		on, total := m.members(pack)
		enable = on < total
		names = []string{r.member()}
		if r.pack == app.NoPack {
			names = slices.Clone(pack.Skills)
			if !m.scope.Project {
				for _, server := range pack.MCPs {
					names = append(names, catalog.MCPPrefix+server)
				}
			}
		}
	}
	if len(names) == 0 {
		return
	}
	report, err := m.app.Toggle(m.scope, enable, names...)
	m.status = syncSummary(report)
	m.fail(err)
	m.fail(m.reload())
}

func (m *Model) switchScope() {
	if m.scope.Project {
		m.scope = m.app.Global()
		return
	}
	scope, err := m.app.ProjectScope()
	if err != nil {
		m.status = err.Error()
		return
	}
	m.scope = scope
	if m.view.Project == "" {
		m.status = "no " + paths.ManifestName + " here yet; the first change creates it"
	}
}

func (m *Model) ask(kind mode, label, initial string, onSubmit func(*Model, string) tea.Cmd) {
	m.mode, m.label, m.onSubmit = kind, label, onSubmit
	m.input.SetValue(initial)
	m.input.CursorEnd()
	m.input.Focus()
}

func (m *Model) updateInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		if m.mode == modeFilter {
			m.filter = ""
			m.rebuild()
		}
		m.mode = modeList
		return nil
	case "enter":
		value, submit := strings.TrimSpace(m.input.Value()), m.onSubmit
		m.mode = modeList
		if submit != nil {
			return submit(m, value)
		}
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.mode == modeFilter {
		m.filter = m.input.Value()
		m.rebuild()
	}
	return cmd
}

func (m *Model) confirm(question string, onYes func(*Model) tea.Cmd) {
	m.mode, m.label, m.onYes = modeConfirm, question, onYes
}

func (m *Model) updateConfirm(msg tea.KeyMsg) tea.Cmd {
	m.mode = modeList
	if msg.String() == "y" {
		return m.onYes(m)
	}
	m.status = "cancelled"
	return nil
}

func (m *Model) askRemove() {
	r, ok := m.current()
	if !ok || (r.skill == "" && r.mcp == "" && r.pack == app.NoPack) {
		return
	}
	target, question := r.member(), fmt.Sprintf("remove %s from the catalog?", r.member())
	if r.skill == "" && r.mcp == "" {
		pack := m.pack(r.pack)
		question = fmt.Sprintf("remove pack %s with its %d skills and %d servers from the catalog?", r.pack, len(pack.Skills), len(pack.MCPs))
	}
	m.confirm(question+" (y/n)", func(m *Model) tea.Cmd {
		_, err := m.app.Remove(target)
		m.status = "removed " + target
		m.fail(err)
		m.fail(m.reload())
		return nil
	})
}

// askPack toggles the selected skill's membership in a pack, creating the
// pack when needed.
func (m *Model) askPack() {
	r, ok := m.current()
	if !ok || (r.skill == "" && r.mcp == "") {
		m.status = "select a skill or a server first"
		return
	}
	member := r.member()
	m.ask(modeInput, "pack to add "+member+" to, or to take it out of", "", func(m *Model, name string) tea.Cmd {
		name = strings.TrimPrefix(name, "@")
		if name == "" {
			return nil
		}
		pack, exists := m.app.Catalog.Packs[name]
		if !exists {
			m.ask(modeInput, "description of new pack "+name, "", func(m *Model, description string) tea.Cmd {
				m.editPack(name, true, func(local *catalog.Catalog) error {
					return local.CreatePack(name, description, []string{member})
				})
				return nil
			})
			return nil
		}
		inPack := slices.Contains(pack.Skills, r.skill) || slices.Contains(pack.MCPs, r.mcp)
		m.editPack(name, false, func(local *catalog.Catalog) error {
			if inPack {
				return local.PackRemove(name, []string{member})
			}
			return local.PackAdd(name, []string{member})
		})
		return nil
	})
}

func (m *Model) askDescribe() {
	r, ok := m.current()
	if !ok || r.pack == app.NoPack {
		return
	}
	m.ask(modeInput, "description of pack "+r.pack, m.pack(r.pack).Description, func(m *Model, value string) tea.Cmd {
		if value == "" {
			m.status = "a pack needs a description"
			return nil
		}
		m.editPack(r.pack, false, func(local *catalog.Catalog) error {
			local.Packs[r.pack].Description = value
			return nil
		})
		return nil
	})
}

func (m *Model) editPack(name string, create bool, edit func(local *catalog.Catalog) error) {
	m.fail(m.app.EditPack(name, create, edit))
	m.fail(m.reload())
}

// askRef changes what the selected skill's source tracks.
func (m *Model) askRef() {
	r, ok := m.current()
	if !ok || r.skill == "" {
		m.status = "select a skill first"
		return
	}
	skill := m.view.Skills[r.skill]
	label := "branch, tag or full commit for " + skill.Source + " (empty for the default branch)"
	m.ask(modeInput, label, skill.Ref, func(m *Model, ref string) tea.Cmd {
		if ref == skill.Ref {
			return nil
		}
		m.busy, m.status = true, "fetching "+skill.Source+"…"
		return func() tea.Msg { return refMsg{source: skill.Source, err: m.app.SetRef(skill.Source, ref)} }
	})
}

func (m *Model) updatePick(msg tea.KeyMsg) tea.Cmd {
	p := &m.pick
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode, m.status = modeList, "cancelled"
		m.app.DropUnusedClone(p.source)
	case "up", "k":
		p.cursor = max(0, p.cursor-1)
	case "down", "j":
		p.cursor = min(len(p.names)-1, p.cursor+1)
	case " ":
		if len(p.names) > 0 {
			p.picked[p.names[p.cursor]] = !p.picked[p.names[p.cursor]]
		}
	case "a":
		all := len(p.picked) < len(p.names)
		p.picked = map[string]bool{}
		for _, name := range p.names {
			if all {
				p.picked[name] = true
			}
		}
	case "enter":
		var skills []string
		for _, name := range p.names {
			if p.picked[name] {
				skills = append(skills, name)
			}
		}
		if len(skills) == 0 {
			m.status = "pick at least one skill (space), or esc to cancel"
			return nil
		}
		m.askAddPack(skills)
	}
	return nil
}

func (m *Model) askAddPack(skills []string) {
	req := app.AddRequest{Source: m.pick.source, URL: m.pick.url, Skills: skills}
	add := func(m *Model) {
		if err := m.app.Add(req); err != nil {
			m.app.DropUnusedClone(req.Source)
			m.fail(err)
			return
		}
		m.status = fmt.Sprintf("added %d skills from %s (disabled)", len(skills), req.Source)
		m.fail(m.reload())
	}
	m.ask(modeInput, "pack for these skills (empty for none)", importer.PackName(req.Source), func(m *Model, name string) tea.Cmd {
		req.Pack = strings.TrimPrefix(name, "@")
		if _, exists := m.app.Local.Packs[req.Pack]; req.Pack == "" || exists {
			add(m)
			return nil
		}
		m.ask(modeInput, "description of new pack "+req.Pack, "", func(m *Model, description string) tea.Cmd {
			req.PackDescription = description
			add(m)
			return nil
		})
		return nil
	})
}

func syncSummary(report app.SyncReport) string {
	var conflicts int
	for _, action := range report.Actions {
		if action.Op == "conflict" {
			conflicts++
		}
	}
	for _, action := range report.MCP {
		if action.Op == "mcp-conflict" {
			conflicts++
		}
	}
	var parts []string
	if conflicts > 0 {
		parts = append(parts, fmt.Sprintf("%d conflicts with unmanaged entries", conflicts))
	}
	if len(report.Missing) > 0 {
		parts = append(parts, "missing from source: "+strings.Join(report.Missing, ", "))
	}
	for server, names := range report.MissingSecrets {
		parts = append(parts, fmt.Sprintf("mcp:%s needs `skillet secret set %s`", server, strings.Join(names, " ")))
	}
	parts = append(parts, report.Notes...)
	return strings.Join(parts, "; ")
}

func updateSummary(msg updatedMsg) string {
	if msg.err != nil {
		return "update failed: " + msg.err.Error()
	}
	var changed []string
	failed := 0
	for _, u := range msg.updates {
		changed = append(changed, u.Changed...)
		if u.Err != nil {
			failed++
		}
	}
	status := fmt.Sprintf("updated %d sources", len(msg.updates))
	if len(changed) > 0 {
		status += "; changed: " + strings.Join(changed, ", ")
	}
	if failed > 0 {
		status += fmt.Sprintf("; %d failed", failed)
	}
	return status
}

var (
	styleTitle  = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Faint(true)
	styleCursor = lipgloss.NewStyle().Reverse(true)
	stylePack   = lipgloss.NewStyle().Bold(true)
	styleWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	stylePane   = lipgloss.NewStyle().PaddingLeft(2)
)

func (m *Model) listHeight() int { return max(3, m.height-4) }

func (m *Model) View() string {
	if m.mode == modePick {
		return m.viewPick()
	}
	scope := strings.Replace(m.scope.String(), m.app.Paths.Home, "~", 1)
	if m.filter != "" && m.mode != modeFilter {
		scope += "  filter: " + m.filter
	}
	header := styleTitle.Render(truncate("skillet  scope: "+scope, m.width-16)) + styleDim.Render("  (tab switches)")

	leftWidth := max(30, m.width/2)
	height := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+height {
		m.offset = m.cursor - height + 1
	}
	var lines []string
	for i := m.offset; i < min(len(m.rows), m.offset+height); i++ {
		line := truncate(m.rowText(m.rows[i]), leftWidth)
		if i == m.cursor {
			line = styleCursor.Render(fmt.Sprintf("%-*s", leftWidth, line))
		} else if m.rows[i].skill == "" && m.rows[i].mcp == "" {
			line = stylePack.Render(line)
		}
		lines = append(lines, line)
	}
	if len(m.rows) == 0 {
		lines = append(lines, styleDim.Render("the catalog is empty: press a to add a source, or run `skillet import`"))
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	left := lipgloss.NewStyle().Width(leftWidth).Render(strings.Join(lines, "\n"))
	right := stylePane.Width(max(20, m.width-leftWidth-2)).Height(height).MaxHeight(height).Render(m.detail())

	var footer string
	switch m.mode {
	case modeFilter, modeInput:
		footer = m.label + ": " + m.input.View()
	case modeConfirm:
		footer = styleWarn.Render(m.label)
	default:
		footer = styleDim.Render(truncate("space toggle · / filter · a add source · d remove · p pack membership · e pack description · r ref · u update · ←/→ fold · q quit", m.width))
	}
	status := styleWarn.Render(truncate(m.status, m.width))
	return strings.Join([]string{header, lipgloss.JoinHorizontal(lipgloss.Top, left, right), status, footer}, "\n")
}

func (m *Model) rowText(r row) string {
	if r.skill == "" && r.mcp == "" {
		pack := m.pack(r.pack)
		on, total := m.members(pack)
		fold := "▾"
		if m.collapsed[r.pack] && m.filter == "" {
			fold = "▸"
		}
		box := "[ ]"
		if on == total && on > 0 {
			box = "[x]"
		} else if on > 0 {
			box = "[-]"
		}
		name := "@" + r.pack
		if r.pack == app.NoPack {
			name = "(no pack)"
		} else if strings.TrimSpace(pack.Description) == "" {
			name += " !"
		}
		return fmt.Sprintf("%s %s %s  %d/%d enabled", fold, box, name, on, total)
	}
	if r.mcp != "" {
		server := m.view.MCPs[r.mcp]
		box := "[ ]"
		if server.Global {
			box = "[x]"
		}
		note := server.Target
		if len(server.MissingSecrets) > 0 {
			note = "? needs secret " + strings.Join(server.MissingSecrets, ", ")
		}
		return fmt.Sprintf("    %s mcp:%s — %s", box, r.mcp, note)
	}
	skill := m.view.Skills[r.skill]
	box := "[ ]"
	switch {
	case m.enabled(r.skill):
		box = "[x]"
	case m.scope.Project && skill.Global:
		box = "[g]"
	}
	note := skill.Description
	if skill.Missing {
		note = "? missing from its source"
	} else if note == "" {
		note = "! no description"
	}
	return fmt.Sprintf("    %s %s — %s", box, r.skill, note)
}

func (m *Model) detail() string {
	r, ok := m.current()
	if !ok {
		return ""
	}
	if r.mcp != "" {
		server := m.view.MCPs[r.mcp]
		lines := []string{styleTitle.Render("mcp:" + server.Name), "", server.Type + " MCP server", server.Target, ""}
		if len(server.MissingSecrets) > 0 {
			lines = append(lines, styleWarn.Render("no value for "+strings.Join(server.MissingSecrets, ", ")+
				" — run `skillet secret set <NAME>`"), "")
		}
		packs := "none"
		if len(server.Packs) > 0 {
			packs = "@" + strings.Join(server.Packs, ", @")
		}
		info := "packs:  " + packs + "\nglobal: " + onOff(server.Global) + "\nServers are global; `skillet run` adds them for one command."
		if server.From != "" {
			info = "from:   gist " + server.From + "\n" + info
		}
		return strings.Join(lines, "\n") + styleDim.Render(info)
	}
	if r.skill == "" {
		pack := m.pack(r.pack)
		title := "@" + r.pack
		if r.pack == app.NoPack {
			title = "(no pack)"
		}
		description := pack.Description
		if strings.TrimSpace(description) == "" {
			description = styleWarn.Render("no description — press e to write one")
		}
		if pack.From != "" {
			description += "\n\n" + styleDim.Render("from gist "+pack.From)
		}
		members := slices.Clone(pack.Skills)
		for _, server := range pack.MCPs {
			members = append(members, catalog.MCPPrefix+server)
		}
		return styleTitle.Render(title) + "\n\n" + description + "\n\n" + styleDim.Render(strings.Join(members, ", "))
	}
	skill := m.view.Skills[r.skill]
	description := skill.Description
	switch {
	case skill.Missing:
		description = styleWarn.Render("not found in the clone of " + skill.Source + " — run update, or remove it")
	case description == "":
		description = styleWarn.Render("the skill's SKILL.md has no description")
	}
	state := []string{"global: " + onOff(skill.Global)}
	if m.view.Project != "" || m.scope.Project {
		state = append(state, "project: "+onOff(skill.Project))
	}
	packs := "none"
	if len(skill.Packs) > 0 {
		packs = "@" + strings.Join(skill.Packs, ", @")
	}
	from := ""
	if skill.From != "" {
		from = "\nfrom:   gist " + skill.From
	}
	return styleTitle.Render(skill.Name) + "\n\n" + description + "\n\n" +
		styleDim.Render("source: "+m.source(skill.Source)+from+"\npacks:  "+packs+"\n"+strings.Join(state, " · "))
}

// source describes a source with the ref it tracks and the commit it is at.
func (m *Model) source(name string) string {
	for _, src := range m.app.Sources() {
		if src.Name != name {
			continue
		}
		ref, commit := src.Ref, src.Commit
		if ref == "" {
			ref = "default branch"
		}
		if len(commit) > 8 {
			commit = commit[:8]
		}
		return fmt.Sprintf("%s @ %s (%s)", name, ref, commit)
	}
	return name
}

func (m *Model) viewPick() string {
	p := m.pick
	lines := []string{styleTitle.Render("skills in "+p.source) + styleDim.Render("  space pick · a all/none · enter continue · esc cancel"), ""}
	height := max(3, m.height-4)
	start := max(0, min(p.cursor-height/2, len(p.names)-height))
	for i := start; i < min(len(p.names), start+height); i++ {
		name := p.names[i]
		box := "[ ]"
		if p.picked[name] {
			box = "[x]"
		}
		line := fmt.Sprintf("%s %s — %s", box, name, p.found[name].Description)
		if m.app.Catalog.HasSkill(name) {
			line += " (in catalog)"
		}
		line = truncate(line, m.width)
		if i == p.cursor {
			line = styleCursor.Render(line)
		}
		lines = append(lines, line)
	}
	if len(p.names) == 0 {
		lines = append(lines, "no SKILL.md found in this source")
	}
	return strings.Join(append(lines, "", styleWarn.Render(m.status)), "\n")
}

func onOff(on bool) string {
	if on {
		return "enabled"
	}
	return "disabled"
}

func truncate(text string, width int) string {
	runes := []rune(text)
	if width < 2 || len(runes) <= width {
		return text
	}
	return string(runes[:width-1]) + "…"
}
