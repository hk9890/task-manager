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

Two Go modules:

```
github.com/hk9890/task-manager            root module — the taskmgr CLI (cobra)
├── cmd/                                 command groups + output rendering
│   └── taskmgr/                         package main — wires command execution
├── scripts/                             repo tooling shipped in neither binary
│   └── checkdocs/                       package main — the doc gate, stdlib only
└── sdk/                                 separate module — the engine
    └── tasks/                           package tasks: storage engine + public API
```

- **`sdk` is its own module** so consumers import
  `github.com/hk9890/task-manager/sdk/tasks` without inheriting the CLI's
  dependencies.
- **Module wiring.** The root `go.mod` carries **no `replace`**: it requires the
  published `…/sdk vX.Y.Z`, which consumers and release builds resolve from the
  `sdk/vX.Y.Z` tag. The committed `go.work` (`use . ./sdk`) points *local* builds at
  the in-tree copy and is ignored by `go install <module>@<version>`. A release
  therefore bumps the pin; it never removes a directive. No module in this
  repository uses a `replace` directive.
- **`main` is at `cmd/taskmgr/`**, not the module root, so
  `go install …/cmd/taskmgr@latest` yields a binary named `taskmgr`.

---

## 5. The engine

`sdk/tasks` is divided into a **pure core** (no filesystem access) and an
**imperative shell** that bridges the core to the `internal/vfs` disk seam.

### Package layout

| Package | Kind | Responsibility |
|---|---|---|
| `tasks` (facade) | imperative shell | Public API for consumers: `Store` CRUD, `Marshal`/`Unmarshal`, locking. Composes pure core with the vfs seam. |
| `tasks/internal/query` | pure | Filter-expression language (QUERY-SPEC). Compiles a query to a `Predicate` over a `Row` interface; no disk, no `tasks` import. |
| `tasks/internal/vfs` | disk seam | One of three packages that call `os`/`syscall`. `FS` interface + `osFS` (real: `WriteAtomic`, `Append`, `flock`, `Remove`/`RemoveAll`, `MoveTree` incl. its cross-device copy fallback) + `Mem` (in-memory, for tests). |
| `tasks/internal/exec` | process seam | Another `os`/`syscall` package: runs hook processes (HOOK-SPEC). `Runner` interface + OS runner (`os/exec`, SIGTERM→SIGKILL timeout) + `Fake` (scripted, for tests). |
| `tasks/internal/env` | environment seam | The third `os`/`syscall` package: reads the user environment (CONFIG-SPEC) — `UserHomeDir`, `Getenv` — to locate the taskmgr home for store resolution and for the machine-wide hook packages a write inherits (HOOK-SPEC §3.5). `Environment` interface + OS impl + `Fake` (for hermetic tests, no real `HOME`). |
| `tasks/internal/storetest` | test support | Fixture builder: constructs a populated store into `vfs.Mem` (L2) or a real `t.TempDir()` (L3) from a declarative spec. |

### Imperative-shell files (may declare `*Store` methods)

A **closed set**; every other non-test file in `sdk/tasks` is pure core by
definition, so a new file is pure core unless it is added to the guard's
`mayDeclareStoreMethods` map. Adding one there to make a build pass is how the
boundary erodes — a file belongs on this list only when it genuinely cannot do
its job over plain values.

Importing `internal/vfs` / `internal/env` is a **second, narrower** exemption
(`mayImportVFS`: `store.go`, `comments.go`, `content.go`, `config.go`,
`registry.go`, `packageload.go`). The two lists are separate because the two rules
exempt different files: one list short-circuited both checks, so a file added for
its `*Store` methods silently stopped having its imports checked as well.

`packageload.go` is the case that shows the split working. It reads package
directories through the seam, so it needs the import exemption — but it does its
work over plain values (`collectUse`, `packageChain`, `inspectRef` all take the
`FS`, the environment and the directories as parameters), so it needs no `*Store`
method and is not on the first list. The three entry points that do have a
receiver — `Packages`, `HookChain`, `InspectPackage` — sit in `store.go`, which is
already on both.

| File | Responsibility |
|---|---|
| `store.go` | Discovery, CRUD, ID allocation; routes every file op through `internal/vfs`. Calls `newIDFromNames` with the directory listing it reads via the seam. |
| `comments.go` | Comment sidecar: append, `replaces`/tombstone resolution to the effective log. |
| `content.go` | Body-overflow sidecar I/O: the two-file write ordering, sidecar read/removal, `ResolveBody` (TASK-STORAGE-SPEC §4.6). The rule it applies is pure and lives in `overflow.go`. |
| `config.go` / `registry.go` | Load/persist the global config (`LoadGlobalConfig`/`SaveGlobalConfig`) and the central registry (CONFIG-SPEC §2–§3); gather the resolution inputs (home/env via `internal/env`, walk-up + symlink canonicalization via `internal/vfs`) and feed them to `resolve.go`; central store creation. |
| `list.go` | `Ready`/`Blocked`/`Detail`/`Query`/`List`/`ListPage`, and `Find`/`FindPage` over a `Criteria`: read the hot index, the `closed/` partition and comment sidecars through the seam, then apply the pure rules in `ready.go`. |
| `mutation.go` | `MutationResult` and the gated-write sequence every mutation shares — validate+index (§6 step 3), pre-hooks around the write (step 4), hints/warnings after post-hooks (step 7). |
| `import.go` | The `Import` primitive: a direct write of a complete externally-sourced end-state (caller supplies status and timestamps, unlike `Create`). |
| `hookrun.go` | Runs hooks for a transition via the `internal/exec` seam; applies the timeout and interprets the gate verdict (§6 steps 4 and 7). |
| `log.go` | `WithLogger` and the `slog` plumbing; the no-op default. |
| `packageload.go` | Reads a package directory through the vfs seam and merges the two `use:` lists into the chain a mutation runs (HOOK-SPEC §3.5). Declares no `*Store` method: everything is a function over the seams and the directories. |

