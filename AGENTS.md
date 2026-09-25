# Agent Instructions

skillet is a Go CLI and TUI that keeps a local catalog of agent skills and enables them with
symlinks. `README.md` describes the behaviour; keep it short and focused on the main uses.

## Layout

- `main.go` — cobra commands; output formatting only
- `internal/catalog` — the catalog file, packs, enabled flags, merging of `config.local.toml`, of
  included catalogs and of a project's `.skillet.toml`
- `internal/source` — git clones, skill discovery, the description index, sources written the
  way `npx skills add` takes them
- `internal/link` — the symlinks into the clones and their ownership rules; the links are the
  enabled state
- `internal/session` — `skillet run` sessions
- `internal/mcp` — MCP server entries in the agents' user configs: per-agent shapes, a
  comment-preserving JSON editor, a text-level TOML section editor, ownership state, a probe
  that tells an MCP endpoint from other URLs
- `internal/secrets` — the values behind `${NAME}` placeholders: a local store and 1Password
  items, read lazily through the `op` CLI
- `internal/gist` — gist access through `gh api`
- `internal/importer` — the lock file of the `skills` npm CLI and its `skills add` commands
- `internal/app` — every operation; the CLI and the TUI both call it
- `internal/tui` — Bubble Tea front end; view state only
- `skills/skillet/SKILL.md` — the skill that teaches agents to enable and disable skills with the CLI
- `examples/config.toml` — a starting catalog with that skill enabled; a test loads it

`App.Local` is the user's catalog file. `App.Catalog` is `Local` merged with the included gists and
the project's manifest, and is what everything reads. `enable`, `disable`, `sync` and `run` change
links and the agents' MCP entries; only `enable` and `disable` with `--save`, `add`, `import` and
the `gist` commands write a config file.

## Checks

Run `just check` before every commit. It runs `gofmt -l`, `go vet` and `go test` for all packages.

Tests run against temporary directories and local git repositories. They never touch the real home
directory, the network or GitHub; gists are faked through the `gist.Client` interface. Keep it so.

Secret values never go into the catalog, test output, logs or error messages; tests use made-up
values. When inspecting a user's agent configs, print names and structure only.

When changing behaviour that touches the home directory, also try it with a throwaway home:
`HOME=$(mktemp -d) XDG_CONFIG_HOME= XDG_DATA_HOME= go run . <command>`.

## Documentation

- Every change in behaviour updates `README.md`; a change to a command, flag, catalog key or TUI
  key also updates the command's help text.
- A change to `list --json`, `enable`, `disable`, `sync`, `run`, the scope flags or the sync output
  lines (skills and MCP) also updates `skills/skillet/SKILL.md`; agents act on what it says.
- An agent's MCP entry shape in `internal/mcp/targets.go` follows that agent's documentation;
  cite the source in the commit that changes it.
- Docs and comments describe the current behaviour, without history.

## Releasing

Releases are cut with one command from a clean, pushed `main`:

```sh
just release 1.2.3
```

It runs these recipes in order, and stops at the first failure:

1. `tag` — checks that the version looks like `1.2.3`, the branch is `main`, the tree is clean,
   `main` equals `origin/main` and the tag is free; runs `just check`; creates the signed tag
   `v1.2.3` and pushes it.
2. `dist` — cross-compiles darwin and linux archives for arm64 and amd64 into `dist/`, with the
   version compiled in, and writes `dist/checksums.txt`.
3. `publish` — creates the GitHub release for the tag with generated notes and uploads `dist/`.

Rules:

- A job is done when it is released: after the work is committed and pushed, cut the release in
  the same session.
- Never tag, build release archives or create a GitHub release by hand; use the recipes so every
  release is made the same way.
- Choose the version by semantic versioning. While the major version is 0: a fix raises the patch
  version; a new command, flag, key or other addition raises the minor version; so does a
  breaking change to the catalog format, a command or a flag.
- A published tag is never moved or deleted. Fix a bad release with a new patch version.
- If `publish` fails after the tag is pushed, fix the cause and rerun `just dist 1.2.3` and
  `just publish 1.2.3`; do not rerun `release`.
- Signing the tag needs the 1Password SSH agent, so the command must run outside a sandbox.
