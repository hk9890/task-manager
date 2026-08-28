# Testing Strategy

Four layers, split by *what they touch*. The seam that makes the split possible is
`sdk/tasks/internal/vfs.FS` (see [PACKAGE-OVERVIEW.md](PACKAGE-OVERVIEW.md)): pure
logic needs no disk, the shell is tested on an in-memory FS, and a real temp dir is
the source of truth for durability.

**This file owns nothing.** The command surface, the gates, and which one makes a
change green are normative in [docs/TESTING.md](../TESTING.md); the conventions a
test follows are in [docs/CODING.md](../CODING.md). This is the one-screen map of
how the layers sit on the tree.

## Layers

| Layer | Touches | Covers | Run |
|---|---|---|---|
| **L1 pure unit** | nothing (no `os`, no `vfs`) | query lex/parse/eval (fake `Row`s), frontmatter byte round-trips, validate tables, ready/blocked graph, comment `resolve` | `mise run test` |
| **L2 store on Mem** | `vfs.Mem` | `Store` CRUD orchestration, `nextID` across partitions, close/reopen, **fault injection** (forced rename/append failure → no torn state) | `mise run test` |
| **L3 integration** | real `t.TempDir()` (`osFS`) | real `fsync`/`flock`/`rename`; full lifecycle; **reload via a fresh `Open()` and re-assert** | `mise run test:integration` |
| **L4 CLI** | temp store + cobra | command → JSON DTO golden | see below |

L1/L2 are the default build (fast, no tag); L3 is gated behind the `integration`
build tag.

**L4 runs at both speeds.** `cmd.Run(args, stdout, stderr) int` executes one
invocation in-process, so anything whose subject is what the command *printed* —
output shapes, the misuse-help block, the generated catalog — is an ordinary
untagged test (`run_test.go`, `usage_render_test.go`,
`commands_catalog_test.go`). Only end-to-end behaviour that needs a real process
builds and forks the binary, behind the `integration` tag (`*_cli_test.go`, via
the harness in `cmd/harness_cli_test.go`). Reach for the subprocess form when the
process itself is the subject: exit codes as the shell sees them, stdin, signals,
or two processes contending the store lock.

`Run` drives package-level state — one command tree, reset per call. Sequential
invocations are independent; concurrent ones are not, so never `t.Parallel` a
test that calls it.

## Boundaries

- `vfs.Mem` proves **logic and error handling**, not durability — `fsync`/`flock`
  are no-ops in memory. **L3 is the only layer that proves atomic writes and
  cross-process locking.** Don't assert durability against the mock.
- Run L1/L2 on every change; run L3/L4 before handoff.

## What Mem can and cannot prove

`vfs.Mem` matches `osFS` on the following contract:

- **Parent-dir-must-exist**: `WriteAtomic`, `Append`, and `Rename` all require
  the parent directory to be present in the dirs set (registered via `MkdirAll`).
  Calls that skip `MkdirAll` will fail on both `Mem` and real disk — the two
  backends agree. A test that passes on `Mem` therefore also passes on disk for
  this invariant.
- **Rename is file-only**: `Mem.Rename` supports moving a single file between
  two existing directories. Renaming a directory is unsupported (returns an
  error); in production the only `Rename` calls move a single task `.md` file,
  so this is not a limitation.

`vfs.Mem` **cannot** prove:

- **Crash durability**: `fsync` is a no-op in memory. A "crash" after `WriteAtomic`
  but before the parent-dir `fsync` — the rename-then-parent-dir-fsync sequence in
  `sdk/tasks/internal/vfs/os.go` — cannot be modelled in `Mem`. L3 (real
  `t.TempDir()`) is the only layer that proves the full crash-safe rename sequence.
- **Atomic-append tearing**: `Mem.Append` is a single map update — it cannot
  model a partial write at the boundary of an OS append. L3 is required to prove
  `O_APPEND` durability on the comment sidecar.
