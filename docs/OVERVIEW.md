# Overview

## Specifications

The authoritative contract lives in `docs/specs/`:

- [ARCHITECTURE-SPEC.md](specs/ARCHITECTURE-SPEC.md) — high-level structure: layers, modules, the write path, invariants.
- [TASK-STORAGE-SPEC.md](specs/TASK-STORAGE-SPEC.md) — the on-disk format: directory layout and every file type.
- [CONFIG-SPEC.md](specs/CONFIG-SPEC.md) — the per-user global config, the central store root and registry, and the store-resolution algorithm.
- [CLI-SPEC.md](specs/CLI-SPEC.md) — the `taskmgr` command surface, options, and JSON output.
- [QUERY-SPEC.md](specs/QUERY-SPEC.md) — the filter-expression language for selecting issues.
- [SDK-SPEC.md](specs/SDK-SPEC.md) — the `sdk/tasks` public Go API.
- [HOOK-SPEC.md](specs/HOOK-SPEC.md) — lifecycle-gate hooks: the per-repository extension
  system for policy and reactions (pre-hooks gate a transition, post-hooks notify).

## Repository layout

```
github.com/hk9890/task-manager   root module — the taskmgr CLI (cobra)
├── cmd/                        command groups + output rendering
│   └── taskmgr/                package main — the binary entrypoint
├── sdk/tasks/                  separate module — the storage engine + public SDK
└── bench/                      separate module — scaling harness (out of build/test)
```

`main` lives in `cmd/taskmgr/` rather than at the root so that
`go install github.com/hk9890/task-manager/cmd/taskmgr@latest` produces a binary
named `taskmgr`.

`sdk` is its own module so consumers can import
`github.com/hk9890/task-manager/sdk/tasks` without the CLI's dependencies. The root
`go.mod` **has no `replace`** — it requires the published `sdk vX.Y.Z`, which is what
downstream consumers and release builds resolve. Local builds and tests use the
in-tree copy instead because of the committed `go.work` (`use . ./sdk ./bench`),
which consumers ignore entirely. The only `replace` in the repository is in
`bench/go.mod`.

`bench/` is a standalone module kept out of `go build ./...` and `make test`, but it
is a workspace member and is compiled and vetted by `mise run quality`.
