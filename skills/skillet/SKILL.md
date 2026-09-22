---
name: skillet
description: "Finds, enables and disables agent skills, MCP servers and whole packs of them with the skillet CLI, globally or for the current project. Use FIRST whenever a skill or an MCP server is wanted or missing: before searching the web or a directory, installing one from elsewhere, or writing a new skill, search the local skillet catalog. Also use when the user asks to enable, disable, turn on, turn off, load or unload a skill, an MCP server or a pack, asks which skills or servers are enabled or available, says too many skills or tools are loaded, wants the setup for a kind of work (for example 'load the iOS skills for this project'), or mentions skillet."
---

# skillet

`skillet` keeps a catalog of evaluated skills and MCP servers, grouped into packs. An enabled skill
is a symlink in the agent's skills directory, an enabled server is an entry in the agent's user
config; disabled ones stay in the catalog. Those links and entries are the state: `enable` and
`disable` change them, and with `--save` also switch the entry on or off in the config file. In
commands, `@name` is a pack, `mcp:name` is a server, `skills:name` is a whole source and a bare
name is a skill. Commands accept `name@source` too, and reject it when the skill comes from
another source.

## Search the catalog first

The catalog holds the skills the user has already evaluated, and most of them are disabled at any
time. A skill that is not loaded in this session is therefore usually one command away. Whenever a
skill would help with the task, or the user asks for one, search the catalog before anything else:

```sh
skillet list --json 'swift|ios|xcode'    # any of these words
skillet list --json pull request         # every word
```

Terms match names and descriptions of skills and of their packs, ignoring case, and may be regular
expressions. Try several words for the same need. In the result, `skills` holds the matching
skills keyed by name, and `global` and `project` tell whether a match is enabled. `mcps` holds the
matching servers the same way, with their command or URL as `target`.

1. A match that is disabled: enable it (see Workflow), and tell the user which skill you picked and why.
2. A match that is enabled but not loaded in this session: it becomes available in the next
   session. Meanwhile read its `SKILL.md` through the link in the skills directory, for example
   `.claude/skills/<name>/SKILL.md` or `~/.claude/skills/<name>/SKILL.md`, and follow it.
3. No match: say that the catalog has nothing for this need. Only then look elsewhere, such as
   another skill directory, the web, or writing a new skill, and leave adding a found skill to the
   catalog to the user.

## Rules

- Run `skillet` only with a subcommand. Bare `skillet` opens an interactive TUI that an agent cannot drive.
- Read the catalog with `skillet list --json`. Take skill and pack names from that output; never guess them.
- To switch a skill off, use `disable`. The catalog is the user's `config.toml`: do not edit it without the user's consent, and add `--save` only when the user asks for a lasting change.
- Never add `--force`, `--purge`, `--disable-all`, `--disable-skills` or `--disable-mcps` on your own. `--force` deletes whatever stands in the way of a link, `sync --purge` deletes the skills and servers skillet does not manage, `sync --disable-all` disables everything it manages, and `--disable-skills` and `--disable-mcps` one kind of it. When a command prints a `conflict` line, show it to the user and ask.
- Adding skills to the catalog is the user's decision: it holds skills the user has evaluated. Propose the lines for `~/.config/skillet/config.toml` and wait for a yes.
- Do not pass `--agents` unless the user asks to act on certain agents only.
- Agents read their skills directories when a session starts. After a change, tell the user that a skill that does not show up yet is available in the next session.

## Choosing the scope

| The user means | Flag | Effect |
| --- | --- | --- |
| this project, this repo, this task | `-p` | Links in the project's agent directories, such as `./.claude/skills` and `./.agents/skills`. The project is the nearest directory up the tree with a `.skillet.toml`, or else with a git repository, or else the working directory. |
| everywhere, always, by default | none | Links in the agent directories below the home directory, such as `~/.claude/skills`. |

Every entry of `~/.config/skillet/config.toml`, and of a project's `.skillet.toml`, is on unless
it carries `enabled = false`. `skillet sync` enables what is on again, so disabling such an entry
lasts until the next sync, and the command prints a `note` that says so. `disable --save` switches
it off in the file for good; do that only when the user asks for it. `~/.config/skillet/config.local.toml`,
when present, overrides `config.toml` on this machine; it is the user's file too, never edit it.

When the request names no scope, prefer `-p` for skills a task needs and the global scope for
skills the user wants in general. Ask when the choice matters and is unclear.

