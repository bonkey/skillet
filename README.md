# skillet

A local catalog of agent skills and MCP servers. Keep one list of what you have evaluated, group it
into packs, and enable it globally, per project, or for a single agent session.

Every installed skill puts its description into every agent session, whether the task needs it or
not. With [skills.sh](https://www.skills.sh) and `npx skills`, installed means loaded: the only way
to switch a skill off is to remove it, and then nothing remembers that you evaluated it. skillet
separates the two. The catalog keeps everything you trust; a session loads only what you enabled.

| | skills.sh / `npx skills` | skillet |
| --- | --- | --- |
| Skills you keep but do not load | remove them, find them again later | stay in the catalog, disabled |
| What is enabled | whatever is installed | the links on disk; config files only declare what is always on |
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

## Main uses

You write the catalog: sources, packs and servers go into `config.toml` by hand. skillet reads it
and switches things on and off. In commands, `@name` is a pack, `mcp:name` is an MCP server, and a
bare name is a skill. `-p` acts on the project around the working directory; the default is the
global scope. `skillet <command> --help` lists every flag.

```sh
skillet                                  # TUI: browse, search, mark what to switch, apply it all at once

skillet import --dry-run                 # take over an existing `npx skills` install
skillet import

skillet enable @craft                    # a whole pack, globally: its skills and its servers
skillet disable top-design mcp:simctl    # single members of it
skillet enable -p @craft                 # in this project
skillet run @craft -- claude             # only while the command runs
skillet sync                             # enable what the config files declare, repair the links
skillet sync --remove                    # and disable everything else

skillet list 'swift|ios' --enabled       # search names and descriptions; terms may be regexps
skillet update                           # fetch all sources, report changed skills
skillet --agents codex enable @craft     # act on these agents instead of those in the config
skillet gist push                        # store the config in a gist
skillet gist pull <gist>                 # load it on another machine
skillet gist include <gist>              # merge someone else's catalog into yours
```

The links are the state. `enable` and `disable` create and remove links, and entries in the agents'
MCP configs; they never write a config file. `[enabled]` in a config file lists what is always on:
`sync` enables it, clones sources that have no clone yet, gives every agent directory the same links
and lets a link follow a skill that moved inside its source. What is enabled without being declared
stays, and `sync` reports it as `extra`; `sync --remove` disables it. Commands print one line per
kind of change; `--verbose` prints every link.

`import` reads `~/.agents/.skill-lock.json`, fetches every source, creates one pack per source and
enables everything, in `config.toml` too. It **deletes** the folder of every imported skill in
`~/.agents/skills`; where a configured agent reads that directory, a link takes its place. Folders
the lock does not list stay.

[`skills/skillet`](skills/skillet/SKILL.md) teaches agents to search the catalog before looking for
skills elsewhere, and to enable and disable what they find.
[`examples/config.toml`](examples/config.toml) is a starting config with that skill enabled.

## Config

`~/.config/skillet/config.toml` holds the catalog and what is always enabled globally:

```toml
gist = "0123456789abcdef0123456789abcdef"      # written by `gist push`
includes = ["fedcba9876543210fedcba9876543210"] # gists merged into this catalog
agents = ["claude-code", "codex", "zed"]

[[secrets]]                                    # 1Password items whose fields fill ${NAME}
account = "my.1password.com"
vault = "abcdefghij"
item = "klmnopqrst"

[sources."wondelai/skills"]
url = "https://github.com/wondelai/skills.git"
ref = "main"                                   # optional: branch, tag, or full commit hash
skills = ["clean-code", "top-design"]          # optional: without it, every skill the source offers

[sources."dietrichgebert/ponytail"]
url = "https://github.com/dietrichgebert/ponytail.git"

[mcps.simctl]
type = "local"
command = ["npx", "-y", "simctl-mcp"]

[mcps.tavily]
type = "remote"                                # transport = "sse" for an SSE endpoint
url = "https://mcp.tavily.com/mcp/?tavilyApiKey=${TAVILY_API_KEY}"

[packs.craft]
description = "Code craft frameworks"
sources = ["dietrichgebert/ponytail"]          # every skill of these sources
skills = ["clean-code@wondelai/skills"]        # plus single skills
mcps = ["simctl"]                              # plus servers

[enabled]                                      # always on; `sync` enables it
packs = ["craft"]                              # everything in these packs
skills = ["top-design@wondelai/skills"]        # plus these skills
mcps = ["tavily"]                              # plus these servers
except = ["mcp:simctl"]                        # minus these
```

- **Skills** are written `name@owner/repo` or just `name`; commands accept both. A reference with
  a source stops resolving when the skill comes from another source.
- **Sources** without `skills` take every skill their repository offers, and `update` brings in the
  ones they gain. `update` moves a branch `ref` forward; a tag or a commit keeps the version you
  evaluated. `skillet sources` shows the commit each clone is at.
- **Projects** may hold a `.skillet.toml` of the same shape: its sources, packs and servers join
  the catalog while you work in that project, where the global config wins a name clash, and its
  `[enabled]` is what `sync -p` enables there. The project is the nearest directory up the tree with
  a `.skillet.toml`, or else with a git repository, or else the working directory. Project skills
  add to the global ones. MCP servers are global: a project cannot enable them, `skillet run` can.
- **Agents**: `claude-code`, `codex`, `cursor`, `gemini-cli`, `github-copilot`, `opencode`, `pi`
  get skills; `claude-code`, `codex`, `crush`, `cursor`, `gemini-cli`, `opencode`, `zed` get MCP
  servers.
- **MCP servers** go into each agent's user config (`~/.claude.json`, `~/.codex/config.toml`, …)
  in that agent's own format. An entry of the same name that is already there is that server:
  skillet overwrites it and manages it from then on, so disabling the server, or the end of a `run`
  that brought it, removes it. Entries under other names stay untouched. A server may also carry
  `environment`, `headers`, `timeout` (seconds) and `disabled_tools`.
- **Secrets** are written `${NAME}` in a server definition. The value is the field labelled `NAME`
  of a 1Password item under `secrets` (the `h`, `v` and `i` of the item's private link are its
  account, vault and item); the first item that has the field wins, and a `NAME = "value"` line in
  `~/.config/skillet/secrets.toml` overrides it. Values are filled in when an agent's config is
  written, so they never reach the config or its gist. 1Password is asked only when an entry has to
  be written; a server without its secret is left as it is and reported.
- **Included gists** contribute their sources, servers, packs and what they enable. Your own entries
  win a name clash, then the earlier include. Put included entries in your own packs, or override a
  pack with one of the same name. Gists may include gists; each takes part once, so cycles are
  harmless. The `secrets` of an included gist are ignored.

## How it works

- Each source is shallow-cloned into `~/.local/share/skillet/repos/<owner>/<repo>`.
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
