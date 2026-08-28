# Coding

Read [OVERVIEW.md](OVERVIEW.md) and the specs in `specs/` first. For the package
map and the test layers, read
[implementation/PACKAGE-OVERVIEW.md](implementation/PACKAGE-OVERVIEW.md) and
[implementation/TESTING-STRATEGY.md](implementation/TESTING-STRATEGY.md); for the
logging/observability design, see [MONITORING.md](MONITORING.md).

## Build & test (mise; `make` still works)

```bash
mise run build             # -> ./bin/taskmgr
mise run fmt
mise run vet
mise run lint
mise run test              # L1 pure + L2 store-on-Mem (fast, both modules)
mise run test:integration  # L3 real temp dir + L4 CLI
mise run quality           # fmt + vet + lint + docs + test  (pre-commit gate)
mise run quality:full      # + L3/L4                         (pre-handoff gate)
```

One task per `mise run`: trailing words become arguments to the first task, so
`mise run fmt vet lint` runs `fmt` with two bogus paths instead of three tasks.

## Single-writer rule

Only `sdk/tasks` — through `internal/vfs` — touches files under `.tasks/`. `cmd/`
and every consumer go through the `Store` API. **Within `sdk/tasks`, only the three
seams `internal/vfs` (disk), `internal/exec` (hook processes), and `internal/env`
(user environment — CONFIG-SPEC) may import `os`/`syscall`;** the pure core imports
none of them. Enforced by `sdk/tasks/importboundary_test.go`. The rule is SDK-only:
`cmd/` is the process boundary and reads `os` directly for args, env, and exit codes.

## Where changes go

| Change | Goes in |
|---|---|
| CLI command / flag | `cmd/` (wired in `root.go`); calls `Store`, never the FS. One flag struct per command, named `<command>Flags` — never shared between sibling subcommands. The only package-level flag variables are `flagJSON`/`flagDir`/`flagStoreName`, which are root *persistent* flags every command reads |
| Stored field / store behaviour | `sdk/tasks` (`model`/`frontmatter`/`validate`/`store`) |
| Filter-expression language | `sdk/tasks/internal/query` (pure; no `os`, no `tasks` import) |
| Any disk operation | `sdk/tasks/internal/vfs` (the seam) — never inline `os` elsewhere |
| Spawning a hook process | `sdk/tasks/internal/exec` (the process seam) — never inline `os/exec` elsewhere |
| Reading the environment (home, any `TASKMGR_*`) | `sdk/tasks/internal/env` (the env seam) — never inline `os.Getenv`/`os.UserHomeDir` elsewhere |
| Store resolution / global config / registry | `sdk/tasks` (`resolve.go` pure matching; `config.go`/`registry.go` shell, via the vfs/env seams) — see [CONFIG-SPEC](specs/CONFIG-SPEC.md) |
| Hook config / orchestration | `sdk/tasks` (`hooks.go` config+validation, `hookrun.go` run, `hookpayload.go`) |
| Pure logic (`ids`, `ready`, `resolve`) | its own file in `sdk/tasks`, no FS import **and no `*Store` method** → unit-tests at L1 |
| Repo tooling that ships with neither binary | `scripts/<name>/` — its own `main` package, stdlib only, run from a `mise` task |

## How to test

- Pure logic → **L1** (no FS). Store orchestration & error paths → **L2** on
  `vfs.Mem` (with fault injection). Durability, `flock`, round-trip → **L3** real
  temp dir. CLI → **L4**, in-process through `cmd.Run(args, stdout, stderr) int`
  unless the process itself is the subject (exit codes as a shell sees them,
  stdin, signals, two processes contending the lock) — those fork the binary
  behind the `integration` tag.
- Build fixtures with `sdk/tasks/internal/storetest`; never hand-roll a real
  `.tasks/`. Deterministic time via `Store.now` inside package `tasks`, via
  `Store.SetNow` from `cmd/` and external consumers. Details in
  [TESTING-STRATEGY.md](implementation/TESTING-STRATEGY.md).

## Keep specs in sync

A change to a CLI command/flag or a public `sdk/tasks` function/type/semantics
**must update the matching spec in the same change** ([CLI](specs/CLI-SPEC.md),
[SDK](specs/SDK-SPEC.md), [STORAGE](specs/TASK-STORAGE-SPEC.md),
[QUERY](specs/QUERY-SPEC.md)). A change to hook events, config, or payloads updates
[HOOK](specs/HOOK-SPEC.md). A change to config, the central registry, or store
resolution updates [CONFIG](specs/CONFIG-SPEC.md). A structural change (packages, a
seam) updates [ARCHITECTURE](specs/ARCHITECTURE-SPEC.md) §5. A mismatch is a bug.

A change a **user** would notice — a command, a flag, a JSON field, an error message —
also updates the page covering it in [user-guide/](user-guide/) in the same change. No
gate catches a missing one; [REVIEWING.md](REVIEWING.md) is where it is checked.

## Modules

Two modules: root (the CLI) and `sdk/` (minimal-dep — only `yaml.v3`). The
committed `go.work` wires local builds to the in-tree SDK; the root `go.mod` has no
`replace` and pins the published `sdk vX.Y.Z` for consumers. `mise run build:all`
compiles both so a cross-module break fails the gate.
Run `mise run tidy` after changing imports. Authoritative:
[ARCHITECTURE-SPEC §4](specs/ARCHITECTURE-SPEC.md).
