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
	"github.com/bonkey/skillet/internal/link"
	"github.com/bonkey/skillet/internal/mcp"
	"github.com/bonkey/skillet/internal/paths"
	"github.com/bonkey/skillet/internal/tui"
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
	cmd.AddCommand(importCmd(), toggleCmd(true), toggleCmd(false),
		syncCmd(), listCmd(), updateCmd(), runCmd(), sourcesCmd(), gistCmd(), mcpCmd())
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

// printSync prints one line per kind of change, and every change with
// --verbose, followed by what was already right. Problems are always
// printed in full.
func printSync(report app.SyncReport) {
	skills, servers := map[string][]string{}, map[string]map[string][]string{}
	var files []string // config files in the order the agents were visited
	for _, action := range report.Actions {
		if verbose || action.Op == link.OpConflict {
			fmt.Println(tilde(action.String()))
		}
		if !slices.Contains(skills[action.Op], action.Name) {
			skills[action.Op] = append(skills[action.Op], action.Name)
		}
	}
	for _, action := range report.MCP {
		if verbose {
			fmt.Println(tilde(action.String()))
		}
		if servers[action.Op] == nil {
			servers[action.Op] = map[string][]string{}
		}
		servers[action.Op][action.File] = append(servers[action.Op][action.File], action.Name)
		if !slices.Contains(files, action.File) {
			files = append(files, action.File)
		}
	}
	if verbose {
		for _, action := range report.Kept {
			fmt.Println(tilde(action.String()))
		}
		for _, action := range report.KeptMCP {
			fmt.Println(tilde(action.String()))
		}
	}
	scope := tilde(report.Scope.String())
	if !verbose {
		for _, op := range []string{link.OpLink, link.OpRelink, link.OpReplace, link.OpUnlink, link.OpDelete} {
			if names := skills[op]; len(names) > 0 {
				fmt.Printf("%-8s %s: %s\n", op, scope, strings.Join(names, ", "))
			}
		}
		for _, op := range []string{mcp.OpAdd, mcp.OpUpdate, mcp.OpRemove, mcp.OpDelete} {
			for _, file := range files {
				if names := servers[op][file]; len(names) > 0 {
					fmt.Printf("%-12s %s: %s\n", op, tilde(file), strings.Join(names, ", "))
				}
			}
		}
	}
	for _, name := range report.Missing {
		fmt.Printf("missing  %s is enabled but not found in its source\n", name)
	}
	if len(report.Extra) > 0 {
		fmt.Printf("extra    %s: enabled in %s but switched off in its config file; `sync --remove` disables them\n",
			strings.Join(report.Extra, ", "), scope)
	}
	for _, name := range sortedKeys(report.MissingSecrets) {
		fmt.Printf("missing-secret mcp:%s is left as it is: no value for %s in the 1Password items or in %s\n",
			name, strings.Join(report.MissingSecrets[name], ", "), "secrets.toml")
	}
	for _, note := range report.Notes {
		fmt.Println("note    ", tilde(note))
	}
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
			printSync(report.Sync)
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
		printSync(report)
		if err == nil && len(report.Actions)+len(report.MCP)+len(report.Missing)+len(report.MissingSecrets) == 0 {
			fmt.Println("nothing to change in", tilde(s.String()))
		}
		return err
	}
	return cmd
}

func syncCmd() *cobra.Command {
	var dryRun, remove, disableAll, purge, disableSkills, disableMCPs bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Enable what the config files switch on and repair the links (global and the project)",
		Long: `Enable what the config files switch on and repair the links.

Every entry of config.toml is on unless its "enabled" flag is false; the
project's .skillet.toml switches on its own entries. Sources without a clone
are cloned, every agent directory gets the same links, and a link follows a
skill that moved inside its source. What is enabled although its config
file switches it off stays and is reported as "extra"; --remove disables it.
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
	cmd.Flags().BoolVar(&remove, "remove", false, "disable what the config file switches off")
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
			report, err := a.Sync(s, app.SyncOptions{DryRun: dryRun, Remove: remove, DisableAll: disableAll,
				DisableSkills: disableSkills, DisableMCPs: disableMCPs, Purge: purge})
			printSync(report)
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
			printSync(a.LastSync)
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
		Short: "List sources with the ref they track and the commit their clone is at",
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
				fmt.Printf("%-24s %-24s %-10s %3d skills\n", src.Name, ref, commit, src.Skills)
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
			printSync(a.LastSync)
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
skills. Enable and disable them like skills, written "mcp:<name>":

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
