# agent-tasks — Specification

A lean, file-based task tracker. It does what we actually use beads for — issues,
dependencies, ready-work computation — and nothing else. No database, no memories,
no sync engine, no integrations.

## Goals

1. **File-based.** A task is a file. The store is a directory tree under version
   control. No database, no daemon.
2. **Small.** Only issues and the relationships between them. Features that exist
   in beads but we never use (memories, `prime`, Jira, Dolt sync, federation,
   gates, swarms, SQL) are deliberately absent.
3. **One writer.** A Go package (`sdk/tasks`) owns all file access. The CLI
   (`atctl`, for agents) and the viewer (beads-workbench, via direct import) are
   thin layers over it. Centralizing writes is what lets us validate everything
   and guarantee nothing malformed reaches disk.

## Architecture

```
github.com/hk9890/agent-tasks          (root module — the atctl CLI, cobra)
├── main.go
├── cmd/                               package cmd: one file per command group
└── sdk/                              (separate module — minimal deps)
    └── tasks/                         package tasks: the engine + public SDK
        ├── model.go      types & enums
        ├── frontmatter.go  file <-> Issue (de)serialization
        ├── store.go      discovery, locking, atomic writes, CRUD, IDs
        ├── ready.go      ready/blocked, cycle detection, detail, list/filter
        ├── validate.go   field invariants
        └── lock_*.go     advisory file lock (flock on unix)
```

`sdk` is its own Go module so consumers (beads-workbench) can import
`github.com/hk9890/agent-tasks/sdk/tasks` without pulling the CLI's dependencies
(cobra et al.). The root module wires it with `replace ... => ./sdk`.

## On-disk format

Per project, a `.tasks/` directory:

```
.tasks/
├── config.yaml         # { prefix: "agt" }
├── agt-0001.md
├── agt-0002.md
└── .lock               # advisory write lock (not an issue file)
```

Each issue is one Markdown file: a YAML frontmatter block, then the free-form
markdown description as the body.

```markdown
---
id: agt-0042
title: Fix drill nav
status: in_progress
type: bug
priority: 1
assignee: hans
labels: [area:details]
parent: agt-0007
blocked_by: [agt-0040]
related: [agt-0012]
created: 2026-06-01T10:00:00Z
updated: 2026-06-04T09:00:00Z
comments:
  - author: hans
    created: 2026-06-02T11:00:00Z
    body: decided to follow the rail
---

## Description
Drilling a related issue should navigate fully.
```

- Timestamps are UTC, truncated to whole seconds (readable, minimal diffs).
- `closed` / `close_reason` appear only on closed issues.
- Comments live in frontmatter as an append-only list (the file is rewritten on
  each comment; the write lock + atomic rename keep this safe).

## Data model

| Field        | Notes                                                      |
|--------------|-----------------------------------------------------------|
| `id`         | `<prefix>-<NNNN>`, allocated by the store                  |
| `title`      | required, non-empty                                        |
| `status`     | `open` \| `in_progress` \| `blocked` \| `closed` (fixed)   |
| `type`       | `task` \| `bug` \| `feature` \| `epic` \| `chore` (fixed)  |
| `priority`   | integer 0 (critical) .. 4 (trivial); default 2            |
| `assignee`   | optional                                                   |
| `labels`     | free-form strings                                         |
| `parent`     | optional issue ID — grouping only (epics fall out of this)|
| `blocked_by` | issue IDs that must close before this is ready            |
| `related`    | non-blocking references                                   |
| `comments`   | append-only `{author, created, body}`                     |
| description  | the markdown body                                          |

### Relationships

Only **one direction** of each edge is stored, on the dependent issue: `parent`,
`blocked_by`, `related`. The inverse edges — **children** (who has this as
parent) and **blocks** (who is blocked by this) — are always *derived* by
scanning, never written. The on-disk graph therefore cannot contradict itself.

There is no "same-type" constraint (a beads quirk): any issue may block or
parent any other. An epic is simply an issue of type `epic` that others name as
`parent`.

## ID allocation

`<prefix>-<NNNN>` where NNNN is `max(existing) + 1`, found by scanning the
directory. There is **no counter file**, which removes the worst git
merge-conflict hotspot. Trade-off: two branches creating issues in parallel can
both pick the same number and collide on merge. This is acceptable for the
mostly-single-writer workflow; a future `atctl doctor`/renumber can detect and
fix collisions. (Open question — see below.)

## Ready / blocked

- **Ready** = issues with status `open` whose every `blocked_by` is closed (a
  missing blocker counts as resolved). Ordered by priority, then oldest first.
- **Blocked** = non-closed issues with at least one open blocker, reported with
  the blocking issues.
- **Cycles**: adding a dependency that would create a cycle is rejected at write
  time (DFS back-edge detection).

## Concurrency & durability

- All writes run under an exclusive advisory lock (`flock` on `.tasks/.lock`), so
  concurrent `atctl` invocations and the bwb process never interleave writes.
- Each write is a temp-file write + `fsync` + atomic `rename`, so readers never
  observe a torn file.
- Reads are lock-free.

## Validation

The store rejects, before anything touches disk:

- empty title; unknown status/type; priority out of range;
- self-parent, self-block, duplicate dependency IDs;
- references (`parent`, `blocked_by`, `related`) to non-existent issues;
- dependency cycles.

## Command surface (`atctl`)

```
atctl init [--prefix X]
atctl create --title T [--type --priority --description[-file] --assignee
                        --label... --parent --blocked-by... --related...] [--json]
atctl show <id> [--json]
atctl list [--status --type --priority-min/max --assignee --label...
            --all --ready --blocked --text --sort --reverse --limit] [--json]
atctl search <text> [same filters] [--json]
atctl ready [--limit] [--json]
atctl blocked [--json]
atctl update <id> [--title --description[-file] --status --type --priority
                   --assignee --parent --add-label --remove-label
                   --set-labels --clear-labels] [--json]
atctl close <id> [--reason] [--json]
atctl dep add|rm <dependent> <blocker>
atctl comment add <id> [body] [--author --file]
atctl labels | statuses | types [--json]
atctl version
```

Every command takes `--json` (machine output) and `-C/--dir` (where to find
`.tasks`, default cwd; the store is discovered by walking up).

## Intentionally excluded

Memories / `remember`, `prime`, Jira, Dolt push/pull, `doctor`/`lint`/`stale`/
`orphans`/`preflight`, gates, `link`, `edit`, federation, swarms, configurable
status/type catalogs, raw SQL/query DSL.

## beads-workbench integration

bwb imports `github.com/hk9890/agent-tasks/sdk/tasks` directly and implements its
existing `Repository` interface over the SDK — no subprocess, no JSON contract,
none of the bd-subprocess machinery (env allowlist, argv-pinning, NDJSON
handling, bd-version workarounds). The SDK's `Issue`/`Detail`/`Ref` map closely
onto bwb's `domain` types. During local development bwb uses a `replace`
directive to the local checkout.

## Open questions / decisions to confirm

- **ID collisions across branches** — accept + add a renumber tool, or switch to
  collision-resistant IDs (short random suffix)? Currently: sequential.
- **Comment storage** — frontmatter list (current) vs. a managed body section.
- **License** — none committed yet (private repo).
