# Architecture Specification

A high-level description of how agent-tasks is structured: the layers, the Go
modules, the storage engine at the core, and the invariants that hold the design
together. Detail lives in the companion specs:
[storage](TASK-STORAGE-SPEC.md), [CLI](CLI-SPEC.md), [SDK](SDK-SPEC.md).

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
        │  atctl (CLI) │      │  future consumers    │     front ends (thin)
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
never reads or writes files directly; it calls SDK functions. The CLI (`atctl`) is
the first and currently only consumer; the same boundary would let a future
consumer (e.g. a viewer or an HTTP server) sit on the engine without duplicating
storage or validation logic. Those are illustrations, not planned work — see §9.

- **Engine (`sdk/tasks`)** — the only code that reads or writes issue files. It
  enforces the on-disk format, validates input, computes ready/blocked and derived
  edges, and serializes concurrent writers.
- **CLI (`atctl`)** — a command-line front end for agents and humans; a thin
  wrapper that parses flags and calls the engine. See the CLI spec.
- **Other consumers** — any program (e.g. a viewer) imports the engine directly and
  works against the same files; there is no subprocess or JSON wire protocol
  between a consumer and the engine.

---

## 4. Modules

Three Go modules:

```
github.com/hk9890/agent-tasks            root module — the atctl CLI (cobra)
├── main.go                              wires command execution
├── cmd/                                 command groups + output rendering
├── sdk/                                 separate module — the engine
│   └── tasks/                           package tasks: storage engine + public API
└── bench/                               separate module — scaling harness
```

- **`sdk` is its own module** so consumers import
  `github.com/hk9890/agent-tasks/sdk/tasks` without inheriting the CLI's
  dependencies. The root module wires the local copy with a `replace` directive.
- **`bench`** is a standalone module (also `replace`d onto the engine) holding a
  scaling harness. It is intentionally excluded from `go build ./...` and the test
  suite.

---

## 5. The engine

`sdk/tasks` is organized by responsibility:

| File | Responsibility |
|---|---|
| `model.go` | `Issue`, `Comment`, `Ref`, `Detail`; status/type enums; priority bounds. |
| `frontmatter.go` | File ⇄ `Issue` (de)serialization (`Marshal` / `Unmarshal`). |
| `store.go` | Discovery, locking, atomic writes, CRUD, ID allocation. |
| `comments.go` | Comment sidecar: append, `replaces`/tombstone resolution to the effective log. |
| `query.go` | Filter-expression parsing + evaluation (QUERY-SPEC.md). |
| `ready.go` | Ready/blocked, cycle detection, listing (sort/limit), detail resolution. |
| `validate.go` | Single-issue field invariants. |
| `lock_unix.go` / `lock_other.go` | Advisory `flock` (unix) and a fallback. |

---

## 6. Write path

Every mutation follows the same path, which is where the "one writer" guarantee is
enforced:

1. **Acquire the store lock** (`flock`); concurrent writers serialize here.
2. **Apply** the change to an in-memory `Issue`.
3. **Validate** field invariants and referential integrity (referenced IDs exist;
   no cycles).
4. **Write atomically**: temp file + `fsync` + `rename` over the target (the
   append-only comment sidecar is the one exception — `O_APPEND` + `fsync`).
5. **Release the lock.**

Reads take a fresh snapshot of the directory and never hold the lock.

---

## 7. Core invariants

- **One writer.** All file access funnels through the engine under an exclusive
  store-wide lock. This is the precondition for validation and atomicity.
- **Derived inverse edges.** Only `parent`, `blocked_by`, and `related` are stored,
  on the dependent issue. Children and "blocks" are always computed by scanning, so
  the on-disk graph cannot contradict itself.
- **No counter file.** IDs are allocated by scanning for the maximum and adding
  one, avoiding a shared mutable file that would be a git merge hotspot.
- **Hot/cold separation.** Active issues and closed history are physically
  separated so the common path stays proportional to open work, not total history.

---

## 8. Consumers
- **`atctl`** — the agent/human CLI and the first (currently only) consumer to be
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
- coordination gates, swarms, configurable status/type catalogs;
- **multi-project workspaces** — a store tracks exactly one project, committed with
  its repo; there is no enclosing workspace or `--project` selection;
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