- **Cross-process locking**: `Mem.Lock` is an in-process mutex. `flock` behaviour
  (advisory, per-fd, cross-process) is proven only by
  `cmd/lock_cli_test.go`, which runs four real `taskmgr` processes at the same
  issue. In-process goroutine tests cannot stand in for it — `flock` is
  per-process, so they hold no lock against each other in the first place.

  What `Mem.Lock` *does* model is the acquisition **failure** path, via
  `FailOn("Lock", …)`: a mutation whose lock cannot be taken must return the
  error and write nothing.

## Fixtures — one builder, two backends

`sdk/tasks/internal/storetest` builds a populated store from a spec, materialized
into `vfs.Mem` (L2, instant) or a real `t.TempDir()` (L3). Same fixture, both layers:

```go
st := storetest.New(t).
    Issue("agt-3k9f2x", storetest.Open, storetest.Parent("agt-8mq04b")).
    Closed("agt-8mq04b").
    Comment("agt-3k9f2x", "hans", "first note")
store := st.Mem()        // L2: in-memory, instant
store := st.TempDir(t)   // L3: materialized on real osFS
```

`storetest` is internal, so only `sdk`-module tests can import it; tests in
`cmd/` use the public `Store` API, `cmd.Run` for in-process assertions, or the
built binary for the subprocess cases.

## Which test package a file belongs in

A `_test.go` file in `sdk/tasks` is either **black-box** (`package tasks_test`,
sees only the exported API) or **white-box** (`package tasks`, sees unexported
identifiers too). The rule:

> **Default to black-box.** Write `package tasks` only when the test must reach
> an unexported identifier — `s.cfg`, `s.now`, `s.fs`, path helpers like
> `s.closedFilePath`, or a pure function such as `findCycle`.

Black-box is the default because it tests what a consumer can actually call, and
because a test that only compiles against the exported surface is proof that the
surface is sufficient.

Each package has **one** store fixture, and they are not interchangeable:

| Test package | Fixture | Why |
|---|---|---|
| `tasks_test` (black-box) | `storetest.New(t).Mem()` / `.TempDir(t)` | `storetest` imports `tasks` |
| `tasks` (white-box) | `newMemStore(t)` in `memstore_test.go` | importing `storetest` back would be a cycle |

`storetest` has a **third** form, for the states no builder can express:
`NewRawFixture(t, root)` creates a `.tasks/` skeleton and writes raw bytes
straight into it — `WriteIssue("closed/tst-0099.md", …)`, `WriteSidecar(…)` — so a
test can produce input the store would never write itself: malformed frontmatter,
a truncated document, a hand-seeded `closed/` entry. It is how "never hand-roll a
real `.tasks/`" stays followable when the point of the test is a broken tree. Like
the builder, it reaches disk through the `vfs` seam.

`newMemStore` returns the store **and** its `vfs.Mem`, so fault injection needs no
second helper: `s, m := newMemStore(t)`, then `m.FailOn(…)`. Do not hand-roll
another one — six near-identical copies accumulated before this rule was written.

The one duplication the split does force is `unwrap`, which exists in both
packages (`unwrap_white_test.go`, `unwrap_black_test.go`) because its signature
names types that are spelled differently on each side. That pair is deliberate;
nothing else should be.

## Conventions

- Tests sit next to the code (`*_test.go`). Never hand-roll a real `.tasks/` —
  `storetest.NewRawFixture` is the way to write raw bytes into one.
- A test that touches a real disk is L3 wherever its subject lives: it goes in an
  `_l3_test.go` file behind `//go:build integration`, so the default run stays
  disk-free.
- Deterministic time only; never assert the wall clock. `Store.now` inside package
  `tasks`, `tasks.WithClock` at construction from `cmd/` and external consumers.
  There is no post-construction setter: a test that must move time owns the state
  its `WithClock` closure reads.
- Assert sentinels with `errors.Is`; field failures are `*ValidationError` (`Field`);
  query parse failures are `*ParseError` (`Pos`).
- **TDD:** with the harness in place, write the layer-appropriate failing test
  first, then implement.

## Commands

`mise run test` (L1+L2) · `mise run test:integration` (L3+L4) · `mise run test:all`.
The gates (`quality`, `quality:full`) and the `make` fallback are documented in
[docs/TESTING.md](../TESTING.md).
