# Task Storage Specification

This document specifies the **on-disk storage format**: the directory layout, the
format and schema of every file type, and the invariants every file must satisfy.

---

## 1. Principles

1. **A task is a file.** The store is a directory tree under version control. No
   database, no daemon, no index files to keep in sync.
2. **Human-readable, diff-friendly.** Every file is UTF-8 text a person can read
   and a reviewer can diff line by line. Formats are chosen so that ordinary
   content (pasted terminal output, code fences) never degrades into escaped
   one-liners.
3. **Self-describing files.** A task file carries its own canonical ID and all of
   its outgoing relationships. Moving or copying a file never makes it ambiguous.
4. **One writer.** All writes go through a single owner that validates and
   serializes them, so nothing malformed or half-written ever reaches disk.
5. **Derive, don't duplicate.** Only one direction of each relationship is stored.
   Inverse edges (children, "blocks") are computed by scanning, never written, so
   the on-disk graph cannot contradict itself.
6. **Hot set stays small.** Active work and closed history are physically
   separated so the common path (scan the open issues) costs O(open), not
   O(all-issues-ever).

---

## 2. Directory layout

The store root is a `.tasks/` directory. It is a **workspace** that holds one or
more **projects**, each in its own subdirectory named for its ID prefix.

```
.tasks/                          # workspace root
├── dtt/                         # a project; directory name == ID prefix
│   ├── config.yaml              # project config
│   ├── .lock                    # advisory write lock (not an issue)
│   ├── dtt-0001.md              # an active task file (filename == canonical ID)
│   ├── dtt-0002.md
│   ├── comments/                # comment sidecars for ALL issues, open or closed
│   │   ├── dtt-0001.yml         #   (cold; never parsed by the hot scan)
│   │   ├── dtt-0002.yml
│   │   └── dtt-0003.yml         #   a closed issue keeps its sidecar here
│   └── closed/                  # closed task files (cold, immutable)
│       └── dtt-0003.md
└── app/                         # a second, fully independent project
    ├── config.yaml
    └── app-0001.md
```

Rules:

- The **workspace** (`.tasks/`) contains only project directories. It has no files
  of its own.
- A **project directory** is the unit of isolation: its own config, its own lock,
  its own ID space, its own hot/cold scan. Cross-project work never contends on a
  shared lock.
- The directory name **is** the prefix. `config.yaml`'s `prefix` must equal it.
- The **hot set** is the project directory's top level: the `*.md` task files of
  non-closed issues. `closed/` and `comments/` are subdirectories and are excluded
  from the hot scan by construction (the scan ignores subdirectories).
- A reader resolving a single issue or a reference may descend into `closed/`; the
  bulk "list active work" path never does.

---

## 3. Identifiers

- **Format:** `<prefix>-<NNNN>`, e.g. `dtt-0042`. `NNNN` is decimal, zero-padded to
  at least four digits, and grows past four when needed (`dtt-12345`).
- **Full pattern:** `^[a-z][a-z0-9]*-[0-9]{4,}$`, max length 64.
- **Prefix:** matches `^[a-z][a-z0-9]*$`, max length 32, equals the project
  directory name.
- **Canonical and stable.** An ID never changes. It appears in three places that
  must agree: the filename (`dtt-0042.md`), the frontmatter `id` field, and any
  references to it from other issues.
- **Allocation:** `max(existing) + 1`, where `existing` is scanned across **all
  partitions of the project** — the hot directory **and** `closed/` (and any
  future cold partition). There is no counter file (it would be the worst git
  merge hotspot). Scanning closed issues for the high-water mark is mandatory:
  skipping it re-issues an ID already taken by a closed task.
- **Collisions:** two branches creating issues in parallel can pick the same
  number and collide on merge. This is accepted for the mostly-single-writer
  workflow; detection/renumber is a tooling concern, not a storage one.

Hierarchy is expressed by the `parent` **field**, not by the ID. IDs are flat;
an epic is just an issue that others name as `parent`.

---

## 4. File types

