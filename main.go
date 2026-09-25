package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/bonkey/skillet/internal/app"
	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/link"
	"github.com/bonkey/skillet/internal/mcp"
	"github.com/bonkey/skillet/internal/paths"
	"github.com/bonkey/skillet/internal/tui"
	"github.com/charmbracelet/lipgloss"
)

// The global flags.
var (
	force   bool
	verbose bool
	agents  []string
)

// version is set by release builds. Other builds report the module version
// from the Go build info.
var version string

func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

func main() {
	if err := root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func open() (*app.App, error) {
	p, err := paths.Default()
	if err != nil {
		return nil, err
	}
	a, err := app.Open(p)
	if err == nil {
		a.Force = force
		for _, name := range agents {
			if _, ok := link.Agents[name]; !ok {
				return nil, fmt.Errorf("unknown agent %q", name)
			}
		}
		if len(agents) > 0 {
			a.UseAgents(agents)
		}
		for _, warning := range a.Warnings {
			fmt.Fprintln(os.Stderr, "warning:", warning)
		}
	}
	return a, err
}

func root() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skillet",
		Short: "A local catalog of agent skills, enabled per scope with symlinks",
		Long: `A local catalog of agent skills, enabled per scope with symlinks.

Without a command, skillet opens the TUI.
In arguments, "@name" is a pack, "mcp:name" is an MCP server, "skills:name"
is a source, and a bare name or "name@source" is a skill.`,
		Version:       buildVersion(),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !term.IsTerminal(int(os.Stdout.Fd())) {
				return cmd.Help()
			}
			a, err := open()
			if err != nil {
				return err
			}
			return tui.Run(a)
		},
	}
	cmd.PersistentFlags().BoolVar(&force, "force", false,
		"delete files, folders and links that stand where an enabled skill goes, and link the skill")
	cmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "print every link and config entry, also those that are already right")
	cmd.PersistentFlags().StringSliceVar(&agents, "agents", nil,
		"act on these agents instead of those in the catalog (comma separated)")
	cmd.AddCommand(addCmd(), importCmd(), toggleCmd(true), toggleCmd(false),
		syncCmd(), statusCmd(), listCmd(), updateCmd(), runCmd(), sourcesCmd(), gistCmd(), mcpCmd())
	return cmd
}

// scopeFlags adds -g/-p and returns a resolver for the chosen scope.
func scopeFlags(cmd *cobra.Command) func(*app.App) (app.Scope, error) {
	var global, project bool
	cmd.Flags().BoolVarP(&global, "global", "g", false, "act on the global scope (default)")
	cmd.Flags().BoolVarP(&project, "project", "p", false, "act on the project around the working directory")
	cmd.MarkFlagsMutuallyExclusive("global", "project")
	return func(a *app.App) (app.Scope, error) {
		if project {
			return a.ProjectScope()
		}
		return a.Global(), nil
	}
}

// tilde shortens paths below the home directory for display.
func tilde(text string) string {
	if home, err := os.UserHomeDir(); err == nil {
		return strings.ReplaceAll(text, home+string(os.PathSeparator), "~/")
	}
	return text
}

// printSync prints the skills and servers that changed, in a table with one
// column per agent, or with --verbose every link and config entry, what was
// already right, and the packs the enabled and disabled skills and servers
// belong to. Problems are always printed in full.
func printSync(a *app.App, report app.SyncReport) {
	if verbose {
		for _, action := range report.Actions {
			fmt.Println(tilde(action.String()))
		}
		for _, action := range report.MCP {
			fmt.Println(tilde(action.String()))
		}
		for _, action := range report.Kept {
			fmt.Println(tilde(action.String()))
		}
		for _, action := range report.KeptMCP {
			fmt.Println(tilde(action.String()))
		}
		printMembership(a.Catalog, report)
	} else {
		printTable(a, report)
	}
	if len(report.Extra) > 0 {
		fmt.Printf("extra    %s: enabled in %s but switched off in its config file; `sync --clean` disables them\n",
			strings.Join(report.Extra, ", "), tilde(report.Scope.String()))
	}
	printProblems(report)
}

