# Overview

A file-based task tracker: every issue is one Markdown file (YAML frontmatter +
markdown body) under a per-project `.tasks/` directory. It deliberately does only
what we use [beads] for — issues, dependencies, and ready-work — with no database,
daemon, or sync. See [SPEC.md](../SPEC.md) for the authoritative data model,
ready/blocked semantics, locking, and validation rules.

## Modules and the engine

```
github.com/hk9890/agent-tasks            root module — the atctl CLI (cobra)
├── main.go                              wires cmd.Execute()
├── cmd/                                 package cmd: command groups + rendering
├── sdk/                                 separate module — minimal deps
│   └── tasks/                           package tasks: storage engine + public SDK
└── bench/                               separate module — scaling harness (out of build/test)
```

The product is two Go modules: the CLI root and `sdk`. `sdk` is its own module so
consumers (e.g. [beads-workbench]) can import
`github.com/hk9890/agent-tasks/sdk/tasks` without pulling in the CLI's
dependencies (cobra et al.). The root module wires the local copy with a
`replace ... => ./sdk` directive.

`bench/` is a third, standalone module (`replace`d onto `../sdk`) holding a
`.tasks` scaling harness. It is deliberately kept out of `go build ./...` and
`make test`; see [bench/README.md](../bench/README.md).

## The engine (`sdk/tasks`)

| File | Responsibility |
|------|----------------|
| `model.go` | issue type, enums (status/type/priority), `Statuses`/`Types` |
| `frontmatter.go` | file ⇄ `Issue` (de)serialization |
| `store.go` | discovery, locking, atomic writes, CRUD, ID allocation |
| `ready.go` | ready/blocked computation, cycle detection, list/filter, detail |
| `validate.go` | single-issue field invariants |
| `lock_unix.go` / `lock_other.go` | advisory `flock` (unix) / no-op fallback |

## Key concepts

- **One writer.** `sdk/tasks` is the only code that reads or writes issue files;
  `atctl` and the viewer are thin layers over it. Centralizing access is what
  lets the store validate everything and serialize concurrent writers.
- **Derived inverse edges.** Only `parent`, `blocked_by`, and `related` are
  stored, on the dependent issue. Children and "blocks" are always derived by
  scanning, never written — so the on-disk graph cannot contradict itself.
- **No counter file.** IDs are `<prefix>-<NNNN>` with `NNNN = max(existing) + 1`,
  found by scanning the directory, to avoid a git merge-conflict hotspot.

[beads]: https://github.com/steveyegge/beads
[beads-workbench]: https://github.com/hk9890/beads-workbench