Five file types exist. Each section gives the path, purpose, format, schema, and a
concrete example.

### 4.1 Workspace

| | |
|---|---|
| **Path** | `.tasks/` |
| **Role** | Marker directory that anchors store discovery and groups projects. |
| **Contents** | Only project directories. No loose files. |

### 4.2 Project config — `config.yaml`

| | |
|---|---|
| **Path** | `.tasks/<prefix>/config.yaml` |
| **Role** | Per-project configuration. One per project. |
| **Format** | A single YAML document. |

```yaml
prefix: dtt
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `prefix` | string | yes | `^[a-z][a-z0-9]*$`, max 32, equals the directory name. |

Unknown keys must be ignored by readers, never rejected.

### 4.3 Task file — `<id>.md`

| | |
|---|---|
| **Path** | `.tasks/<prefix>/<id>.md` (active) or `.tasks/<prefix>/closed/<id>.md` (closed) |
| **Role** | The unit of storage: exactly one issue per file. |
| **Format** | A `---`-fenced YAML frontmatter block, then the markdown body. |

```markdown
---
id: dtt-0042
title: Fix drill navigation
status: in_progress
type: bug
priority: 1
assignee: hans
labels: [area:details, triage:fix-as-is]
parent: dtt-0007
blocked_by: [dtt-0040]
related: [dtt-0012]
created: 2026-06-01T10:00:00Z
updated: 2026-06-04T09:00:00Z
---

## Description
Drilling a related issue should navigate fully, not just update the rail.
```

**Frontmatter schema** (field order is normative — writers emit in this order):

| Field | Type | Required | Emitted when |
|---|---|---|---|
| `id` | string | yes | always |
| `title` | string | yes | always |
| `status` | enum | yes | always |
| `type` | enum | yes | always |
| `priority` | int | yes | always |
| `assignee` | string | no | non-empty |
| `labels` | [string] | no | non-empty |
| `parent` | string | no | non-empty |
| `blocked_by` | [string] | no | non-empty |
| `related` | [string] | no | non-empty |
| `created` | timestamp | yes | always |
| `updated` | timestamp | yes | always |
| `closed` | timestamp | no | status is `closed` |
| `close_reason` | string | no | status is `closed` and a reason was given |

**Field constraints** (rejected before any byte is written):

| Field | Constraint |
|---|---|
| `id` | `^[a-z][a-z0-9]*-[0-9]{4,}$`, max 64; equals the filename stem. |
| `title` | 1–200 chars after trim; single line (no `LF`); no control characters. |
| `status` | exactly one of `open`, `in_progress`, `blocked`, `closed`. |
| `type` | exactly one of `task`, `bug`, `feature`, `epic`, `chore`. |
| `priority` | integer `0`–`4` (0 = critical … 4 = trivial); default `2`. |
| `assignee` | 0–128 chars; single line; no control characters. |
| `labels` | 0–64 items; each 1–64 chars matching `^[a-z0-9][a-z0-9:._/-]*$`; unique. |
| `parent` | a valid ID (§3); must reference an existing issue; not self. |
| `blocked_by` | 0–256 items; each a valid ID; unique; no self; no cycles. |
| `related` | 0–256 items; each a valid ID; unique; no self. |
| `created` / `updated` / `closed` | `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$` (§6). |
| `close_reason` | 0–4096 chars; may be multi-line (block scalar). |
| body (description) | 0–65536 bytes; verbatim markdown; trimmed of leading/trailing blank lines only. |

- **Body.** Everything after the closing `---` is the issue's free-form markdown
  description, stored **verbatim**. Because it is the body and not a YAML scalar,
  it is immune to YAML escaping — code fences, tables, and trailing whitespace
  survive unchanged.
- **No `comments` in frontmatter.** Comments live in the sidecar (§4.4). This keeps
  task files small so the hot scan stays cheap.

### 4.4 Comment sidecar — `comments/<id>.yml`

| | |
|---|---|
| **Path** | `.tasks/<prefix>/comments/<id>.yml` |
| **Role** | The append-only comment log for one issue. Created lazily on first comment. |
| **Format** | A **multi-document YAML stream**: one document per comment, in chronological order, separated by `---`. |

```yaml
---
id: k7m2p9qx
author: hans
created: 2026-06-04T15:22:37Z
body: |
    ## Verified on live tenant
    Ran the content-only update; server preserved the name.

    ```
    $ dtctl update notebook 6a957de1 --content @body.json
    200 OK
    ```
