package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/bonkey/skillet/internal/app"
	"github.com/bonkey/skillet/internal/catalog"
	"github.com/bonkey/skillet/internal/paths"
	"github.com/bonkey/skillet/internal/tui"
)

// force is the global --force flag.
var force bool

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
		for _, notice := range a.Notices {
			fmt.Fprintln(os.Stderr, "note:", tilde(notice))
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
In arguments, "@name" is a pack and a bare name is a skill.`,
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
	cmd.AddCommand(importCmd(), addCmd(), removeCmd(), packCmd(), toggleCmd(true), toggleCmd(false),
		syncCmd(), listCmd(), updateCmd(), runCmd(), sourcesCmd(), refCmd(), gistCmd(), mcpCmd())
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

func printSync(report app.SyncReport) {
	for _, action := range report.Actions {
		fmt.Println(tilde(action.String()))
	}
	for _, name := range report.Missing {
		fmt.Printf("missing  %s is enabled but not found in its source\n", name)
	}
	for _, action := range report.MCP {
		fmt.Println(tilde(action.String()))
	}
	for _, name := range sortedKeys(report.MissingSecrets) {
		fmt.Printf("missing-secret mcp:%s is left as it is: no value for %s in the 1Password items or in %s\n",
			name, strings.Join(report.MissingSecrets[name], ", "), "secrets.yaml")
	}
	for _, note := range report.Notes {
		fmt.Println("note    ", note)
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
~/.agents/skills is DELETED and replaced by a link into the fetched clone;
folders that are not in the lock stay. Review with --dry-run first.`,
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
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "fetch and report, but write neither the catalog nor any link")
	cmd.Flags().StringVar(&lock, "lock", "", "lock file to read (default ~/.agents/.skill-lock.json)")
	return cmd
}

func addCmd() *cobra.Command {
	var skills []string
	var all, enable bool
	var pack, packDescription, ref string
	cmd := &cobra.Command{
		Use:   "add <owner/repo|url>",
		Short: "Add skills of a source to the catalog; without --skill/--all, list what the source offers",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			name, url, found, err := a.Fetch(args[0], ref)
			if err != nil {
				return err
			}
			if all {
				skills = sortedKeys(found)
			}
			if len(skills) == 0 {
				defer a.DropUnusedClone(name)
				for _, skill := range sortedKeys(found) {
					fmt.Printf("%-32s %s\n", skill, found[skill].Description)
				}
				return nil
			}
			for _, skill := range skills {
				if _, ok := found[skill]; !ok {
					a.DropUnusedClone(name)
					return fmt.Errorf("%s has no skill %q", name, skill)
				}
			}
			err = a.Add(app.AddRequest{Source: name, URL: url, Ref: ref, Skills: skills,
				Pack: pack, PackDescription: packDescription, Enable: enable})
			if err != nil {
				a.DropUnusedClone(name)
				return err
			}
			fmt.Printf("added %d skills from %s\n", len(skills), name)
			return nil
		},
	}
	cmd.Flags().StringSliceVarP(&skills, "skill", "s", nil, "skills to add (repeatable, comma separated)")
	cmd.Flags().BoolVar(&all, "all", false, "add every skill of the source")
	cmd.Flags().StringVar(&pack, "pack", "", "put the skills in this pack")
	cmd.Flags().StringVar(&packDescription, "pack-description", "", "description for --pack when the pack is new")
	cmd.Flags().StringVar(&ref, "ref", "", "branch or tag to track (default: the default branch)")
	cmd.Flags().BoolVar(&enable, "enable", false, "enable the skills globally")
	cmd.MarkFlagsMutuallyExclusive("skill", "all")
	return cmd
}

func removeCmd() *cobra.Command {
	var source string
	cmd := &cobra.Command{
		Use:   "remove <skill|@pack>...",
		Short: "Remove skills from the catalog; @pack removes the pack and its skills",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			if source != "" {
				src, ok := a.Catalog.Sources[source]
				if !ok {
					return fmt.Errorf("unknown source %q", source)
				}
				args = append(args, src.Skills...)
			}
			if len(args) == 0 {
				return errors.New("nothing to remove")
			}
			report, err := a.Remove(args...)
			printSync(report)
			return err
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "remove every skill of this source")
	return cmd
}

func packCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "pack", Short: "Edit packs"}
	edit := func(fn func(local *catalog.Catalog, args []string) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			return a.EditPack(args[0], cmd.Name() == "create", func(local *catalog.Catalog) error { return fn(local, args) })
		}
	}

	var description string
	create := &cobra.Command{
		Use:   "create <pack> --description <text> [skill...]",
		Short: "Create a pack",
		Args:  cobra.MinimumNArgs(1),
		RunE: edit(func(local *catalog.Catalog, args []string) error {
			return local.CreatePack(args[0], description, args[1:])
		}),
	}
	create.Flags().StringVarP(&description, "description", "d", "", "what the pack is for (required)")

	cmd.AddCommand(create,
		&cobra.Command{
			Use:   "add <pack> <skill>...",
			Short: "Add skills to a pack",
			Args:  cobra.MinimumNArgs(2),
			RunE:  edit(func(local *catalog.Catalog, args []string) error { return local.PackAdd(args[0], args[1:]) }),
		},
		&cobra.Command{
			Use:   "rm <pack> [skill...]",
			Short: "Take skills out of a pack; without skills, delete the pack and keep its skills",
			Args:  cobra.MinimumNArgs(1),
			RunE: edit(func(local *catalog.Catalog, args []string) error {
				if len(args) > 1 {
					return local.PackRemove(args[0], args[1:])
				}
				if _, ok := local.Packs[args[0]]; !ok {
					return fmt.Errorf("unknown pack %q", args[0])
				}
				local.RemovePack(args[0])
				return nil
			}),
		},
		&cobra.Command{
			Use:   "describe <pack> <text>",
			Short: "Set the description of a pack",
			Args:  cobra.ExactArgs(2),
			RunE: edit(func(local *catalog.Catalog, args []string) error {
				pack, ok := local.Packs[args[0]]
				if !ok {
					return fmt.Errorf("unknown pack %q", args[0])
				}
				if strings.TrimSpace(args[1]) == "" {
					return errors.New("a pack needs a description")
				}
				pack.Description = args[1]
				return nil
			}),
		})
	return cmd
}

func toggleCmd(enable bool) *cobra.Command {
	use, short := "disable", "Disable skills and packs in a scope"
	if enable {
		use, short = "enable", "Enable skills and packs in a scope"
	}
	cmd := &cobra.Command{Use: use + " <skill|@pack>...", Short: short, Args: cobra.MinimumNArgs(1)}
	scope := scopeFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		a, err := open()
		if err != nil {
			return err
		}
		s, err := scope(a)
		if err != nil {
			return err
		}
		report, err := a.Toggle(s, enable, args...)
		printSync(report)
		return err
	}
	return cmd
}

func syncCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Make the links match the declared state (global and, inside a project, the project)",
		Args:  cobra.NoArgs,
	}
	scope := scopeFlags(cmd)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "only print what would change")
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
			if root, ok := a.Paths.ProjectRoot(); ok {
				scopes = append(scopes, app.Scope{Project: true, Root: root})
			}
		}
		for _, s := range scopes {
			report, err := a.Sync(s, app.SyncOptions{DryRun: dryRun})
			printSync(report)
			if err != nil {
				return err
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
			name += " (gist " + short(pack.From) + ")"
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
		Use:   "run [skill|@pack]... -- <command> [args...]",
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
				fmt.Printf("%-40s %-24s %-10s %3d skills\n", src.Name, ref, commit, src.Skills)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func refCmd() *cobra.Command {
	var useDefault bool
	cmd := &cobra.Command{
		Use:   "ref <source> [<branch|tag|commit>]",
		Short: "Make a source track a branch, a tag or a full commit hash",
		Long: `Make a source track a branch, a tag or a full commit hash.

A branch moves forward on every update. A tag or a commit keeps the source at
the version you evaluated. Without a ref, print what the source tracks.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			if len(args) == 1 && !useDefault {
				for _, src := range a.Sources() {
					if src.Name == args[0] {
						fmt.Printf("%s tracks %q at %s\n", src.Name, src.Ref, short(src.Commit))
						return nil
					}
				}
				return fmt.Errorf("unknown source %q", args[0])
			}
			ref := ""
			if len(args) == 2 {
				ref = args[1]
			}
			if err := a.SetRef(args[0], ref); err != nil {
				return err
			}
			for _, src := range a.Sources() {
				if src.Name == args[0] {
					fmt.Printf("%s is at %s\n", src.Name, short(src.Commit))
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&useDefault, "default", false, "track the default branch again")
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
			Short: "Replace the catalog with the one in a gist; the replaced file is saved as config.yaml.bak",
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
  skillet pack add ios mcp:sosumi
  skillet run @ios -- claude

Enabled servers are written into the user configs of the agents listed under
"agents": claude-code, codex, crush, cursor, gemini-cli, opencode, zed. An
existing entry of the same name is overwritten and managed from then on;
entries under other names stay untouched. Servers exist in the global scope
and in "run" sessions, not in projects.`,
	}
	imp := &cobra.Command{
		Use:   "import [file]",
		Short: "Copy the servers and presets of an mcp-setup config into the catalog",
		Long: `Copy the servers and presets of an mcp-setup config into the catalog
(default ~/.config/mcp-setup/config.json).

Definitions become catalog servers and presets become packs, as they are.
Nothing becomes enabled and no agent config changes. Keys that the
definitions contain are copied too: replace them with ${NAME} in
config.yaml before pushing the catalog.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open()
			if err != nil {
				return err
			}
			file := a.MCPSetupConfig()
			if len(args) == 1 {
				file = args[0]
			}
			report, err := a.ImportMCP(file)
			if err != nil {
				return err
			}
			for _, name := range sortedKeys(report.Skipped) {
				fmt.Printf("skipped  %s: %s\n", name, report.Skipped[name])
			}
			fmt.Printf("imported %d servers and %d packs\n", len(report.Servers), len(report.Packs))
			if len(report.Servers) > 0 {
				fmt.Println("note     definitions were copied as they are: replace keys with ${NAME} in " +
					tilde(a.Paths.ConfigFile()) + " before `skillet gist push`")
			}
			return nil
		},
	}
	cmd.AddCommand(imp)
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
