package importer

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// Command is what a `skills add` command asks for.
type Command struct {
	Sources []string // as the command writes them; see source.ParseSpec
	Skills  []string // the skills to take; none for all
}

// ParseCommand reads a command of the `skills` npm CLI, as npx, bunx, pnpx,
// pnpm dlx, yarn dlx or bun x start it, or as the installed CLI. ok is false
// for any other command. --skill and --all choose the skills; the options
// that choose agents, the scope or prompts are ignored.
func ParseCommand(argv []string) (cmd Command, ok bool, err error) {
	args, ok := skillsArgs(argv)
	if !ok {
		return Command{}, false, nil
	}
	if len(args) == 0 || !slices.Contains([]string{"add", "a", "install", "i"}, args[0]) {
		return Command{}, true, errors.New("only `skills add` puts skills into the catalog")
	}
	all := false
	for i := 1; i < len(args); i++ {
		// values takes the arguments up to the next option, as the CLI does.
		values := func() []string {
			var taken []string
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				taken = append(taken, args[i])
			}
			return taken
		}
		switch arg := args[i]; arg {
		case "-s", "--skill":
			cmd.Skills = append(cmd.Skills, values()...)
		case "-a", "--agent", "--subagent":
			values()
		case "--metadata":
			i++
		case "--all":
			all = true
		case "-g", "--global", "-y", "--yes", "--copy", "--full-depth", "--json":
		case "-l", "--list":
			return Command{}, true, errors.New("`-l` lists the skills of a source; drop it to add them")
		default:
			if strings.HasPrefix(arg, "-") {
				return Command{}, true, fmt.Errorf("unknown option %s of `skills add`", arg)
			}
			cmd.Sources = append(cmd.Sources, arg)
		}
	}
	if len(cmd.Sources) == 0 {
		return Command{}, true, errors.New("the `skills add` command names no source")
	}
	if all || slices.Contains(cmd.Skills, "*") {
		cmd.Skills = nil
	}
	return cmd, true, nil
}

// skillsArgs returns the arguments that follow the `skills` package in a
// command that runs it.
func skillsArgs(argv []string) ([]string, bool) {
	if len(argv) == 0 {
		return nil, false
	}
	runner, rest := filepath.Base(argv[0]), argv[1:]
	switch {
	case runner == "skills":
		return rest, true
	case runner == "npx", runner == "bunx", runner == "pnpx":
	case (runner == "pnpm" || runner == "yarn") && len(rest) > 0 && rest[0] == "dlx",
		runner == "bun" && len(rest) > 0 && rest[0] == "x":
		rest = rest[1:]
	default:
		return nil, false
	}
	for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return nil, false
	}
	if pkg, _, _ := strings.Cut(rest[0], "@"); pkg != "skills" {
		return nil, false
	}
	return rest[1:], true
}