// printProblems prints the skills missing from their sources, the servers
// without their secrets, and the notes of a report.
func printProblems(report app.SyncReport) {
	for _, name := range report.Missing {
		fmt.Printf("missing  %s is enabled but not found in its source\n", name)
	}
	for _, name := range sortedKeys(report.MissingSecrets) {
		fmt.Printf("missing-secret mcp:%s is left as it is: no value for %s in the 1Password items or in %s\n",
			name, strings.Join(report.MissingSecrets[name], ", "), "secrets.toml")
	}
	for _, note := range report.Notes {
		fmt.Println("note    ", tilde(note))
	}
}

// mark is one cell of a table.
type mark int

const (
	markNone     mark = iota // the agent has no such directory or config
	markKeep                 // already right
	markAdd                  // linked or written now
	markRepair               // relinked or rewritten
	markRemove               // unlinked or removed; in status: not on the disk
	markConflict             // something in the way
	markExtra                // on the disk, off in the config
)

// glyphs are the cells of the tables: Nerd Font symbols on a terminal,
// letters when the output is piped, so that a program can read them.
var (
	nerdGlyphs  = [...]string{"\uf068", "\uf00c", "\uf067", "\uf021", "\uf00d", "\uf071", "\uf06a"}
	asciiGlyphs = [...]string{"-", "ok", "+", "~", "x", "!", "e"}
	syncNames   = [...]string{"absent", "kept", "added", "repaired", "removed", "conflict", "extra"}
	statusNames = [...]string{"absent", "on", "added", "sync repairs", "not on disk; sync enables", "conflict",
		"off in config; sync --clean disables"}
	glyphStyles = [...]lipgloss.Style{
		lipgloss.NewStyle().Faint(true),
		lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true),
		lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true),
		lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true),
		lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
	}
	styleHeading = lipgloss.NewStyle().Bold(true)
)

func markOf(op string) mark {
	switch op {
	case link.OpKeep, mcp.OpKeep:
		return markKeep
	case link.OpLink, link.OpReplace, mcp.OpAdd:
		return markAdd
	case link.OpRelink, mcp.OpUpdate:
		return markRepair
	case link.OpUnlink, link.OpDelete, mcp.OpRemove, mcp.OpDelete:
		return markRemove
	case link.OpConflict:
		return markConflict
	}
	return markNone
}

func markOfState(state string) mark {
	switch state {
	case app.StateOn:
		return markKeep
	case app.StateDrift:
		return markRemove
	case app.StateRepair:
		return markRepair
	case app.StateConflict:
		return markConflict
	case app.StateExtra:
		return markExtra
	}
	return markNone
}

// shortAgent is the column header of an agent.
func shortAgent(name string) string {
	for _, suffix := range []string{"-code", "-cli"} {
		name = strings.TrimSuffix(name, suffix)
	}
	return strings.TrimPrefix(name, "github-")
}

// table is one table of printTables: a row per skill or server, a column
// per agent, and the packs shown as off without rows.
type table struct {
	title  string
	agents []string
	rows   []app.Row
	mark   func(value string) mark
	off    []string
}

func agentsOf(columns [][2]string) []string {
	var names []string
	for _, col := range columns {
		names = append(names, col[0])
	}
	return names
}

// printTable prints the skills and servers that a report changed, one
// table each, with a column per agent that has the directory or config.
func printTable(a *app.App, report app.SyncReport) {
	changed := func(rows []app.Row) []app.Row {
		return slices.DeleteFunc(rows, func(r app.Row) bool {
			for _, op := range r.Agents {
				if m := markOf(op); m != markNone && m != markKeep {
					return false
				}
			}
			return true
		})
	}
	skills, servers := a.Rows(report)
	dirs, files := a.Columns(report.Scope)
	scope := tilde(report.Scope.String())
	printTables([]table{
		{scope + " skills", agentsOf(dirs), changed(skills), markOf, nil},
		{scope + " mcp", agentsOf(files), changed(servers), markOf, nil},
	}, syncNames)
}

// printStatus prints every skill and server of a scope with its state per
// agent, and the packs that are off.
func printStatus(a *app.App, status app.Status) {
	dirs, files := a.Columns(status.Report.Scope)
	var offSkills, offServers []string
	for _, pack := range status.OffPacks {
		set := catalog.Set{Packs: []string{pack}}
		if len(a.Catalog.Resolve(set)) > 0 || len(a.Catalog.ResolveMCPs(set)) == 0 {
			offSkills = append(offSkills, pack)
		}
		if len(a.Catalog.ResolveMCPs(set)) > 0 {
			offServers = append(offServers, pack)
		}
	}
	scope := tilde(status.Report.Scope.String())
	printTables([]table{
		{scope + " skills", agentsOf(dirs), status.Skills, markOfState, offSkills},
		{scope + " mcp", agentsOf(files), status.MCPs, markOfState, offServers},
	}, statusNames)
}

