# Package Overview

The *implementation* layout of the SDK module (`sdk/tasks` + its internal
packages) and the CLI module (`cmd`). The behavioural contract lives in
[../specs/](../specs/); this file is the package map and the rules that hold it
together. Test layers: [TESTING-STRATEGY.md](TESTING-STRATEGY.md).

## Layout

```
sdk/tasks/                  package tasks — public facade + imperative shell
  model · frontmatter · validate · ids · ready · comments · store · query (adapter)
  internal/query/           pure filter-expression engine: lex · parse · ast · eval · errors
  internal/vfs/             the disk seam: FS interface · osFS (prod) · Mem (test + faults)
  internal/storetest/       fixture builder ("make a store") — test-only support package
cmd/                        atctl CLI (cobra); calls Store, never the FS
bench/                      standalone module; outside build/test
```

## What each package holds

- **`tasks`** — the only package consumers import. Public types
  (`Issue`/`Comment`/`Ref`/`Detail`), `Store` CRUD, `Marshal`/`Unmarshal`. The
  *imperative shell*: it composes the pure core with the `vfs` seam under the lock.
- **`internal/query`** — the QUERY-SPEC language. **Pure**: no disk, no `tasks`
  import. Compiles an expression to a `Predicate` over a `Row` interface and
  returns `*ParseError`. `tasks` adapts `*Issue`→`Row` and re-exports
  `ParseError` (`type ParseError = query.ParseError`).
- **`internal/vfs`** — the **only** package that calls `os`/`syscall`. The `FS`
  interface (`ReadDir`, `ReadFile`, `Stat`, `WriteAtomic`, `Append`, `Rename`,
  `MkdirAll`, `Remove`, `Lock`); `osFS` (real — encapsulates temp+fsync+rename and
  flock); `Mem` (in-memory + fault injection).
- **`internal/storetest`** — builds a populated store from a declarative spec into
  *either* `vfs.Mem` or a real `t.TempDir()`. A normal (non-`_test.go`) package so
  any package's tests can import it; because only test files import it, it never
  ships in a binary.
- **`cmd`** — flag parsing + JSON DTO rendering. Goes through `Store`.

## Rules (load-bearing)

1. **Only `internal/vfs` imports `os`/`syscall`.** Everything else uses the `FS` seam.
2. **The pure core imports neither `os` nor `vfs`** — `query`, `frontmatter`,
   `validate`, `ids`, the `ready`/`blocked` graph, and comment `resolve` take
   in-memory inputs and return values/errors (so they unit-test at L1).
3. **`cmd` never touches the filesystem** — always via `Store`.
4. **`internal/query` must not import `tasks`** (import cycle); it evaluates over `Row`.
5. **Single writer:** every mutation runs through `Store` under the `vfs` lock;
   atomic writes only (the append-only comment sidecar is the lone exception).
