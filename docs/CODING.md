# Coding

Read [OVERVIEW.md](OVERVIEW.md) and the specs in `specs/` first.

## Build

```bash
make build      # -> ./bin/atctl    (make install onto $PATH)
make fmt vet    # both modules; Go 1.26
```

## Single-writer rule

`sdk/tasks` is the **only** package that may touch files under `.tasks/`. `cmd/`
and every consumer go through the `Store` API — reading or writing issue files
elsewhere bypasses validation, locking, and atomic writes.

## Modules

- Root (CLI) → SDK via `replace … => ./sdk`; `sdk/` is a separate, minimal-dep
  module (only `yaml.v3`); `bench/` is standalone, outside build/test.
- Run `make tidy` after changing imports.

## Where changes go

- **CLI command** → `cmd/` (wired in `root.go`); calls the `Store`, not the FS.
- **Stored field/behaviour** → `sdk/tasks` (`model`/`frontmatter`/`validate`/
  `store`), exposed via the JSON DTOs in `cmd/render.go`.

## Keep the specs in sync

A change to a CLI command/flag or a public `sdk/tasks` function/type/semantics
**must update the matching spec in the same change** ([CLI-SPEC](specs/CLI-SPEC.md),
[SDK-SPEC](specs/SDK-SPEC.md), [TASK-STORAGE-SPEC](specs/TASK-STORAGE-SPEC.md)). A
mismatch is a bug.

## Invariants

- Layered validation: self-contained in `validate.go`, referential in `store.go`.
- Never persist derived edges; atomic writes only (lock + temp + fsync + rename);
  timestamps UTC whole-seconds.