// printTables prints tables whose first columns share one width, so that
// their agent columns line up. Rows are grouped by their first pack, in
// pack name order, and rows in no pack come last. One legend follows.
func printTables(tables []table, names [7]string) {
	tables = slices.DeleteFunc(tables, func(t table) bool {
		return len(t.agents) == 0 || len(t.rows)+len(t.off) == 0
	})
	if len(tables) == 0 {
		return
	}
	label := func(r app.Row) string {
		if r.Label != "" {
			return r.Label
		}
		return r.Name
	}
	width := 0
	for _, t := range tables {
		width = max(width, len(t.title))
		for _, r := range t.rows {
			width = max(width, len(label(r))+2) // rows are indented below their pack
		}
	}
	glyphs := asciiGlyphs
	if term.IsTerminal(int(os.Stdout.Fd())) {
		glyphs = nerdGlyphs
	}
	used := map[mark]bool{}
	for i, t := range tables {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("%-*s", width, t.title)
		for _, agent := range t.agents {
			fmt.Printf("  %s", shortAgent(agent))
		}
		fmt.Println()
		groups := map[string][]app.Row{}
		for _, r := range t.rows {
			pack := ""
			if len(r.Packs) > 0 {
				pack = r.Packs[0]
			}
			groups[pack] = append(groups[pack], r)
		}
		for _, pack := range slices.Sorted(slices.Values(append(sortedKeys(groups), t.off...))) {
			rows := groups[pack]
			switch {
			case pack == "":
				continue
			case len(rows) == 0:
				fmt.Println(glyphStyles[markNone].Render("@" + pack + "  off"))
				continue
			}
			fmt.Println(styleHeading.Render("@" + pack))
			for _, r := range rows {
				printRow(r, label(r), width, t, glyphs, used)
			}
		}
		if rows := groups[""]; len(rows) > 0 {
			fmt.Println(styleHeading.Render("no pack"))
			for _, r := range rows {
				printRow(r, label(r), width, t, glyphs, used)
			}
		}
	}
	var legend []string
	for cell := markNone; cell <= markExtra; cell++ {
		if used[cell] {
			legend = append(legend, glyphStyles[cell].Render(glyphs[cell])+" "+names[cell])
		}
	}
	if len(legend) > 0 {
		fmt.Println(glyphStyles[markNone].Render(strings.Join(legend, "   ")))
	}
}

func printRow(r app.Row, label string, width int, t table, glyphs [7]string, used map[mark]bool) {
	fmt.Printf("  %-*s", width-2, label)
	for _, agent := range t.agents {
		cell := t.mark(r.Agents[agent])
		used[cell] = true
		name, span := shortAgent(agent), lipgloss.Width(glyphs[cell])
		pad := (len(name) - span) / 2
		fmt.Printf("  %*s%s%*s", pad, "", glyphStyles[cell].Render(glyphs[cell]), max(len(name)-pad-span, 0), "")
	}
	fmt.Println()
}