### Pure-core files (no filesystem access)

The complement of the table above, and what `TestImportBoundary_PureCoreNoVfs`
enforces. A pure-core file may not import `os` or `internal/vfs`, **and may not
declare a method on `*Store`**: a method reaches the seam through the `s.fs`
field, which no import list reveals, and cannot be called without constructing a
store — which is the property that makes L1 testing impossible. Checking only the
imports is what let `ready.go` sit in this table while reading disk on every
call.

| File | Responsibility |
|---|---|
| `model.go` | `Issue`, `Comment`, `Ref`, `Detail`; status/type enums; priority bounds. |
| `ids.go` | `idStem` + `newIDFromNames`: collision-resistant base36 ID allocation over a name list. |
| `frontmatter.go` | File ⇄ `Issue` (de)serialization (`Marshal` / `Unmarshal`). |
| `overflow.go` | The body-overflow rule: the split/join watermarks (`layoutFor`) and rendering an issue into its `.md` and sidecar halves (`renderForWrite`). Decides from a byte count and a bool; the I/O is in `content.go`. |
| `validate.go` | Single-issue field invariants. |
| `ready.go` | The graph rules: open-blocker computation, cycle detection, sort and window. Plain functions over an issue index and an "is this closed?" predicate; the methods that supply both are in `list.go`. |
| `resolve.go` | Canonical path matching and store-resolution precedence (CONFIG-SPEC §4): lexical canonicalization, ancestor/longest-prefix match, local-then-central decision; no FS. |
| `transition.go` | Classifies an old/new `Issue` pair into a `transition` and derives its `pre-`/`post-` event names; issue cloning and equality. |
| `query.go` / `search.go` | The query surface: the `*Issue`→`query.Row` adapter and the `ParseError` alias, and `SearchExpr` free-text→expression. |
| `criteria.go` | The `Criteria` builder and `Build`, which compiles it to a filter expression. The two `*Store` methods that take one, `Find` and `FindPage`, are in `list.go`. |
| `hooks.go` | Hook types (`Hook`) and their compilation into the runnable chain (HOOK-SPEC §3). Pure: it takes a resolved chain and returns a validated one. |
| `packages.go` | The hook-package format (HOOK-SPEC §3.6) as pure core: manifest decoding, `use:` entry resolution, the `argv[0]` rule, and `checkUseChange`, which checks only what a write introduces. |
| `hookpayload.go` | Builds the JSON payload handed to a hook process (HOOK-SPEC §5). |
| `configdoc.go` | Renders a config change back into an existing `config.yaml`, leaving unknown keys and comments as the author wrote them. Maps bytes to bytes. |
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

**A store carries the seams its resolution used**, rather than reaching for the OS
ones once it is built. A write reads the per-user config through the `env` seam to
inherit machine-wide hook packages (HOOK-SPEC §3.5), so a store that swapped in `env.NewOS()`
after an injected resolution would read the developer's real home — which makes
that inheritance untestable anywhere else, and quietly undoes the isolation the
seam exists for.

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
   append-only comment sidecar is the one exception — `O_APPEND` + `fsync`). A
   body that overflows writes a second file, the content sidecar; each write is
   individually atomic but the pair is not a transaction, so the order is fixed
   to make every crash point readable
   ([TASK-STORAGE-SPEC.md](TASK-STORAGE-SPEC.md) §4.6).
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
- **Bounded hot files.** Every issue file the engine writes stays under ~64 KiB:
  a larger body moves to a content sidecar
  ([TASK-STORAGE-SPEC.md](TASK-STORAGE-SPEC.md) §4.6). Since the hot scan reads
  whole files, this makes its cost a function of how many issues are open, not of
  what anyone pasted into one. A file written *before* overflow existed keeps its
  inline body until its next mutation, which then splits it; the legacy
  inline-comment migration rewrites such a file faithfully rather than reshaping
  it, so nothing is silently rewritten on a read path.
- **Hot/cold separation.** Active issues and closed history are physically
  separated so the common path stays proportional to open work, not total history.
  The partition axis is **open-vs-closed only** — deferred or long-parked issues
  stay in the hot set; there is deliberately no `parked/` partition (decision
  at-39dru2). The hot scan is O(open) at ~13µs/file, so it degrades gracefully even
  at a few thousand hot issues; a status-based split would add routing complexity
  for no correctness benefit.

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

- a **knowledge base**: no retrieval layer, no indexing, no embedding, no
  "prime"-style context assembly. `type: doc` stores a document *as an issue* —
  it gets an ID, a lifecycle, `related` edges, and the same query language as
  everything else, and nothing more. The store holds documents; it does not
  interpret them, rank them, or feed them to anything;
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
