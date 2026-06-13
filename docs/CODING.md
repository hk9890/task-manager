# Coding

Read [OVERVIEW.md](OVERVIEW.md) and the specs in `specs/` first. For the package
map and the test layers, read
[implementation/PACKAGE-OVERVIEW.md](implementation/PACKAGE-OVERVIEW.md) and
[implementation/TESTING-STRATEGY.md](implementation/TESTING-STRATEGY.md); for the
logging/observability design, see [implementation/MONITORING.md](implementation/MONITORING.md).

## Build & test (mise; `make` still works)

```bash
mise run build             # -> ./bin/taskmgr
mise run fmt vet lint
mise run test              # L1 pure + L2 store-on-Mem (fast, both modules)
mise run test:integration  # L3 real temp dir + L4 CLI
mise run quality           # vet + lint + test  (pre-commit gate)
```

## Single-writer rule

Only `sdk/tasks` — through `internal/vfs` — touches files under `.tasks/`. `cmd/`
and every consumer go through the `Store` API. **Only `internal/vfs` may import
`os`/`syscall`;** the pure core imports neither.

## Where changes go

| Change | Goes in |
|---|---|
| CLI command / flag | `cmd/` (wired in `root.go`); calls `Store`, never the FS |
| Stored field / store behaviour | `sdk/tasks` (`model`/`frontmatter`/`validate`/`store`) |
| Filter-expression language | `sdk/tasks/internal/query` (pure; no `os`, no `tasks` import) |
| Any disk operation | `sdk/tasks/internal/vfs` (the seam) — never inline `os` elsewhere |
| Pure logic (`ids`, `ready`, `resolve`) | its own file in `sdk/tasks`, no FS import → unit-tests at L1 |

## How to test

- Pure logic → **L1** (no FS). Store orchestration & error paths → **L2** on
  `vfs.Mem` (with fault injection). Durability, `flock`, round-trip → **L3** real
  temp dir. CLI → **L4**.
- Build fixtures with `internal/storetest`; never hand-roll a real `.tasks/`.
  Deterministic time via `Store.now`. Details in TESTING-STRATEGY.md.

## Keep specs in sync

A change to a CLI command/flag or a public `sdk/tasks` function/type/semantics
**must update the matching spec in the same change** ([CLI](specs/CLI-SPEC.md),
[SDK](specs/SDK-SPEC.md), [STORAGE](specs/TASK-STORAGE-SPEC.md),
[QUERY](specs/QUERY-SPEC.md)). A structural change (packages, the `vfs` seam)
updates [ARCHITECTURE](specs/ARCHITECTURE-SPEC.md) §5. A mismatch is a bug.

## Modules

Root (CLI) → SDK via `replace … => ./sdk`; `sdk/` is minimal-dep (only `yaml.v3`);
`bench/` is standalone, outside build/test. Run `mise run tidy` after changing imports.