// printMembership prints, per pack, the skills and servers that a report
// finds enabled, enables or disables; those in no pack are listed by their
// own name. In the global scope, the packs with nothing on are listed as off.
func printMembership(cat *catalog.Catalog, report app.SyncReport) {
	kept, on, off := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, action := range report.Actions {
		switch action.Op {
		case link.OpLink, link.OpReplace:
			on[action.Name] = true
		case link.OpUnlink:
			off[action.Name] = true
		}
	}
	for _, action := range report.Kept {
		kept[action.Name] = true
	}
	for _, action := range report.MCP {
		switch action.Op {
		case mcp.OpAdd:
			on[catalog.MCPPrefix+action.Name] = true
		case mcp.OpRemove:
			off[catalog.MCPPrefix+action.Name] = true
		}
	}
	for _, action := range report.KeptMCP {
		kept[catalog.MCPPrefix+action.Name] = true
	}
	scope := tilde(report.Scope.String())
	active := map[string]bool{}
	for _, group := range []struct {
		op    string
		names map[string]bool
	}{{"enabled", kept}, {"enable", on}, {"disable", off}} {
		loose := map[string]bool{}
		for name := range group.names {
			loose[name] = true
		}
		for _, pack := range cat.PackNames() {
			set := catalog.Set{Packs: []string{pack}}
			var members []string
			for _, skill := range cat.Resolve(set) {
				if group.names[skill] {
					members = append(members, skill)
					delete(loose, skill)
				}
			}
			for _, server := range cat.ResolveMCPs(set) {
				if group.names[catalog.MCPPrefix+server] {
					members = append(members, catalog.MCPPrefix+server)
					delete(loose, catalog.MCPPrefix+server)
				}
			}
			if len(members) > 0 {
				sort.Strings(members)
				fmt.Printf("%-8s %s: @%s: %s\n", group.op, scope, pack, strings.Join(members, ", "))
				if group.op != "disable" {
					active[pack] = true
				}
			}
		}
		if len(loose) > 0 {
			fmt.Printf("%-8s %s: %s\n", group.op, scope, strings.Join(sortedKeys(loose), ", "))
		}
	}
	if !report.Scope.Project {
		var idle []string
		for _, pack := range cat.PackNames() {
			if !active[pack] {
				idle = append(idle, "@"+pack)
			}
		}
		if len(idle) > 0 {
			fmt.Printf("%-8s %s: %s\n", "off", scope, strings.Join(idle, ", "))
		}
	}
}

func addCmd() *cobra.Command {
	var skills []string
	var name string
	cmd := &cobra.Command{
		Use:   "add <owner/repo|url|path> | add -- <command>...",
		Short: "Put a skill source or an MCP server into the catalog",
		Long: `Put a skill source or an MCP server into the catalog.

A source is written as ` + "`npx skills add`" + ` takes it: owner/repo, owner/repo@skill,
owner/repo/path, a GitHub or GitLab URL, a git URL or a local path, each
with an optional #ref. Without --skill the source takes every skill it
offers, also those it gains later. A ` + "`skills add`" + ` command may follow --: its
--skill and --all count, its other options are ignored.

A URL that is not a git repository is an MCP server when one answers there,
and a command after -- that is not ` + "`skills add`" + ` starts a server;
` + "`skillet add mcp`" + ` takes a server without checking. Values of URL parameters
that look like secrets move to secrets.toml.

add writes the config file only, the project's .skillet.toml with -p and
config.toml otherwise; ` + "`skillet sync`" + ` enables what it added.

  skillet add bonkey/skills --skill captains-log
  skillet add -- npx skills add bonkey/skills -g --skill captains-log -y
  skillet add https://mcp.exa.ai/mcp
  skillet add -- npx @mobilenext/mobile-mcp@latest`,
		Args: cobra.ArbitraryArgs,
	}
	scope := scopeFlags(cmd)
	cmd.Flags().StringSliceVarP(&skills, "skill", "s", nil, "take only these skills of the source (comma separated)")
	cmd.Flags().StringVar(&name, "name", "", "name the server instead of guessing it from its command or URL")
	run := func(server bool, scope func(*app.App) (app.Scope, error)) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			req := app.AddRequest{Skills: skills, Name: name, MCP: server}
			switch dash := cmd.ArgsLenAtDash(); {
			case dash == 0 && len(args) > 0:
				req.Command = args
			case dash < 0 && len(args) == 1:
				req.Arg = args[0]
			default:
				return errors.New("give one source or URL, or a command after --")
			}
			a, err := open()
			if err != nil {
				return err
			}
			s := a.Global()
			if scope != nil {
				if s, err = scope(a); err != nil {
					return err
				}
			}
			report, err := a.Add(s, req)
			if err != nil {
				return err
			}
			for _, entry := range report.Present {
				fmt.Printf("present  %s is in the catalog already\n", entry)
			}
			for _, note := range report.Notes {
				fmt.Println("note    ", tilde(note))
			}
			if len(report.Secrets) > 0 {
				fmt.Printf("secrets  %s: values kept in %s\n", strings.Join(report.Secrets, ", "), tilde(a.Paths.SecretsFile()))
			}
			if report.File != "" {
				fmt.Printf("added    %s to %s; `skillet sync` enables it\n", strings.Join(report.Added, ", "), tilde(report.File))
			}
			return nil
		}
	}
	cmd.RunE = run(false, scope)
	server := &cobra.Command{
		Use:   "mcp <url> | mcp -- <command>...",
		Short: "Put an MCP server into the catalog, without checking the URL or the command",
		Long: `Put an MCP server into the catalog, without checking the URL or the command.

The server is named after the package its command starts, or after its
host; --name names it otherwise. Values of URL parameters that look like
secrets move to secrets.toml. ` + "`skillet sync`" + ` enables it.

  skillet add mcp https://mcp.tavily.com/mcp/?tavilyApiKey=...
  skillet add mcp -- npx @mobilenext/mobile-mcp@latest`,
		Args: cobra.ArbitraryArgs,
		RunE: run(true, nil),
	}
	server.Flags().StringVar(&name, "name", "", "name the server instead of guessing it from its command or URL")
	cmd.AddCommand(server)
	return cmd
}

