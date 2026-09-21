// Package tui is the interactive front end. It keeps view state only and
// calls package app for every change.
package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bonkey/skillet/internal/app"
	"github.com/bonkey/skillet/internal/catalog"
)

type mode int

const (
	modeList mode = iota
	modeFilter
	modeConfirm
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

type Model struct {
	app   *app.App
	view  app.View
	scope app.Scope

	rows      []row
	cursor    int
	offset    int
	collapsed map[string]bool
	filter    string

	// marks holds the changes that wait for the confirmation, per scope
	// (keyed by Scope.Project): a skill, or a server as "mcp:name", and
	// whether it gets enabled.
	marks map[bool]map[string]bool

	mode     mode
	input    textinput.Model
	question string
	onYes    func(*Model) tea.Cmd

	status string
	busy   bool
	width  int
	height int
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
	m := &Model{app: a, scope: a.Global(), collapsed: map[string]bool{}, marks: map[bool]map[string]bool{false: {}, true: {}}, input: textinput.New(), width: 100, height: 30}
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
	for _, marks := range m.marks {
		for member := range marks {
			server, isMCP := strings.CutPrefix(member, catalog.MCPPrefix)
			if _, ok := view.Skills[member]; !ok && (!isMCP || view.MCPs[server].Name == "") {
				delete(marks, member)
			}
		}
	}
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

// enabled reports whether a skill, or a server as "mcp:name", is enabled in
// the active scope.
func (m *Model) enabled(member string) bool {
	if server, ok := strings.CutPrefix(member, catalog.MCPPrefix); ok {
		return m.view.MCPs[server].Global
	}
	if m.scope.Project {
		return m.view.Skills[member].Project
	}
	return m.view.Skills[member].Global
}

// on reports the state a member has once the marks are applied.
func (m *Model) on(member string) bool {
	if enable, marked := m.marks[m.scope.Project][member]; marked {
		return enable
	}
	return m.enabled(member)
}

// packMembers lists what a pack holds in the active scope. Servers count in
// the global scope only.
func (m *Model) packMembers(pack app.PackView) []string {
	members := slices.Clone(pack.Skills)
	if !m.scope.Project {
		for _, server := range pack.MCPs {
			members = append(members, catalog.MCPPrefix+server)
		}
	}
	return members
}

// members counts what a pack holds in the active scope, and how much of it
// is on once the marks are applied.
func (m *Model) members(pack app.PackView) (on, total int) {
	for _, member := range m.packMembers(pack) {
		total++
		if m.on(member) {
			on++
		}
	}
	return on, total
}

func (m *Model) marked() int { return len(m.marks[false]) + len(m.marks[true]) }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
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
		case modeFilter:
			return m, m.updateInput(msg)
		case modeConfirm:
			m.mode = modeList
			if msg.String() == "y" {
				return m, m.onYes(m)
			}
			m.status = "cancelled"
			return m, nil
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
		if m.marked() == 0 {
			return tea.Quit
		}
		m.confirm(fmt.Sprintf("quit and drop %d marked changes?", m.marked()), func(*Model) tea.Cmd { return tea.Quit })
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
		m.mark()
	case "a":
		m.askApply()
	case "tab":
		m.switchScope()
	case "/":
		m.mode = modeFilter
		m.input.SetValue(m.filter)
		m.input.CursorEnd()
		m.input.Focus()
	case "esc":
		if m.filter == "" && m.marked() > 0 {
			m.confirm(fmt.Sprintf("drop %d marked changes?", m.marked()), func(m *Model) tea.Cmd {
				clear(m.marks[false])
				clear(m.marks[true])
				return nil
			})
		}
		m.filter = ""
		m.rebuild()
	case "u":
		m.busy, m.status = true, "updating sources…"
		return func() tea.Msg {
			updates, err := m.app.Update(false)
			return updatedMsg{updates: updates, err: err}
		}
	}
	return nil
}

func (m *Model) confirm(question string, onYes func(*Model) tea.Cmd) {
	m.mode, m.question, m.onYes = modeConfirm, question, onYes
}

// mark notes the change of the selected row for the confirmation. Marking a
// row again takes the change back.
func (m *Model) mark() {
	r, ok := m.current()
	if !ok {
		return
	}
	members, enable := []string{r.member()}, !m.on(r.member())
	switch {
	case r.mcp != "" && m.scope.Project:
		m.status = "MCP servers are global: switch the scope with tab, or use `skillet run`"
		return
	case r.skill == "" && r.mcp == "":
		pack := m.pack(r.pack)
		on, total := m.members(pack)
		members, enable = m.packMembers(pack), on < total
	}
	marks := m.marks[m.scope.Project]
	for _, member := range members {
		if m.enabled(member) == enable {
			delete(marks, member)
		} else {
			marks[member] = enable
		}
	}
}

// changes lists the marks of one scope as arguments for Toggle.
func (m *Model) changes(project bool) (enable, disable []string) {
	for member, on := range m.marks[project] {
		if on {
			enable = append(enable, member)
		} else {
			disable = append(disable, member)
		}
	}
	slices.Sort(enable)
	slices.Sort(disable)
	return enable, disable
}

