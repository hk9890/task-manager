# Package Overview

A one-screen orientation map of the SDK module (`sdk/tasks` + its internal
packages) and the CLI module (`cmd`).

**This file owns nothing.** The package table, the pure-core/shell split, and the
seam rule are normative in [ARCHITECTURE-SPEC §5](../specs/ARCHITECTURE-SPEC.md).
Test layers: [TESTING-STRATEGY.md](TESTING-STRATEGY.md).

## Layout

```
sdk/tasks/                  package tasks — public facade + imperative shell
  model · frontmatter · validate · ids · ready · comments · store
  mutation · transition · import · config · registry · resolve
  query · criteria · search  (query surface)
  hooks · hookrun · hookpayload  (lifecycle gates)
  log · doc
  internal/query/           pure filter-expression engine: lex · parse · ast · eval · errors
  internal/vfs/             the disk seam: FS interface · osFS (prod) · Mem (test + faults)
  internal/exec/            the process seam: Runner interface · OS runner · Fake
  internal/env/             the environment seam: Environment interface · OS impl · Fake
  internal/storetest/       fixture builder ("make a store") — test-only support package
cmd/                        taskmgr CLI (cobra); calls Store, never the FS
  taskmgr/                  package main — the binary entrypoint
bench/                      separate module; excluded from go build ./... and make test
```

## What each package holds

- **`tasks`** — the only package consumers import. Public types
  (`Issue`/`Comment`/`Ref`/`Detail`), `Store` CRUD, `Marshal`/`Unmarshal`. The
  *imperative shell*: it composes the pure core with the `vfs` seam under the lock.
- **`internal/query`** — the QUERY-SPEC language. **Pure**: no disk, no `tasks`
  import. Compiles an expression to a `Predicate` over a `Row` interface and
  returns `*ParseError`. `tasks` adapts `*Issue`→`Row` and re-exports
  `ParseError` (`type ParseError = query.ParseError`).
- **`internal/vfs` · `internal/exec` · `internal/env`** — the three seams (disk,
  hook processes, user environment) and the only SDK packages that call
  `os`/`syscall`. Each has a real implementation and a test double.
- **`internal/storetest`** — builds a populated store from a declarative spec into
  *either* `vfs.Mem` or a real `t.TempDir()`. A normal (non-`_test.go`) package so
  any package's tests can import it; because only test files import it, it never
  ships in a binary.
- **`cmd`** — flag parsing + JSON DTO rendering. Goes through `Store`.

## Rules (load-bearing)

1. **Seam confinement** — within `sdk/tasks`, only `internal/vfs`, `internal/exec`,
   and `internal/env` import `os`/`syscall`. Enforced by
   `sdk/tasks/importboundary_test.go`.
2. **The pure core imports neither `os` nor `vfs`** — `query`, `frontmatter`,
   `validate`, `ids`, the `ready`/`blocked` graph, and comment `resolve` take
   in-memory inputs and return values/errors (so they unit-test at L1).
3. **`cmd` never touches the filesystem** — always via `Store`.
4. **`internal/query` must not import `tasks`** (import cycle); it evaluates over `Row`.
5. **Single writer:** every mutation runs through `Store` under the `vfs` lock;
   atomic writes only (the append-only comment sidecar is the lone exception).
