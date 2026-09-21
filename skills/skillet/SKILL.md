---
name: skillet
description: "Finds, enables and disables agent skills and whole packs of skills with the skillet CLI, globally or for the current project. Use FIRST whenever a skill is wanted or missing: before searching the web or a skill directory, installing a skill from elsewhere, or writing a new one, search the local skillet catalog. Also use when the user asks to enable, disable, turn on, turn off, load or unload a skill or a pack, asks which skills are enabled or available, says too many skills are loaded, wants the skills for a kind of work (for example 'load the iOS skills for this project'), or mentions skillet."
---

# skillet

`skillet` keeps a catalog of evaluated skills, grouped into packs. An enabled skill is a symlink in
the agent's skills directory; a disabled skill stays in the catalog. In commands, `@name` is a pack
and a bare name is a skill.

## Search the catalog first

The catalog holds the skills the user has already evaluated, and most of them are disabled at any
time. A skill that is not loaded in this session is therefore usually one command away. Whenever a
skill would help with the task, or the user asks for one, search the catalog before anything else:

```sh
skillet list --json | jq -r '.skills[]
  | select((.name + " " + .description) | test("swift|ios|xcode"; "i"))
  | [.name, (if .global or .project then "enabled" else "disabled" end), .description] | @tsv'
```

Search with several words for the same need, and look at pack descriptions too (`.packs[]`).
Without `jq`, read `skillet list --json` directly.

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
- To switch a skill off, use `disable`. `remove` deletes it from the catalog: run it only when the user asks for exactly that.
- Never add `--force` on your own. It deletes whatever stands in the way of a link. When a command prints a `conflict` line, show it to the user and ask.
- Adding skills to the catalog (`skillet add`) is the user's decision: the catalog holds skills the user has evaluated. Propose the command and wait for a yes.
- Agents read their skills directories when a session starts. After a change, tell the user that a skill that does not show up yet is available in the next session.

## Choosing the scope

| The user means | Flag | Effect |
| --- | --- | --- |
| this project, this repo, this task | `-p` | Links in `./.claude/skills` and `./.agents/skills`; the set is saved in `.skillet.yaml` at the project root. Without a `.skillet.yaml` up the tree, `-p` creates one in the working directory. |
| everywhere, always, by default | none | Links in `~/.claude/skills` and `~/.agents/skills`; the set is saved in `~/.config/skillet/catalog.yaml`. |

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

   Disabling one skill of an enabled pack keeps the pack enabled and records the skill as an exception.

4. Read the output. Each line is one change: `link`, `unlink`, `relink`, `replace`. No output
   means the state already matched. Handle `conflict` and `missing` lines as described below.

5. Report what is enabled now and in which scope, and mention the next-session caveat.

## For one session only

To give a single agent session extra skills without changing the project, the user starts it
through skillet:

```sh
skillet run @ios -- claude
```

The skills are linked while the command runs and unlinked when it exits. An agent cannot apply
this to the session it is running in; suggest the command to the user.

## Troubleshooting

| Output | Cause | What to do |
| --- | --- | --- |
| `conflict <path> exists and is not managed by skillet` | A file, folder or foreign link stands where the skill goes. | Show the path to the user. With their consent, rerun the same command with `--force`, which deletes that entry. |
| `missing <skill> is enabled but not found in its source` | The source is not cloned, or the skill left the repository. | Run `skillet update`, then `skillet sync`. If it stays missing, tell the user. |
| `unknown skill "<name>"` or `unknown pack "<name>"` | The name is not in the catalog. | Check `skillet list --json`. If the skill is not there, `skillet add <owner/repo>` lists what a source offers; adding is the user's call. |
| `the home directory cannot be a project` | `-p` was used in `~`. | Use the global scope, or change to the project directory. |
| `warning: gist …` | An included gist could not be reached; its cached copy is in use. | Carry on. Mention it if the user expected fresh data. |
| `skillet: command not found` | skillet is not installed. | Ask the user to install it: `mise use -g github:bonkey/skillet` or `go install github.com/bonkey/skillet@latest`. |
