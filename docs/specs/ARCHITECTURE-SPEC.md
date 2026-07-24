# Architecture Specification

A high-level description of how task-manager is structured: the layers, the Go
modules, the storage engine at the core, and the invariants that hold the design
together. Detail lives in the companion specs:
[storage](TASK-STORAGE-SPEC.md), [config & resolution](CONFIG-SPEC.md),
[CLI](CLI-SPEC.md), [SDK](SDK-SPEC.md), and the [hooks](HOOK-SPEC.md) extension
system.

---

## 1. What it is

A lean, file-based task tracker. Issues, dependencies, and ready-work computation
live as plain files in a directory tree under version control — no database, no
daemon, no sync engine. It deliberately implements only the small set of tracker
features that are actually used, and nothing more.

---

## 2. Design goals

1. **File-based.** A task is a file; the store is a directory tree. Sharing is
   whatever the surrounding version control already does.
2. **Small and legible.** Only issues and the relationships between them. Every
   artifact is human-readable text that diffs cleanly.
3. **One writer.** A single engine owns all file access. Centralizing writes is
   what makes it possible to validate every change and guarantee that nothing
   malformed or half-written reaches disk.
4. **Minimal dependencies.** The engine pulls in almost nothing, so it can be
   embedded by other programs without weight.

---

## 3. Layered structure

The system is one storage engine with thin front ends over it. Nothing reaches
the files except through the engine.

```
        ┌──────────────┐      ┌─────────────────────┐
        │  taskmgr (CLI) │      │  future consumers    │     front ends (thin)
        └──────┬───────┘      └──────────┬──────────┘
               │     import / invoke      │
               └────────────┬─────────────┘
                            ▼
                 ┌────────────────────┐
                 │  sdk/tasks (engine)│   validation · locking · atomic writes
                 └─────────┬──────────┘
                           ▼
                 ┌────────────────────┐
                 │   .tasks/ on disk  │   files under version control
                 └────────────────────┘
```
**Every front end goes through the engine — and only the engine.** A front end
never reads or writes files directly; it calls SDK functions. The CLI (`taskmgr`) is
the first and currently only consumer; the same boundary would let a future
consumer (e.g. a viewer or an HTTP server) sit on the engine without duplicating
storage or validation logic. Those are illustrations, not planned work — see §9.

- **Engine (`sdk/tasks`)** — the only code that reads or writes issue files. It
  enforces the on-disk format, validates input, computes ready/blocked and derived
  edges, and serializes concurrent writers.
- **CLI (`taskmgr`)** — a command-line front end for agents and humans; a thin
  wrapper that parses flags and calls the engine. See the CLI spec.
- **Other consumers** — any program (e.g. a viewer) imports the engine directly and
  works against the same files; there is no subprocess or JSON wire protocol
  between a consumer and the engine.

---

## 4. Modules

Three Go modules:

```
github.com/hk9890/task-manager            root module — the taskmgr CLI (cobra)
├── cmd/                                 command groups + output rendering
│   └── taskmgr/                         package main — wires command execution
├── sdk/                                 separate module — the engine
│   └── tasks/                           package tasks: storage engine + public API
└── bench/                               separate module — scaling harness
```

- **`sdk` is its own module** so consumers import
  `github.com/hk9890/task-manager/sdk/tasks` without inheriting the CLI's
  dependencies.
- **Module wiring.** The root `go.mod` carries **no `replace`**: it requires the
  published `github.com/hk9890/task-manager/sdk vX.Y.Z`, so downstream consumers and
  release builds resolve the SDK from its `sdk/vX.Y.Z` tag. The committed `go.work`
  (`use . ./sdk ./bench`) is what makes *local* builds and tests use the in-tree
  copy; `go.work` is ignored by `go install <module>@<version>` and by consumers.
  Releases therefore require the version pin to be bumped, not a directive removed.
- **`bench`** is a standalone module — the one place a `replace` is used
  (`bench/go.mod`) — holding a scaling harness. It is excluded from
  `go build ./...` and the default test suite, but as a workspace member it is
  compiled and vetted by the `build:all`/`vet:all` tasks in the quality gate.
- **`main` is at `cmd/taskmgr/`**, not the module root, so
  `go install github.com/hk9890/task-manager/cmd/taskmgr@latest` yields a binary
  named `taskmgr`.

---

## 5. The engine

`sdk/tasks` is divided into a **pure core** (no filesystem access) and an
**imperative shell** that bridges the core to the `internal/vfs` disk seam.

### Package layout

