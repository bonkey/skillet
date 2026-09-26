# skillet

A local catalog of agent skills and MCP servers. Keep one list of what you have evaluated, group it
into packs, and switch it on globally, per project, or for a single agent session.

Every skill in an agent's skills directory costs context in every session, whether the task needs
it or not, and a long list makes it harder for the agent to pick the right one. With
[skills.sh](https://www.skills.sh) and `npx skills`, as with the agents' own installers, installed
means loaded: the only way to switch a skill off is to remove it, and then nothing remembers that
you evaluated it. skillet separates the two. The catalog keeps everything you trust; a session
loads only what you switched on.

- **Small context, on purpose.** Switch on the pack for the work at hand, `@ios` for the app,
  `@craft` for a review, and the agent sees the handful of skills the task needs.
- **Evaluate once, keep it.** A skill you tried and set aside stays in the catalog, off, with its
  source and the commit you pinned. Finding it again is one search, offline.
- **Skills and MCP servers together.** A pack holds both. Switching it on writes the servers into
  every agent's config in that agent's own format, with secrets read from 1Password at write time
  and never stored in the catalog.
- **One catalog, every agent.** Claude Code, Codex, Cursor, Gemini CLI, opencode and others get
  the same links and entries; one command changes all of them.
- **Scopes that match the work.** Global for what you always want, a project for one repository,
  or `skillet run @ios -- claude` for a single session that cleans up after itself.
- **Portable.** The catalog is one TOML file: push it to a gist, pull it on another machine, or
  include a colleague's gist and override what you see differently.

| | skills.sh / `npx skills` | skillet |
| --- | --- | --- |
| Skills you keep but do not load | remove them, find them again later | stay in the catalog, disabled |
| What is enabled | whatever is installed | the links on disk; config files say what is on by default |
| Switching on and off | reinstall over the network | one command or one key, offline |
| Packs | built in a web UI, stored in a Vercel account | a few lines of local TOML, skills and MCP servers together |
| Scope | global or project | global, project, or one command: `skillet run @ios -- claude` |
| Moving to another machine, sharing | a pack URL, or a lock file per project | the whole catalog in a gist, which can include other people's gists |

## Install

```sh
mise use -g github:bonkey/skillet            # release binary, through mise
go install github.com/bonkey/skillet@latest  # from source, with Go
```

skillet needs `git`; the gist commands need the `gh` CLI, and 1Password secrets the `op` CLI.
Archives for macOS and Linux are attached to each
[release](https://github.com/bonkey/skillet/releases).

## Quick start

1. Write the catalog. [`examples/config.toml`](examples/config.toml) is a start with one skill:

   ```sh
   mkdir -p ~/.config/skillet && cp examples/config.toml ~/.config/skillet/config.toml
   ```

   Add a `[[skills]]` entry per repository you want and a `[[mcps]]` entry per server, then group
   them into packs. The [Config](#config) section shows every key. `skillet add` writes the
   entries for you:

   ```sh
   skillet add -- npx skills add bonkey/skills -g --skill captains-log -y
   skillet add -- npx @mobilenext/mobile-mcp@latest
   ```

2. Switch it on. `sync` clones the sources, links every skill that is on into the agents' skills
   directories and writes the servers into their configs:

   ```sh
   skillet sync
   skillet status                           # what is on, per agent
   ```

3. Take over what is already there. `import` reads the lock file of `npx skills` and the MCP
   servers in the configs of Claude Code, Codex and Gemini CLI into the catalog, moving values
   that look like secrets into `~/.config/skillet/secrets.toml`:

   ```sh
   skillet import --dry-run
   skillet import
   ```

   Skills installed by hand and servers the catalog does not know stay until you say so. `sync
   --purge` deletes them from the agents, `sync --disable-all` takes out everything skillet manages,
   `--disable-skills` and `--disable-mcps` one kind of it, and all accept `--dry-run`:

   ```sh
   skillet sync --purge --dry-run           # what would go
   skillet sync --purge                     # the agents hold the catalog, nothing else
   ```

## Main uses

You write the catalog: packs go into `config.toml` by hand, sources and servers by hand or with
`skillet add`. skillet reads it and switches things on and off. In commands, `@name` is a pack, `mcp:name` is an MCP server,
`skills:name` is a whole source and a bare name is a skill. `-p` acts on the project around the
working directory; the default is the global scope. `skillet <command> --help` lists every flag.

```sh
skillet                                  # TUI: browse, search, mark what to switch, apply or save it at once, undo, reset

skillet import --dry-run                 # take over an existing `npx skills` install
skillet import

skillet add bonkey/skills --skill pr     # a source, or some of its skills; `sync` enables them
skillet add -- npx skills add bonkey/skills -g --skill captains-log -y   # a pasted `npx skills` command
skillet add https://mcp.exa.ai/mcp       # a server: a URL that is no git repository
skillet add -- npx @mobilenext/mobile-mcp@latest   # a server's command
skillet add mcp <url>                    # a server, without checking the URL

skillet enable @craft                    # a whole pack, globally: its skills and its servers
skillet disable top-design mcp:simctl-mcp  # single members of it
skillet disable --save @craft            # and switch it off in the config, so sync keeps it off
skillet enable -p @craft                 # in this project
skillet run @craft -- claude             # only while the command runs
skillet sync                             # enable what the config files switch on, repair the links
skillet sync --clean                     # and disable what they switch off
skillet sync --purge                     # and delete the skills and servers skillet does not manage
skillet sync --disable-mcps              # remove every server entry skillet manages; the config still switches them on
skillet sync --disable-all               # disable everything skillet manages

skillet status                           # every skill and server with its state per agent
skillet status --unmanaged               # and those skillet does not manage
skillet list 'swift|ios' --enabled       # search names and descriptions; terms may be regexps
skillet sources                          # every source with the ref it tracks and its commit
skillet update                           # fetch all sources, report changed skills
skillet --agents codex enable @craft     # act on these agents instead of those in the config
skillet gist push                        # store the config in a gist
skillet gist pull <gist>                 # load it on another machine
skillet gist include <gist>              # merge someone else's catalog into yours
```

The links are the state. `enable` and `disable` create and remove links, and entries in the agents'
MCP configs; with `--save` they also flip the entry's `enabled` flag in the config file. Every entry
of a config file is on unless it says `enabled = false`: `sync` enables what is on, clones sources
that have no clone yet, gives every agent directory the same links and lets a link follow a skill
that moved inside its source. What is enabled although its config file switches it off stays, and
`sync` reports it as `extra`; `sync --clean` disables it. Commands print what they changed in two tables with
one column per agent: one row per skill, named by its folder inside the clones, and one row per
server, grouped under their pack, or under `unmanaged` when skillet does not manage it.
A cell is added, repaired, removed, a conflict, already right,
or absent where the agent lacks the entry. On a terminal the cells are Nerd Font symbols; piped,
they are letters. `skillet status` prints the same tables for everything and changes nothing: a
cell is on, not on the disk although the config switches it on (`sync` enables it), on the disk
although the config switches it off (`sync --clean` disables it), in need of repair, a conflict or
absent. A pack that is off is headed `off` and lists its members. `status --unmanaged` also lists
the skills and servers skillet does not manage; `status --json` prints it for programs.
`--verbose` prints every link and server entry instead, also those that were already right, and then the enabled
skills and servers grouped by pack, the change grouped the same way, and the packs with nothing
on.

`add` takes a source as `npx skills add` does (`owner/repo`, `owner/repo@skill`,
`owner/repo/path`, GitHub and GitLab URLs, git URLs and local paths, with an optional `#ref`), or a
whole `npx skills add` command after `--`, of which `--skill` and `--all` count. It clones the
source to check the skills and appends the entry to `config.toml`, or with `-p` to the project's
`.skillet.toml`, where an existing entry changes only its `only` list. Comments stay: a list that
holds comments is left for you to change. It links nothing. A URL where git finds no repository
is a server when an MCP server answers there, a command after `--` other than `skills add` starts
a server, and `add mcp` takes a server without checking. Secret-looking URL parameters are
appended to `secrets.toml`.

`import` reads `~/.agents/.skill-lock.json`, fetches every source, creates one pack per source and
enables everything. It **deletes** the folder of every imported skill in
`~/.agents/skills`; where a configured agent reads that directory, a link takes its place. Folders
the lock does not list stay. It also reads the MCP servers of Claude Code, Codex and Gemini CLI
into the catalog: an environment variable, header or URL parameter whose name looks like a secret
becomes a `${NAME}` placeholder, and its value goes to `secrets.toml`.

[`skills/skillet`](skills/skillet/SKILL.md) teaches agents to search the catalog before looking for
skills elsewhere, and to enable and disable what they find.
[`examples/config.toml`](examples/config.toml) is a starting config with that skill enabled.

## Config

`~/.config/skillet/config.toml` holds the catalog:

```toml
agents = ["claude-code", "codex", "zed"]

[[packs]]
name = "craft"
description = "Code craft frameworks"
skills = ["ponytail", "clean-code@wondel"]     # a source name: every skill of it; else one skill
mcps = ["simctl-mcp"]

[[packs]]
name = "experiments"
description = "Not in use"
enabled = false                                # off, with everything it holds

[[skills]]                                     # a git repository, named after its last URL segment
url = "https://github.com/dietrichgebert/ponytail.git"

[[skills]]
name = "wondel"                                # optional: it would be "skills" otherwise
url = "https://github.com/wondelai/skills.git"
ref = "main"                                   # optional: branch, tag, or full commit hash
path = "apps/skills"                           # optional: the directory that holds the skills
only = ["clean-code", { name = "top-design", enabled = false }]   # optional: without it, every skill

[[mcps]]                                       # a command makes a local server, named after the package
command = ["npx", "-y", "simctl-mcp"]
environment = { HOME = "${HOME}" }

[[mcps]]                                       # a url makes a remote server, named after the host
url = "https://mcp.tavily.com/mcp/?tavilyApiKey=${TAVILY_API_KEY}"
enabled = false

[[secrets]]                                    # 1Password items whose fields fill ${NAME}
account = "my.1password.com"
vault = "abcdefghij"
item = "klmnopqrst"
```

- **Packs** group skills and servers. A member of `skills` that names a source stands for every
  skill of that source; `name@source` is always one skill, also when a skill and a source share a
  name. Everything is on unless switched off: `enabled = false` switches a pack off with all it
  holds, and a skill or server that packs hold is on while one of those packs is.
- **Skills** entries are git repositories. A source is named after the last segment of its URL;
  when two sources without a `name` share that segment, both become `owner-repo`. `only` limits
  the source to the listed skills; without it, `update` brings in the skills the source gains.
  `update` moves a branch `ref` forward; a tag or a commit keeps the version you evaluated.
  `path` takes the skills from that directory of the repository and ignores the rest; two
  entries of one repository with different paths need distinct `name`s.
  `enabled = false` switches a source off; an `only` entry written `{ name = "x", enabled = false }`
  switches one skill off, also in a source that takes all. A list keeps at least one skill on,
  since a list without one takes all: switch the source off instead. Skills are written `name` or
  `name@source`; a reference with the wrong source does not resolve. In commands, `skills:name`
  is the whole source.
- **MCP servers** are local with `command` and remote with `url`. A server is named after the
  package a runner such as `npx`, `uvx` or `docker run` starts, else after its command, else
  after the label before the top-level domain of its host (`mcp.exa.ai` gives `exa`). A server
  may also carry `environment`, `headers`, `timeout` (seconds), `transport = "sse"` for an SSE
  endpoint, `disabled_tools`, and `enabled = false`. Servers go into each agent's user config (`~/.claude.json`,
  `~/.codex/config.toml`, …) in that agent's own format; an entry of the same name that is already
  there is taken over, and entries under other names stay untouched.
- **Secrets**: `${NAME}` in a server definition is the field labelled `NAME` of a 1Password item
  under `secrets` (the `h`, `v` and `i` of the item's private link are its account, vault and
  item); the first item that has the field wins, and a `NAME = "value"` line in
  `~/.config/skillet/secrets.toml` overrides it. Values are filled in when an agent's config is
  written, so they never reach the catalog or its gist; a server without its secret is left as
  it is and reported.
- **Agents**: `claude-code`, `codex`, `cursor`, `gemini-cli`, `github-copilot`, `opencode`, `pi`
  get skills; `claude-code`, `codex`, `crush`, `cursor`, `gemini-cli`, `opencode`, `zed` get MCP
  servers.
- **Projects** may hold a `.skillet.toml` of the same shape: its entries join the catalog while
  you work in that project, where the global config wins a name clash, and its own entries are
  what `sync -p` switches on there; a manifest flags only its own entries. The project is the
  nearest directory up the tree with a `.skillet.toml`, or else with a git repository, or else
  the working directory. MCP servers are global: a project cannot enable them, `skillet run` can.
- **Gists**: `gist = "<id>"` names the gist `gist push` and `gist pull` use; `includes = ["<id>"]`
  merges other catalogs into yours. Your own entries win a name clash, then the earlier include.
  Gists may include gists; each takes part once, so cycles are harmless. The `secrets` of an
  included gist are ignored. `config.local.toml` stays on the machine: `gist push` does not send
  it and `gist pull` does not touch it.

### Local overrides

`~/.config/skillet/config.local.toml`, when present, overrides `config.toml` on this machine. It
takes the same entries, and a same-named source, server or pack replaces the one in `config.toml`;
`agents` replaces the list and `secrets` are added. Two more keys switch entries of either file on
or off by the names the commands take; `disabled` wins over `enabled`:

```toml
enabled = ["@experiments", "top-design"]
disabled = ["mcp:simctl-mcp", "skills:ponytail"]

[[mcps]]                                       # this machine reaches Tavily through a proxy
name = "tavily"
url = "http://localhost:8080/tavily"
```

`enable --save` and `disable --save` write `config.toml`; a name the local file defines or lists
is refused, since the local file would win: change it there.

## How it works

- Each source is shallow-cloned into `~/.local/share/skillet/repos/<name>`.
- An enabled skill is a symlink in every agent directory, such as `~/.claude/skills/<name>` or a
  project's `.agents/skills/<name>`, that points straight into the clone with an absolute path. The
  whole skill folder is linked, and enabling or disabling works offline. An enabled server is an
  entry skillet wrote into an agent's config.
- skillet touches only links that lead into its clones. Something else that stands where an enabled
  skill goes is reported as a conflict; `--force`, on any command, deletes it and links the skill.
- Agent configs are edited in place: comments, key order and everything outside the server entries
  stay as they are.
- `run` changes real files: the skills appear in the project, and the servers in the user config of
  the agent the command starts (`claude`, `codex`, `gemini`, …). Another session of that agent that
  starts meanwhile sees them too; a running one picks changes up after a restart. Parallel runs
  keep each other's entries, what was enabled before stays enabled, and the next `sync` cleans up
  after a crashed one.
- The links are machine-local: add `.claude/skills/` and `.agents/skills/` to a project's ignore
  file. `.skillet.toml` can be committed.

## Development

```sh
just check            # gofmt, go vet, go test
just release 1.2.3    # tag, build archives, publish; see AGENTS.md
```