---
id: b3n8t1wz
author: hans
created: 2026-06-05T09:00:00Z
body: short follow-up note
---
id: q5f4r0hd
author: hans
created: 2026-06-05T10:00:00Z
replaces: k7m2p9qx
body: |
    Correction: the server returned 200 but also bumped the version to 2.
---
id: w9c2k5te
author: hans
created: 2026-06-05T11:00:00Z
replaces: b3n8t1wz
deleted: true
```

**Per-comment document schema:**

| Field | Type | Required | Constraint |
|---|---|---|---|
| `id` | string | yes | A short random token, `^[0-9a-z]{8}$`, self-assigned on append **without reading the stream**. The ~36⁸ keyspace makes per-issue collisions negligible, so parallel branches never clash. Identifies the comment; ordering comes from position, not the id. |
| `author` | string | no | 0–128 chars; single line; no control characters. |
| `created` | timestamp | yes | `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$` (§6). |
| `replaces` | string | no | The `id` of an earlier comment (one appearing earlier in the stream) in the same issue that this document supersedes — used for both edits and deletes. |
| `deleted` | bool | no | When `true`, this document is a tombstone that retracts the comment named by `replaces`; `body` is then omitted. |
| `body` | string | cond. | Required unless `deleted: true`. 1–65536 bytes; markdown; sanitized (§ rules below); rendered as a block scalar. |

**Format rules — these are load-bearing:**

1. **Append-only.** Every change — a new comment, an edit, or a delete — is a new
   document appended to the stream (`---\n` + the document). Existing documents are
   never rewritten. Appending does not touch the task file.
2. **Identity & ordering.** A comment's `id` is an opaque, stable random handle; it
   carries no ordering. Documents are stored in chronological **append order**, so
   position — not the id — gives sequence.
3. **Editing and deleting are append-with-`replaces`.** Comments are never changed
   in place. To revise a comment, append a new document whose `replaces` names the
   target's `id`. To delete one, append a tombstone — `replaces` the target with
   `deleted: true` and no `body`. The original always stays in the log (full history
   is preserved); a reader resolves each `replaces` chain to its newest document and
   renders that as effective — the comment's current text, or nothing if the newest
   is a tombstone — treating superseded documents as history. `replaces` chains are
   allowed (an edit of an edit, or a delete of an edit).
4. **Body sanitization is mandatory.** Before serialization the writer must
   normalize each body: convert CRLF/CR to LF, and strip trailing whitespace from
   every line. This forces YAML to emit a readable block scalar (`body: |`)
   instead of a double-quoted, `\n`-escaped one-liner. A writer **must not** emit a
   body that round-trips as a double-quoted scalar.
   - Trade-off: sanitization drops trailing whitespace, so markdown "two trailing
     spaces = hard line break" is not preserved. This is acceptable for comments.
5. **A literal `---` inside a body is safe.** Block-scalar content is indented
   under `body:`, so a `---` line in a comment is body text, never a document
   separator. The decoder splits only on `---` at column 0.
6. **Sidecars survive close.** A closed issue's task file is frozen (§5), but its
   comment sidecar remains append-only — post-close verification notes are a real
   and supported pattern. The sidecar always lives in `comments/<id>.yml`
   regardless of the task's state; only the task `.md` moves on close, so no
   sidecar path ever changes.

### 4.5 Lock file — `.lock`

| | |
|---|---|
| **Path** | `.tasks/<prefix>/.lock` |
| **Role** | Advisory exclusive write lock for the project (§7). Not an issue; never parsed. |
| **Format** | Empty; only its `flock` state matters. |

---

## 5. Lifecycle, partitions, and immutability

An issue is **active** while its file lives in the project's hot directory and
**closed** once it lives in `closed/`.

- **Open / in_progress / blocked** issues are active; their files are mutable and
  sit in the hot directory.
- **Closing** an issue: set `status: closed`, set the `closed` timestamp (and
  `close_reason` if given), bump `updated`, then **move** the task `.md` into
  `closed/`. The comment sidecar stays in `comments/` (only the task file moves).
  The move is a git rename — history is preserved.
- **Closed files are immutable.** Writes to anything under `closed/` are rejected,
  with one exception: the comment sidecar remains append-only (§4.4.6).
- **Reopening** moves the file back to the hot directory, clears `closed` /
  `close_reason`, sets a non-closed status, and re-enables writes.
- Closed history may later be compacted/compressed; only the cold partition is
  ever a candidate, and only old entries, since compression forfeits per-file
  diffing and grep.

> **Why partition at all.** Closed issues dominate a mature store (often ~90%).
> Keeping them out of the hot directory makes "scan the active work" O(open)
> instead of O(all-issues-ever), and keeps it flat as history grows.

---

## 6. Encoding conventions

- **Charset:** UTF-8, LF line endings. A leading UTF-8 BOM is tolerated on read,
  never written.
- **Timestamps:** RFC 3339 / ISO 8601, **UTC**, truncated to whole seconds
  (`2026-06-04T09:00:00Z`). Whole seconds keep diffs minimal and values readable.
- **YAML emission:** two-space indent; fields in the schema order above; empty
  optional fields omitted (never written as `null` / `[]` / `""`). String scalars
  that span multiple lines use block scalars.
- **Atomic on disk:** every file write is temp-file + `fsync` + `rename` over the
  target, so a reader never observes a torn file.

---

## 7. Concurrency & durability

- **Single writer per project.** Every mutation runs under an exclusive advisory
  lock (`flock` on `.tasks/<prefix>/.lock`). Concurrent writers serialize; they
  never interleave writes. Locks are per project, so unrelated projects proceed in
  parallel.
- **Reads are lock-free.** Atomic renames make this safe.
- **Throughput ceiling.** The write path `fsync`s inside the lock, so write
  throughput is bounded by `fsync` latency. In-lock work is kept minimal; the hot
  scan is the main cost, which §2/§5 keep O(open).

---

## 8. Relationships & derived edges

Stored on the dependent issue, one direction only:

- `parent` — grouping/epic membership.
- `blocked_by` — hard dependencies that gate readiness.
- `related` — soft, non-blocking references.

Derived by scanning, never stored:

- **children** — issues whose `parent` is this issue.
- **blocks** — issues that list this issue in their `blocked_by`.

There is no same-type constraint: any issue may parent or block any other.

---

## 9. Ready / blocked semantics

- **Ready** = `status: open` issues whose every `blocked_by` is closed. A blocker
  that exists in `closed/` counts as resolved; a reference that exists in neither
  the hot directory nor `closed/` is a dangling reference and is a validation
  error, not silently treated as satisfied. Ordering: priority (most urgent
  first), then oldest `created`, then ID.
- **Blocked** = non-closed issues with at least one open blocker.
- **Cycles** in `blocked_by` are rejected at write time (DFS back-edge detection).

---

## 10. Validation

A writer rejects, before anything touches disk:

- any field violating its §4 constraint (length, pattern, enum, range);
- empty `title`; a closed issue without a `closed` timestamp;
- self-parent, self-block, duplicate IDs in `blocked_by` / `related`;
- references (`parent`, `blocked_by`, `related`) to IDs that exist in neither the
  hot directory nor `closed/`;
- dependency cycles;
- a `prefix` that does not match the project directory name;
- a comment body that would serialize as a double-quoted (escaped) scalar;
- a comment with neither a `body` nor `deleted: true`;
- a comment `replaces` that does not name an existing earlier comment in the same
  issue.