| Package | Kind | Responsibility |
|---|---|---|
| `tasks` (facade) | imperative shell | Public API for consumers: `Store` CRUD, `Marshal`/`Unmarshal`, locking. Composes pure core with the vfs seam. |
| `tasks/internal/query` | pure | Filter-expression language (QUERY-SPEC). Compiles a query to a `Predicate` over a `Row` interface; no disk, no `tasks` import. |
| `tasks/internal/vfs` | disk seam | One of three packages that call `os`/`syscall`. `FS` interface + `osFS` (real: `WriteAtomic`, `Append`, `flock`) + `Mem` (in-memory, for tests). |
| `tasks/internal/exec` | process seam | Another `os`/`syscall` package: runs hook processes (HOOK-SPEC). `Runner` interface + OS runner (`os/exec`, SIGTERM→SIGKILL timeout) + `Fake` (scripted, for tests). |
| `tasks/internal/env` | environment seam | The third `os`/`syscall` package: reads the user environment for store resolution (CONFIG-SPEC) — `UserHomeDir`, `Getenv`. `Environment` interface + OS impl + `Fake` (for hermetic resolution tests, no real `HOME`). |
| `tasks/internal/storetest` | test support | Fixture builder: constructs a populated store into `vfs.Mem` (L2) or a real `t.TempDir()` (L3) from a declarative spec. |

### Imperative-shell files (may import `internal/vfs` / `internal/env`)

The shell is an **exhaustive, closed set of four files**. Every other non-test file
in `sdk/tasks` is pure core by definition — that is the rule
`TestImportBoundary_PureCoreNoVfs` enforces, so a new file is pure core unless it is
added to the guard's `imperativeShell` map in the same change.

| File | Responsibility |
|---|---|
| `store.go` | Discovery, CRUD, ID allocation; routes every file op through `internal/vfs`. Calls `newIDFromNames` with the directory listing it reads via the seam. |
| `comments.go` | Comment sidecar: append, `replaces`/tombstone resolution to the effective log. |
| `config.go` / `registry.go` | Load/persist the global config and the central registry (CONFIG-SPEC §2–§3); gather the resolution inputs (home/env via `internal/env`, walk-up + symlink canonicalization via `internal/vfs`) and feed them to `resolve.go`; central store creation. |

### Pure-core files (no `internal/vfs`)

The complement of the table above. None of these import `os` or `internal/vfs`;
`hookrun.go` and `log.go` are the two permitted to reach the `internal/exec` seam,
which is what lets a pure-core file spawn a hook without touching disk.

| File | Responsibility |
|---|---|
| `model.go` | `Issue`, `Comment`, `Ref`, `Detail`; status/type enums; priority bounds. |
| `ids.go` | `idStem` + `newIDFromNames`: collision-resistant base36 ID allocation over a name list. |
| `frontmatter.go` | File ⇄ `Issue` (de)serialization (`Marshal` / `Unmarshal`). |
| `validate.go` | Single-issue field invariants. |
| `ready.go` | Ready/blocked, cycle detection, listing (sort/limit), detail resolution. |
| `resolve.go` | Canonical path matching and store-resolution precedence (CONFIG-SPEC §4): lexical canonicalization, ancestor/longest-prefix match, local-then-central decision; no FS. |
| `mutation.go` | `MutationResult`; the gated-write sequence shared by every mutation — validate + index (§6 step 3), run pre-hooks around the caller's write (step 4), collect hints/warnings after post-hooks (step 7). |
| `transition.go` | Classifies an old/new `Issue` pair into a `transition` and derives its `pre-`/`post-` event names; issue cloning and update-insensitive equality. |
| `import.go` | The `Import` primitive: a validated direct write of a complete externally-sourced issue end-state (caller supplies status and timestamps, unlike `Create`). |
| `query.go` / `criteria.go` / `search.go` | The query surface: `compileExpr` plus the `*Issue`→`query.Row` adapter, the structured `Criteria` builder, and `SearchExpr` free-text→expression — all three produce or evaluate QUERY-SPEC expressions. |
| `hooks.go` | Hook config types (`Hook`) and their validation (HOOK-SPEC §3). |
| `hookpayload.go` | Builds the JSON payload handed to a hook process (HOOK-SPEC §5). |
| `hookrun.go` | Runs hooks for a transition via the `internal/exec` seam; applies the timeout and interprets the gate verdict (§6 steps 4 and 7). |
| `log.go` | `WithLogger` and the `slog` plumbing; the no-op default. |
| `doc.go` | Package documentation. |

### Seams and os/syscall confinement

`internal/vfs` (disk), `internal/exec` (hook processes), and `internal/env`
(user environment) are the **only** three locations for `os`/`syscall` calls. Every
other package — pure core and imperative shell alike — reaches the filesystem via
the `vfs.FS` interface, spawns hook processes via the `exec.Runner` interface, and
reads the environment via the `env.Environment` interface, so all three can be
swapped for in-memory/scripted/fake implementations in tests. Store resolution
(CONFIG-SPEC) therefore reads `HOME` and the environment **only** through the `env`
seam — keeping it hermetically testable with no real `HOME` touched — which is why
the engine, not a front end, can own resolution without sacrificing test isolation.
This confinement is enforced at two levels:

- **Code rule** (`CODING.md`): never import `os`/`syscall` outside `internal/vfs`,
  `internal/exec`, and `internal/env`.