Project skills add to the global ones. A globally enabled skill cannot be switched off for one
project: say so, and offer to disable it globally and enable it with `-p` in the projects that need it.

## Workflow

1. Look at the catalog and the current state:

   ```sh
   skillet list --json              # everything
   skillet list --enabled --json    # only what is enabled globally or in this project
   skillet list --pack ios --json   # one pack
   skillet list --json review       # only what matches the terms
   ```

   The output has `packs` (`name`, `description`, `skills`) and `skills` keyed by name
   (`description`, `packs`, `global`, `project`, `missing`). `project` at the top level is the
   project root, and is absent outside a project.

2. Match the request to names with the catalog search above. Prefer a pack when the user asks
   for a kind of work. Show the user what you picked when the match is not obvious.

3. Apply the change:

   ```sh
   skillet enable -p @ios swift-docc     # a pack and a skill, in this project
   skillet disable top-design            # one skill, globally
   skillet disable @marketing            # a whole pack, globally
   ```

   Disabling one skill of a pack leaves the pack's other skills enabled.

4. Read the output. Each line is one kind of change with the scope and the names it covers:
   `link`, `unlink`, `relink`, `replace`, `delete`. With `--verbose` each line is one link with its path, and `keep` lines list the links that were already right.
   `nothing to change` means the state already matched. Handle `conflict`, `missing` and `extra`
   lines as described below.

5. Report what is enabled now and in which scope, and mention the next-session caveat.

## MCP servers

- Servers are global. `-p` does not take them; a pack enabled with `-p` links its skills and
  prints a `note` naming the servers it skipped. Enable those globally, or suggest `skillet run`.
- `skillet enable mcp:tavily` and `skillet disable mcp:tavily` write and remove the server in the
  user config of every configured agent. Lines start with `mcp-add`, `mcp-update`, `mcp-remove` or `mcp-delete`
  and name the config file and the servers, one line per file; with `--verbose` there is one
  line per server entry.
  An entry of the same name that is already in an agent's config is overwritten with the
  catalog's definition.
- An agent loads its servers at start. Tell the user that the change takes effect in the next
  session of that agent.
- Never ask for, read, print or write a secret value. Secrets come from the user's 1Password
  items or a local file. When a line says `missing-secret mcp:<name> ... no value for <NAME>`, or
  a `note` mentions 1Password, tell the user: they add a field `<NAME>` to their 1Password item,
  unlock 1Password, or add a `<NAME> = "value"` line to `~/.config/skillet/secrets.toml` themselves.
  The server is not enabled until then: run the `enable` command again afterwards.
- Do not edit `~/.config/skillet/secrets.toml` or the agents' MCP config files by hand, and do not
  add a server definition to the catalog without the user's consent.

## For one session only

To give a single agent session extra skills without changing the project, the user starts it
through skillet:

```sh
skillet run @ios -- claude
```

The skills are linked, and the pack's servers written into that agent's user config, while the
command runs; both are undone when it exits. An agent cannot apply this to the session it is
running in; suggest the command to the user.

## Troubleshooting

| Output | Cause | What to do |
| --- | --- | --- |
| `conflict <path> exists and is not managed by skillet` | A file, folder or foreign link stands where the skill goes. | Show the path to the user. With their consent, rerun the same command with `--force`, which deletes that entry. |
| `missing <skill> is enabled but not found in its source` | The source is not cloned, or the skill left the repository. | Run `skillet update`, then `skillet sync`. If it stays missing, tell the user. |
| `extra <names>: enabled in <scope> but switched off …` | `sync` found skills or servers that are enabled although the scope's config file switches them off. They stay enabled. | Nothing, unless the user wants only what the file switches on: then `skillet sync --clean`, with their consent. |
| `unknown skill "<name>"` or `unknown pack "<name>"` | The name is not in the catalog. | Check `skillet list --json`. If the skill is not there, adding its source to `config.toml` is the user's call. |
| `the home directory cannot be a project` | `-p` was used in `~`. | Use the global scope, or change to the project directory. |
| `warning: gist …` | An included gist could not be reached; its cached copy is in use. | Carry on. Mention it if the user expected fresh data. |
| `skillet: command not found` | skillet is not installed. | Ask the user to install it: `mise use -g github:bonkey/skillet` or `go install github.com/bonkey/skillet@latest`. |
