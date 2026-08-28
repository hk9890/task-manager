# Overview

The map of this repository: where things live, the invariants a design has to respect,
and the expressions that find the rest.

## Repository layout

```
github.com/hk9890/task-manager     root module — the taskmgr CLI (cobra)
├── cmd/                          one file per command group, plus rendering
│   ├── root.go                   the command tree and the three persistent flags
│   ├── render.go                 human tables, detail blocks, and the JSON DTOs
│   ├── guide.go                  the how-to that ships inside the binary
│   ├── commands.go               the machine catalog, derived from the live tree
│   └── taskmgr/                  package main
├── sdk/tasks/                    separate module — the storage engine and the public API
│   ├── internal/vfs/             the disk seam: FS interface, osFS, and Mem for tests
│   ├── internal/exec/            the process seam: hook processes
│   ├── internal/env/             the environment seam: HOME and TASKMGR_* for resolution
│   ├── internal/query/           the filter-expression engine — pure, imports nothing of tasks
│   └── internal/storetest/       fixture builder; every test store is built through it
├── scripts/checkdocs/            the doc gate (DOCUMENTING.md)
└── docs/
    ├── specs/                    the normative contract — the table below
    └── implementation/           orientation maps that own nothing; the specs are normative
```

## Key concepts

The invariants a design has to respect.
[ARCHITECTURE-SPEC](specs/ARCHITECTURE-SPEC.md) states each in full; these are the shapes
to hold in mind while reading the code.

- **One writer.** Only `sdk/tasks` touches a store, under a `flock`, validating and writing
  atomically. Every other guarantee rests on it.
- **Pure core, imperative shell.** A file in `sdk/tasks` is pure core unless
  `sdk/tasks/importboundary_test.go` lists it — and pure core may declare no `*Store`
  method, not merely avoid an import.
- **Three seams.** `internal/vfs`, `internal/exec` and `internal/env` are the only packages
  that call `os`/`syscall`.
- **Derived edges are never stored.** `parent`, `blocked_by` and `related` are written;
  children, "blocks" and the inverse of `related` are computed, so the graph cannot
  contradict itself.
- **`ready` and `blocked` come from the graph, not the `status` field.** The `blocked`
  status is a manual label the engine never sets or clears.
- **Hot/cold partition.** Active issues at the store root, closed ones under `closed/`, so
  a list costs O(open); a body over `MaxInlineBody` moves to a sidecar to keep it that way.
- **Policy lives in hooks, not the core.** A behaviour that can be a hook is a hook.

## Specifications

The specs are **normative** — code that disagrees with one is a bug, and a behaviour
change updates its spec in the same commit.

| Spec | Owns |
|---|---|
| [ARCHITECTURE-SPEC](specs/ARCHITECTURE-SPEC.md) | The layers, the two modules, the pure-core/shell split, the write path, the invariants |
| [TASK-STORAGE-SPEC](specs/TASK-STORAGE-SPEC.md) | The on-disk format: directory layout, every file type, IDs, validation, the partitions |
| [CONFIG-SPEC](specs/CONFIG-SPEC.md) | The per-user home, the central store root and registry, and the store-resolution algorithm |
| [CLI-SPEC](specs/CLI-SPEC.md) | The `taskmgr` command surface — commands, options, exit codes, and the JSON DTOs |
| [SDK-SPEC](specs/SDK-SPEC.md) | The public `sdk/tasks` Go API |
| [QUERY-SPEC](specs/QUERY-SPEC.md) | The filter-expression language: grammar, fields, operators, evaluation scope |
| [HOOK-SPEC](specs/HOOK-SPEC.md) | Lifecycle-gate hooks: the eight events, the payload, the decision contract |

Two documents under [implementation/](implementation/) orient without owning anything:
[PACKAGE-OVERVIEW](implementation/PACKAGE-OVERVIEW.md) maps the packages,
[TESTING-STRATEGY](implementation/TESTING-STRATEGY.md) the four test layers.

## Finding things

```bash
rg -n 'Use:\s+"' cmd/                                    # every command the CLI serves
rg -n '^func \(s \*Store\)' sdk/tasks/                   # every operation the engine offers
rg -n '^\tErr\w+' sdk/tasks/store.go                     # the error sentinels callers match with errors.Is
rg -n 'kind: field' sdk/tasks/internal/query/parse.go    # every query field and the operators it allows
rg -n 'imperativeShell' -A30 sdk/tasks/importboundary_test.go   # which files may reach the disk seam
rg -n '^func validateFields' -A90 sdk/tasks/validate.go  # every field constraint enforced before a write
rg -n 'MaxInlineBody|joinInlineBody' sdk/tasks/overflow.go      # the body-overflow watermarks
rg -n '<field-name>' sdk/tasks/frontmatter.go            # how a stored field is read and written
```

Start from `sdk/tasks/store.go` for the engine's entry points and `cmd/root.go` for the
command tree. `sdk/tasks/doc.go` is the package's own orientation.

## External resources

| Resource | Where |
|---|---|
| Published SDK reference | https://pkg.go.dev/github.com/hk9890/task-manager/sdk/tasks |
| Git remote and releases | https://github.com/hk9890/task-manager |
| Command framework | [`spf13/cobra`](https://pkg.go.dev/github.com/spf13/cobra) (resolved version: `go.mod`) |
| YAML encoder — the SDK's only dependency | [`gopkg.in/yaml.v3`](https://pkg.go.dev/gopkg.in/yaml.v3) |