// askApply asks once for all marked changes of both scopes.
func (m *Model) askApply() {
	if m.marked() == 0 {
		m.status = "nothing is marked: space marks a skill, a server or a pack"
		return
	}
	var parts []string
	for _, project := range []bool{false, true} {
		enable, disable := m.changes(project)
		scope := map[bool]string{false: "global", true: "project"}[project]
		if len(enable) > 0 {
			parts = append(parts, fmt.Sprintf("enable %s (%s)", strings.Join(enable, ", "), scope))
		}
		if len(disable) > 0 {
			parts = append(parts, fmt.Sprintf("disable %s (%s)", strings.Join(disable, ", "), scope))
		}
	}
	m.confirm(strings.Join(parts, "; ")+"?", (*Model).apply)
}

func (m *Model) apply() tea.Cmd {
	var notes []string
	var failed error
	for _, project := range []bool{false, true} {
		scope := m.app.Global()
		if project {
			var err error
			if scope, err = m.app.ProjectScope(); err != nil {
				failed = err
				continue
			}
		}
		enable, disable := m.changes(project)
		for i, names := range [][]string{enable, disable} {
			if len(names) == 0 {
				continue
			}
			report, err := m.app.Toggle(scope, i == 0, names...)
			if summary := syncSummary(report); summary != "" {
				notes = append(notes, summary)
			}
			if err != nil {
				failed = err
			}
		}
		clear(m.marks[project])
	}
	m.status = strings.Join(notes, "; ")
	if m.status == "" {
		m.status = "applied"
	}
	m.fail(failed)
	m.fail(m.reload())
	return nil
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
}

func (m *Model) updateInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.filter = ""
		m.rebuild()
		m.mode = modeList
		return nil
	case "enter":
		m.mode = modeList
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.filter = m.input.Value()
	m.rebuild()
	return cmd
}

func syncSummary(report app.SyncReport) string {
	var conflicts int
	for _, action := range report.Actions {
		if action.Op == "conflict" {
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
		parts = append(parts, fmt.Sprintf("mcp:%s has no value for %s", server, strings.Join(names, ", ")))
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
	scope := strings.Replace(m.scope.String(), m.app.Paths.Home, "~", 1)
	if m.filter != "" && m.mode != modeFilter {
		scope += "  filter: " + m.filter
	}
	if m.marked() > 0 {
		scope += fmt.Sprintf("  %d marked", m.marked())
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
		lines = append(lines, styleDim.Render("the catalog is empty: add sources to config.toml, or run `skillet import`"))
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	left := lipgloss.NewStyle().Width(leftWidth).Render(strings.Join(lines, "\n"))
	right := stylePane.Width(max(20, m.width-leftWidth-2)).Height(height).MaxHeight(height).Render(m.detail())

	var footer string
	switch m.mode {
	case modeFilter:
		footer = "filter: " + m.input.View()
	case modeConfirm:
		footer = styleWarn.Render(truncate(m.question, m.width-6) + " (y/n)")
	default:
		footer = styleDim.Render(truncate("space mark · a apply the marks · esc drop them · tab scope · / filter · u update · ←/→ fold · q quit", m.width))
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
			box = "[~]"
		}
		name := "@" + r.pack
		if r.pack == app.NoPack {
			name = "(no pack)"
		} else if strings.TrimSpace(pack.Description) == "" {
			name += " !"
		}
		state := "enabled"
		if slices.ContainsFunc(m.packMembers(pack), func(member string) bool {
			_, marked := m.marks[m.scope.Project][member]
			return marked
		}) {
			state = "once applied"
		}
		return fmt.Sprintf("%s %s %s  %d/%d %s", fold, box, name, on, total, state)
	}
	if r.mcp != "" {
		server := m.view.MCPs[r.mcp]
		box := m.box(r.member())
		note := server.Target
		if len(server.MissingSecrets) > 0 {
			note = "? needs secret " + strings.Join(server.MissingSecrets, ", ")
		}
		return fmt.Sprintf("    %s mcp:%s — %s", box, r.mcp, note)
	}
	skill := m.view.Skills[r.skill]
	box := m.box(r.skill)
	if box == "[ ]" && m.scope.Project && skill.Global {
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

// box shows the state of a member: enabled, disabled, or marked to become
// enabled (+) or disabled (-).
func (m *Model) box(member string) string {
	enable, marked := m.marks[m.scope.Project][member]
	switch {
	case marked && enable:
		return "[+]"
	case marked:
		return "[-]"
	case m.enabled(member):
		return "[x]"
	}
	return "[ ]"
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
				": add the field to a 1Password item under `secrets`, or a line to secrets.toml"), "")
		}
		packs := "none"
		if len(server.Packs) > 0 {
			packs = "@" + strings.Join(server.Packs, ", @")
		}
		info := "packs:  " + packs + "\nglobal: " + onOff(server.Global) + "\nServers are global; `skillet run` adds them for one command."
		if server.From != "" {
			info = "from:   " + app.OriginName(server.From) + "\n" + info
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
			description = styleWarn.Render("no description")
		}
		if pack.From != "" {
			description += "\n\n" + styleDim.Render("from "+app.OriginName(pack.From))
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
		description = styleWarn.Render("not found in the clone of " + skill.Source + " — run update")
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
		from = "\nfrom:   " + app.OriginName(skill.From)
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