func importCmd() *cobra.Command {
	var dryRun bool
	var lock string
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Seed the catalog from the lock file of the `skills` npm CLI",
		Long: `Seed the catalog from the lock file of the ` + "`skills`" + ` npm CLI.

Only the definitions (source and skill name) come from the lock. The content
of every source is fetched fresh. Each imported skill's folder in
~/.agents/skills is DELETED; where a configured agent reads that directory, a
link into the fetched clone takes its place. Folders that are not in the lock
stay. The MCP servers in the configs of Claude Code, Codex and Gemini CLI
join the catalog too; values that look like secrets are replaced by ${NAME}
placeholders and kept in secrets.toml. Review with --dry-run first.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			if lock == "" {
				lock = a.LegacyLock()
			}
			report, err := a.Import(lock, dryRun)
			if err != nil {
				return err
			}
			printSync(a, report.Sync)
			for _, old := range sortedKeys(report.Renamed) {
				fmt.Printf("renamed  %s is called %s in its source; the link named %[1]s stays in place\n", old, report.Renamed[old])
			}
			byReason := map[string][]string{}
			for _, name := range sortedKeys(report.Skipped) {
				byReason[report.Skipped[name]] = append(byReason[report.Skipped[name]], name)
			}
			for _, reason := range sortedKeys(byReason) {
				fmt.Printf("skipped  %s: %s\n", strings.Join(byReason[reason], ", "), tilde(reason))
			}
			verb := "imported"
			if dryRun {
				verb = "would import"
			}
			fmt.Printf("%s %d skills from %d sources in %d packs\n", verb, len(report.Imported), len(a.Catalog.Sources), len(a.Catalog.Packs))
			if len(report.Servers) > 0 {
				fmt.Printf("%s %d servers from the agents' configs: %s\n", verb, len(report.Servers), strings.Join(report.Servers, ", "))
			}
			if len(report.Secrets) > 0 {
				fmt.Printf("secrets  %s: values kept in %s\n", strings.Join(report.Secrets, ", "), tilde(a.Paths.SecretsFile()))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "fetch and report, but write neither the catalog nor any link")
	cmd.Flags().StringVar(&lock, "lock", "", "lock file to read (default ~/.agents/.skill-lock.json)")
	return cmd
}

func toggleCmd(enable bool) *cobra.Command {
	var save bool
	use, short := "disable", "Disable skills, servers, packs and sources in a scope"
	if enable {
		use, short = "enable", "Enable skills, servers, packs and sources in a scope"
	}
	cmd := &cobra.Command{Use: use + " <skill|mcp:server|@pack|skills:source>...", Short: short, Args: cobra.MinimumNArgs(1),
		Long: short + `.

Only links and the agents' MCP entries change. With --save, the "enabled"
flag of the named entries in the scope's config file changes too, so the
next sync keeps the result; without it, what the file switches on comes
back with the next sync.`}
	scope := scopeFlags(cmd)
	cmd.Flags().BoolVarP(&save, "save", "s", false, "also write the flag into the scope's config file")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		a, err := open()
		if err != nil {
			return err
		}
		s, err := scope(a)
		if err != nil {
			return err
		}
		report, err := a.Toggle(s, enable, save, args...)
		printSync(a, report)
		if err == nil && len(report.Actions)+len(report.MCP)+len(report.Missing)+len(report.MissingSecrets) == 0 {
			fmt.Println("nothing to change in", tilde(s.String()))
		}
		return err
	}
	return cmd
}

func statusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show every skill and server with its state per agent (global and the project)",
		Long: `Show every skill and server with its state per agent, and change nothing.

A table per scope and kind has a row per skill, named by its folder inside
the clones, or per server, grouped by pack, and a column per agent:

  on        on in the config file and on the disk
  drift     on in the config file but not on the disk; sync enables it
  extra     on the disk but off in the config file; sync --clean disables it
  repair    on, but the link or entry needs rewriting; sync repairs it
  conflict  something skillet does not own stands in the way
  absent    the agent does not have it

A pack switched off in the config file with nothing on the disk is one
"off" line. --json prints the same as a list of scopes.`,
		Args: cobra.NoArgs,
	}
	scope := scopeFlags(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the states as JSON")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		a, err := open()
		if err != nil {
			return err
		}
		chosen, err := scope(a)
		if err != nil {
			return err
		}
		scopes := []app.Scope{chosen}
		if !cmd.Flags().Changed("global") && !cmd.Flags().Changed("project") {
			if project, err := a.ProjectScope(); err == nil {
				scopes = append(scopes, project)
			}
		}
		var all []app.Status
		for i, s := range scopes {
			status, err := a.Status(s)
			if err != nil {
				return err
			}
			if asJSON {
				all = append(all, status)
				continue
			}
			if i > 0 {
				fmt.Println()
			}
			printStatus(a, status)
			printProblems(status.Report)
		}
		if asJSON {
			out := json.NewEncoder(os.Stdout)
			out.SetIndent("", "  ")
			return out.Encode(all)
		}
		return nil
	}
	return cmd
}

func syncCmd() *cobra.Command {
	var dryRun, clean, disableAll, purge, disableSkills, disableMCPs bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Enable what the config files switch on and repair the links (global and the project)",
		Long: `Enable what the config files switch on and repair the links.

Every entry of config.toml is on unless its "enabled" flag is false; the
project's .skillet.toml switches on its own entries. Sources without a clone
are cloned, every agent directory gets the same links, and a link follows a
skill that moved inside its source. What is enabled although its config
file switches it off stays and is reported as "extra"; --clean disables it.
--disable-skills unlinks every skill skillet manages in the scope and
--disable-mcps removes every server entry it manages, except those of running
sessions; --disable-all is both. The config files stay as they are, so the next
plain sync enables it all again. --purge deletes the skills and servers
skillet does not manage from the agents' directories and configs, so that
they hold the catalog and nothing else. All of them are meant to be tried
with --dry-run first.`,
		Args: cobra.NoArgs,
	}
	scope := scopeFlags(cmd)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "only print what would change")
	cmd.Flags().BoolVar(&clean, "clean", false, "disable what the config file switches off")
	cmd.Flags().BoolVar(&disableSkills, "disable-skills", false, "unlink every skill skillet manages in the scope")
	cmd.Flags().BoolVar(&disableMCPs, "disable-mcps", false, "remove every server entry skillet manages from the agents' configs")
	cmd.Flags().BoolVar(&disableAll, "disable-all", false, "both --disable-skills and --disable-mcps")
	cmd.Flags().BoolVar(&purge, "purge", false, "delete the skills and servers skillet does not manage from the agents")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		a, err := open()
		if err != nil {
			return err
		}
		chosen, err := scope(a)
		if err != nil {
			return err
		}
		if disableMCPs && chosen.Project {
			return errors.New("MCP servers are global: --disable-mcps needs the global scope")
		}
		scopes := []app.Scope{chosen}
		if !cmd.Flags().Changed("global") && !cmd.Flags().Changed("project") {
			if project, err := a.ProjectScope(); err == nil {
				scopes = append(scopes, project)
			}
		}
		verbose = verbose || dryRun
		for _, s := range scopes {
			report, err := a.Sync(s, app.SyncOptions{DryRun: dryRun, Remove: clean, DisableAll: disableAll,
				DisableSkills: disableSkills, DisableMCPs: disableMCPs, Purge: purge})
			printSync(a, report)
			if err != nil {
				return err
			}
			if len(report.Actions)+len(report.MCP)+len(report.Missing)+len(report.MissingSecrets)+len(report.Extra) == 0 {
				fmt.Println("nothing to change in", tilde(s.String()))
			}
		}
		return nil
	}
	return cmd
}

func listCmd() *cobra.Command {
	var enabledOnly, asJSON bool
	var pack string
	cmd := &cobra.Command{
		Use:   "list [term...]",
		Short: "List or search packs and skills with their descriptions and enabled state",
		Long: `List or search packs and skills with their descriptions and enabled state.

With terms, only skills that match every term are listed. A term is matched,
ignoring case, against the skill's name and description and against the name
and description of its pack. A term may be a regular expression:

  skillet list pull request       # both words
  skillet list 'swift|ios|xcode'  # any of them
  skillet list --enabled review   # combined with the other filters`,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			view, err := a.View()
			if err != nil {
				return err
			}
			view = view.Filter(pack, enabledOnly, args)
			if asJSON {
				out := json.NewEncoder(os.Stdout)
				out.SetIndent("", "  ")
				return out.Encode(view)
			}
			printView(view)
			return nil
		},
	}
	cmd.Flags().BoolVar(&enabledOnly, "enabled", false, "only skills enabled globally or in the project")
	cmd.Flags().StringVar(&pack, "pack", "", "only this pack")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func printView(view app.View) {
	width := 100
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 40 {
		width = w
	}
	if len(view.Packs) == 0 {
		fmt.Println("no skills match")
		return
	}
	fmt.Println("G = enabled globally, P = enabled in this project, ! = no description, ? = missing from its source, or a server without its secrets")
	for _, pack := range view.Packs {
		name, mark := "@"+pack.Name, " "
		if pack.Name == app.NoPack {
			name = "(no pack)"
		} else if strings.TrimSpace(pack.Description) == "" {
			mark = "!"
		}
		if pack.From != "" {
			name += " (" + app.OriginName(short(pack.From)) + ")"
		}
		fmt.Printf("\n%s%s  %s\n", mark, name, pack.Description)
		for _, skillName := range pack.Skills {
			skill := view.Skills[skillName]
			flags := []byte("-- ")
			if skill.Global {
				flags[0] = 'G'
			}
			if skill.Project {
				flags[1] = 'P'
			}
			if skill.Missing {
				flags[2] = '?'
			} else if skill.Description == "" {
				flags[2] = '!'
			}
			line := fmt.Sprintf("  %s %-30s %s", flags, skill.Name, skill.Description)
			if runes := []rune(line); len(runes) > width {
				line = string(runes[:width-1]) + "…"
			}
			fmt.Println(line)
		}
		for _, name := range pack.MCPs {
			server := view.MCPs[name]
			flags := []byte("-  ")
			if server.Global {
				flags[0] = 'G'
			}
			if len(server.MissingSecrets) > 0 {
				flags[2] = '?'
			}
			line := fmt.Sprintf("  %s %-30s %s", flags, "mcp:"+server.Name, server.Target)
			if runes := []rune(line); len(runes) > width {
				line = string(runes[:width-1]) + "…"
			}
			fmt.Println(line)
		}
	}
}

func updateCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Fetch all sources, report changed skills and move the clones forward",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			updates, err := a.Update(check)
			printSync(a, a.LastSync)
			failed := 0
			for _, u := range updates {
				switch {
				case u.Err != nil:
					failed++
					fmt.Printf("%s: %v\n", u.Source, u.Err)
				case u.Cloned:
					fmt.Printf("%s: cloned\n", u.Source)
				case len(u.Changed) > 0:
					fmt.Printf("%s: changed: %s\n", u.Source, strings.Join(u.Changed, ", "))
				case u.Other:
					fmt.Printf("%s: new commits, no catalog skill changed\n", u.Source)
				}
			}
			if err != nil {
				return err
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d sources failed", failed, len(updates))
			}
			if check {
				fmt.Println("checked", len(updates), "sources; nothing was changed")
			} else {
				fmt.Println("updated", len(updates), "sources")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only report, leave the clones as they are")
	return cmd
}

func runCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run [skill|mcp:server|@pack|skills:source]... -- <command> [args...]",
		Short: "Enable skills in the project for as long as a command runs",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			if dash < 0 || dash == len(args) {
				return errors.New("usage: skillet run [skill|@pack]... -- <command> [args...]")
			}
			a, err := open()
			if err != nil {
				return err
			}
			code, err := a.Run(args[:dash], args[dash:])
			if err != nil {
				return err
			}
			if code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
}

