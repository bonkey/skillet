# skillet

A local catalog of agent skills. Keep one list of the skills you have evaluated, group them into
packs, and enable them globally, per project, or for a single agent session.

Every installed skill puts its description into every agent session, whether the task needs it or
not. With [skills.sh](https://www.skills.sh) and `npx skills`, installed means loaded: the only way
to switch a skill off is to remove it, and then nothing remembers that you evaluated it. skillet
separates the two. The catalog keeps everything you trust; a session loads only what you enabled.

| | skills.sh / `npx skills` | skillet |
| --- | --- | --- |
| Skills you keep but do not load | remove them, find them again later | stay in the catalog, disabled |
| Switching on and off | reinstall over the network | one command or one key, offline |
| Packs | built in a web UI, stored in a Vercel account | a few lines of local YAML with a description, toggled as a unit |
| Scope | global or project | global, project, or one command: `skillet run @ios -- claude` |
| Moving to another machine, sharing | a pack URL, or a lock file per project | the whole catalog in a gist, which can include other people's gists |

A TUI shows every skill with its description and state, and each source tracks a branch, a tag or
a commit. `skillet import` takes over an existing `npx skills` install.

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

## The skillet skill

The repository ships a skill, [`skills/skillet`](skills/skillet/SKILL.md), that teaches agents to
search the catalog before looking for skills elsewhere, and to enable and disable skills and packs.
Install it with skillet itself:

```sh
skillet add bonkey/skillet --all --enable     # into the catalog, enabled globally

skillet add bonkey/skillet --all \
    --pack skillet --pack-description "Lets agents manage skills with skillet"
skillet enable -p @skillet                    # or only in this project
skillet run @skillet -- claude                # or only for one session
```

On a machine without a catalog, [`examples/catalog.yaml`](examples/catalog.yaml) is a starting
catalog with the skill enabled:

```sh
mkdir -p ~/.config/skillet
curl -fsSL https://raw.githubusercontent.com/bonkey/skillet/main/examples/catalog.yaml \
    -o ~/.config/skillet/catalog.yaml
skillet update                                # clones the sources and links what is enabled
```

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

Agents: `claude-code`, `codex`, `cursor`, `gemini-cli`, `github-copilot`, `opencode`, `pi`.

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
- skillet touches only links of this chain. Other directories and links stay as they are, and one
  that stands where an enabled skill goes is reported as a conflict. `--force`, on any command,
  deletes such an entry and links the skill.
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