- **Guard test** (`importboundary_test.go`): `TestImportBoundary_OnlyVfsImportsOS`
  fails the build if any non-test file outside those three seams imports `os` or
  `syscall`; `TestImportBoundary_PureCoreNoVfs` fails if a pure-core file gains an
  `internal/vfs` import.

---

## 6. Write path

Every mutation follows the same path, which is where the "one writer" guarantee is
enforced:

1. **Acquire the store lock** — an in-process mutex, then an exclusive `flock` on
   `.tasks/.lock`; concurrent writers serialize here, whether goroutines in one
   process or separate processes.
2. **Apply** the change to an in-memory `Issue`.
3. **Validate** field invariants and referential integrity (referenced IDs exist;
   no cycles).
4. **Run pre-hooks** for the transition ([HOOK-SPEC.md](HOOK-SPEC.md) §4). These are
   gates: the first hook that denies or errors aborts the mutation — the lock is
   released and nothing is written.
5. **Write atomically**: temp file + `fsync` + `rename` over the target (the
   append-only comment sidecar is the one exception — `O_APPEND` + `fsync`).
6. **Release the lock.**
7. **Run post-hooks** outside the lock ([HOOK-SPEC.md](HOOK-SPEC.md) §4): non-vetoing
   notifications that cannot change the committed outcome.

Reads take a fresh snapshot of the directory and never hold the lock.

---

## 7. Core invariants

- **One writer.** All file access funnels through the engine; every mutation
  serializes against all others — goroutines via an in-process mutex, processes via
  an exclusive store-wide `flock`. This is the precondition for validation and atomicity.
- **Derived inverse edges.** Only `parent`, `blocked_by`, and `related` are stored,
  on the dependent issue. Children, "blocks", and the inverse of `related` (which is
  symmetric) are always computed by scanning, so the on-disk graph cannot contradict
  itself.
- **No counter file.** IDs are random base36 tokens checked against existing IDs,
  avoiding both a shared mutable counter (a git merge hotspot) and the
  parallel-branch ID collisions that sequential numbering caused.
- **Hot/cold separation.** Active issues and closed history are physically
  separated so the common path stays proportional to open work, not total history.
  The partition axis is **open-vs-closed only** — deferred or long-parked issues
  stay in the hot set; there is deliberately no `parked/` partition (decision
  at-39dru2). The hot scan is O(open) at ~13µs/file — measured by the `bench`
  harness, REDESIGN A/B/C (see [bench/README.md](../../bench/README.md)) — so it
  degrades gracefully
  even at a few thousand hot issues; a status-based split would add routing
  complexity for no correctness benefit.

---

## 8. Consumers
- **`taskmgr`** — the agent/human CLI and the first (currently only) consumer to be
  built. Stateless; each invocation opens the store, performs one operation, and
  exits.
- **Future consumers (illustrative, none planned).** Any Go program can import the
  engine and work against the same files — for example a graphical viewer, or an
  HTTP server exposing task operations to non-Go clients. If one is built it imports
  the SDK like any other consumer (no subprocess, no wire protocol) and gets its own
  spec — e.g. a REST spec — at that time.

---

## 9. Non-goals

Deliberately out of scope, because they are unused weight:

- memories / notes-as-knowledge, "prime"-style context dumps;
- external tracker integrations (Jira, Linear, GitHub);
- a database or SQL backend; a sync engine or federation;
- swarms, configurable status/type catalogs, and **policy baked into the core** — rules
  like "tests must pass before close" are not engine features; they live in the hook
  extension system ([HOOK-SPEC.md](HOOK-SPEC.md)), which runs user scripts at
  transitions and keeps the core free of policy;
- **multi-project workspaces** — a store still tracks exactly **one** project; there
  is no enclosing workspace that aggregates several projects, and no `--project`
  selection. A store may live outside its repo under a per-user central root and be
  found by path ([CONFIG-SPEC.md](CONFIG-SPEC.md)), but that resolves to a single
  store — it is not a workspace;
- a **REST / HTTP API** or other remote front end — a future possibility, not built;
  if added it would import the SDK and get its own spec.

A small **filter-expression language** for selecting issues (QUERY-SPEC.md) is in
scope; a general SQL/query engine backed by a database is not.

The store is plain files under existing version control; anything an external
system would provide is left to that system.

---

## 10. Dependencies & philosophy

The engine depends on essentially nothing beyond YAML encoding; the CLI adds a
command framework. The guiding principle is subtractive: prefer the smallest design
that does the job, keep every artifact human-readable, and centralize writes so
correctness is enforced in exactly one place.

A feature in the **core must earn its place**: if a behaviour can be expressed as a hook
([HOOK-SPEC.md](HOOK-SPEC.md)) rather than engine code, it is a hook. The core carries only
issues, dependencies, and ready-work; per-repository policy and reactions live in the
extension system. That split is what lets the engine stay small while still supporting
complex, project-specific workflows.
