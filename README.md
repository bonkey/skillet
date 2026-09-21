# skillet

A local catalog of agent skills. Keep one list of the skills you have evaluated, group them into
packs, and enable them globally, per project, or for a single agent session.

skillet links whole skill folders, so multi-file skills arrive complete. Packs on
[skills.sh](https://www.skills.sh/docs/packs) install only a skill's `SKILL.md`
([vercel-labs/skills#2197](https://github.com/vercel-labs/skills/issues/2197)).

## Install

```sh
mise use -g ubi:bonkey/skillet               # release binary, through mise
go install github.com/bonkey/skillet@latest  # from source, with Go
```

Pin a version with `ubi:bonkey/skillet@0.1.0`. Archives for macOS and Linux are attached to each
[release](https://github.com/bonkey/skillet/releases). skillet needs `git`, and the `gh` CLI for
the gist commands.

## Main uses

In commands, `@name` is a pack and a bare name is a skill. `-p` acts on the project around the
working directory; the default is the global scope. `skillet <command> --help` lists every flag.

```sh
skillet                                  # TUI: browse, toggle, add, remove

skillet import --dry-run                 # take over an existing `npx skills` install
skillet import

skillet add wondelai/skills              # list what a source offers
skillet add wondelai/skills --skill clean-code,top-design \
    --pack craft --pack-description "Code craft"

skillet enable @craft                    # a whole pack, globally
skillet disable top-design               # one skill of it
skillet enable -p @craft                 # in this project; writes .skillet.yaml
skillet run @craft -- claude             # only while the command runs

skillet list                             # packs and skills with descriptions and state
skillet update                           # fetch all sources, report changed skills
skillet ref wondelai/skills v2.1         # track a branch, tag, or full commit hash
skillet gist push                        # store the catalog in a gist
skillet gist pull <gist>                 # load it on another machine
skillet gist include <gist>              # merge someone else's catalog into yours
```

TUI keys: `space` toggles a skill or a whole pack, `tab` switches between the global and the
project scope, `/` filters, `a` adds a source, `d` removes, `p` changes pack membership, `e` edits a
pack description, `r` changes a source's ref, `u` updates, `q` quits.

`import` reads `~/.agents/.skill-lock.json`. It takes the source and name of each skill from the
lock, fetches the content from the source, creates one pack per source, and enables everything
globally. It **deletes** the folder of every imported skill in `~/.agents/skills` and puts a link
in its place. Folders the lock does not list stay.

## Catalog

`~/.config/skillet/catalog.yaml` holds the catalog and the global enabled set:

```yaml
gist: 0123456789abcdef0123456789abcdef     # written by `gist push`
includes:                                  # gists merged into this catalog
  - fedcba9876543210fedcba9876543210
agents: [claude-code]
sources:
  wondelai/skills:
    url: https://github.com/wondelai/skills.git
    ref: main            # optional: branch, tag, or full commit hash
    skills: [clean-code, top-design]
packs:
  craft:
    description: Code craft frameworks
    skills: [clean-code]
enabled:
  packs: [craft]         # every skill of these packs
  skills: [top-design]   # plus these skills
  except: []             # minus these
```

A project keeps its own `packs`, `skills`, and `except` in `.skillet.yaml` at its root. Project
skills add to the global ones.

Agents: `claude-code`, `codex`, `cursor`, `gemini-cli`, `github-copilot`, `opencode`.

**Versions.** All skills of a source share its `ref`. `update` moves a branch forward. A tag or a
commit keeps the source at the version you evaluated. `skillet sources` shows the commit each
clone is at.

**Included gists.** An included gist contributes its sources, packs, and what it enables. Your own
entries win a name clash, then the earlier include. Included entries are read-only: disable an
included skill, put it in your own packs, or override a pack by creating one of the same name.
Gists may include further gists. Each gist takes part once, so gists that include each other are
safe. `update` refreshes the cached copies; an unreachable gist falls back to its cache with a
warning. `skillet gist list` shows the tree.

## How it works

- Each source is shallow-cloned into `~/.local/share/skillet/repos/<owner>/<repo>`.
- An enabled skill is a chain of two symlinks: `~/.agents/skills/<name>` points into the clone, and
  each agent directory such as `~/.claude/skills/<name>` points at `../../.agents/skills/<name>`.
  Projects use `./.agents/skills` the same way. Enabling and disabling works offline.
- skillet touches only links of this chain. Other directories and links stay as they are.
- Descriptions come from each skill's `SKILL.md`, cached in `~/.local/share/skillet/index.json`.
- `run` tracks sessions in `.claude/skills/.skillet-sessions/`. Parallel sessions keep each
  other's links, and the next `sync` or `run` cleans up after a crashed one.
- The links are machine-local: add `.claude/skills/` and `.agents/skills/` to the project's ignore
  file.

## Development

```sh
just check            # gofmt, go vet, go test
just release 1.2.3    # tag, build archives, publish; see AGENTS.md
```
