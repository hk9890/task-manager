# Overview

## Specifications

The authoritative contract lives in `docs/specs/`:

- [ARCHITECTURE-SPEC.md](specs/ARCHITECTURE-SPEC.md) — high-level structure: layers, modules, the write path, invariants.
- [TASK-STORAGE-SPEC.md](specs/TASK-STORAGE-SPEC.md) — the on-disk format: directory layout and every file type.
- [CLI-SPEC.md](specs/CLI-SPEC.md) — the `taskmgr` command surface, options, and JSON output.
- [QUERY-SPEC.md](specs/QUERY-SPEC.md) — the filter-expression language for selecting issues.
- [SDK-SPEC.md](specs/SDK-SPEC.md) — the `sdk/tasks` public Go API.

## Repository layout

```
github.com/hk9890/task-manager   root module — the taskmgr CLI (cobra)
├── main.go, cmd/               command groups + output rendering
├── sdk/tasks/                  separate module — the storage engine + public SDK
└── bench/                      separate module — scaling harness (out of build/test)
```

`sdk` is its own module so consumers can import
`github.com/hk9890/task-manager/sdk/tasks` without the CLI's dependencies; the root
module wires the local copy with a `replace … => ./sdk` directive. `bench/` is a
standalone module kept out of `go build ./...` and `make test`.