// short abbreviates a commit hash or gist id.
func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func sourcesCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "sources",
		Short: "List sources with the ref they track, the commit their clone is at and their path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			if asJSON {
				out := json.NewEncoder(os.Stdout)
				out.SetIndent("", "  ")
				return out.Encode(a.Sources())
			}
			for _, src := range a.Sources() {
				ref, commit := src.Ref, short(src.Commit)
				if ref == "" {
					ref = "(default branch)"
				}
				if commit == "" {
					commit = "not cloned"
				}
				in := ""
				if src.Path != "" {
					in = " in " + src.Path
				}
				fmt.Printf("%-24s %-24s %-10s %3d skills%s\n", src.Name, ref, commit, src.Skills, in)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func gistCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gist",
		Short: "Keep the catalog in a gist and include the catalogs of other gists",
		Long: `Keep the catalog in a gist and include the catalogs of other gists.

Gists are reached through the gh CLI. An included gist adds its sources, packs
and enabled skills to yours; your own entries win a name clash, then the
earlier include. Gists may include further gists. Each gist takes part once,
so gists that include each other do no harm.`,
	}
	var fresh, public bool
	push := &cobra.Command{
		Use:   "push",
		Short: "Upload the catalog to its gist, creating a secret gist the first time",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			id, created, err := a.Push(fresh, public)
			if err != nil {
				return err
			}
			verb := "updated"
			if created {
				verb = "created"
			}
			fmt.Printf("%s https://gist.github.com/%s\n", verb, id)
			return nil
		},
	}
	push.Flags().BoolVar(&fresh, "new", false, "create a new gist even when the catalog already has one")
	push.Flags().BoolVar(&public, "public", false, "make a newly created gist public")

	act := func(fn func(a *app.App, args []string) (string, error), done string) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			id, err := fn(a, args)
			if err != nil {
				return err
			}
			for _, warning := range a.Warnings {
				fmt.Fprintln(os.Stderr, "warning:", warning)
			}
			printSync(a, a.LastSync)
			fmt.Printf(done+"\n", id)
			return nil
		}
	}
	cmd.AddCommand(push,
		&cobra.Command{
			Use:   "pull [<gist>]",
			Short: "Replace the catalog with the one in a gist; the replaced file is saved as config.toml.bak",
			Args:  cobra.MaximumNArgs(1),
			RunE: act(func(a *app.App, args []string) (string, error) {
				return a.Pull(strings.Join(args, ""))
			}, "pulled gist %s"),
		},
		&cobra.Command{
			Use:   "include <gist>",
			Short: "Merge the catalog of another gist into yours",
			Args:  cobra.ExactArgs(1),
			RunE:  act(func(a *app.App, args []string) (string, error) { return a.Include(args[0]) }, "included gist %s"),
		},
		&cobra.Command{
			Use:   "exclude <gist>",
			Short: "Stop including a gist",
			Args:  cobra.ExactArgs(1),
			RunE:  act(func(a *app.App, args []string) (string, error) { return a.Exclude(args[0]) }, "excluded gist %s"),
		},
		&cobra.Command{
			Use:   "list",
			Short: "Show the catalog's gist and the included gists",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				a, err := open()
				if err != nil {
					return err
				}
				own := a.Local.Gist
				if own == "" {
					own = "(none; `gist push` creates one)"
				}
				fmt.Println("catalog gist:", own)
				depth := map[string]int{"": 0}
				for _, inc := range a.Included {
					depth[inc.ID] = depth[inc.Parent] + 1
					indent := strings.Repeat("  ", depth[inc.ID])
					fmt.Printf("%sincludes %s  %d sources, %d packs\n", indent, inc.ID, len(inc.Catalog.Sources), len(inc.Catalog.Packs))
					for _, id := range inc.Skipped {
						fmt.Printf("%s  skipped %s: already part of the catalog\n", indent, id)
					}
				}
				return nil
			},
		})
	return cmd
}

func mcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP servers",
		Long: `Manage MCP servers.

Servers are defined under "mcps" in the catalog and belong to packs next to
skills; ` + "`skillet add mcp`" + ` puts one there. Enable and disable them like
skills, written "mcp:<name>":

  skillet enable @ios mcp:tavily
  skillet disable mcp:simctl
  skillet run @ios -- claude

Enabled servers are written into the user configs of the agents listed under
"agents": claude-code, codex, crush, cursor, gemini-cli, opencode, zed. An
existing entry of the same name is overwritten and managed from then on;
entries under other names stay untouched. Servers exist in the global scope
and in "run" sessions, not in projects.`,
	}
	return cmd
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
