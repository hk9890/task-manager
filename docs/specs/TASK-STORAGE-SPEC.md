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

The store is a single directory holding **exactly one project's** issues directly;
there is no enclosing workspace and no project subdirectories *within* a store. By
default it is a `.tasks/` directory committed alongside the project it tracks — the
common case, **one tracker per repository**. A store may instead live **detached**
from its project, under a per-user central root, and be located by a registry that
maps the project path to it; that arrangement (the global config, the central root,
and the resolution rule) is specified in [CONFIG-SPEC.md](CONFIG-SPEC.md). The
on-disk format below is identical either way — a central store is byte-for-byte an
ordinary store — which is what makes relocating one a plain folder move.

```
.tasks/                          # store root (one project, committed with the repo)
├── config.yaml                  # project config (carries the ID prefix)
├── .lock                        # advisory write lock (not an issue)
├── proj-3k9f2x.md               # an active task file (filename == canonical ID)
├── proj-8mq04b.md
├── comments/                    # comment sidecars for ALL issues, open or closed
│   ├── proj-3k9f2x.yml          #   (cold; never parsed by the hot scan)
│   ├── proj-8mq04b.yml
│   └── proj-w1e7hd.yml          #   a closed issue keeps its sidecar here
├── content/                     # body sidecars for issues whose body overflowed
│   └── proj-8mq04b              #   (§4.6; no extension; never read by the hot scan)
└── closed/                      # closed task files (cold, immutable)
    └── proj-w1e7hd.md
```

Rules:

- The store holds **exactly one project**. The ID prefix comes from `config.yaml`,
  not from the directory name (which is `.tasks` for a local store, or the store's
  subfolder name under a central root — see [CONFIG-SPEC.md](CONFIG-SPEC.md)).
- The **hot set** is the store's top level: the `*.md` task files of non-closed
  issues. `closed/`, `comments/` and `content/` are subdirectories and are
  excluded from the hot scan by construction (the scan ignores subdirectories).
- The hot scan reads each `*.md` **in full**, so the cost of "list the active
  work" is the total size of those files, not merely their count. §4.6 is what
  bounds it: no file in the hot directory exceeds ~64 KiB, whatever a caller
  pastes into an issue.
- A reader resolving a single issue or a reference may descend into `closed/`; the
  bulk "list active work" path never does.

---

## 3. Identifiers

- **Format:** `<prefix>-<token>`, e.g. `proj-3k9f2x`. `token` is a random base36
  string (`[0-9a-z]`), 6 characters by default. Legacy sequential IDs
  (`<prefix>-0042`, zero-padded decimal) created under the previous scheme
  remain valid and resolvable.
- **Full pattern:** `^[a-z][a-z0-9]*-[0-9a-z]+$`, max length 64.
- **Prefix:** matches `^[a-z][a-z0-9]*$`, max length 32; declared in
  `config.yaml`. Every issue ID in the store shares it. The bound keeps every
  allocated ID (`prefix` + `-` + a 6-char token) within the 64-char maximum.
- **Canonical and stable.** An ID never changes. It appears in three places that
  must agree: the filename (`proj-3k9f2x.md`), the frontmatter `id` field, and any
  references to it from other issues.
- **Allocation:** a random base36 token. Existing IDs are scanned across **all
  partitions of the store** — the hot directory **and** `closed/` (and any future
  cold partition) — and used to reject duplicates, regenerating on the
  (astronomically unlikely) event of a collision. There is no counter file (it
  would be the worst git merge hotspot) and no high-water scan. A caller may also
  supply an explicit ID (import/migration) provided it matches this grammar,
  carries the prefix, and is not already in use.
- **Collisions:** the random token removes the parallel-branch merge-collision
  class by construction. Two branches each allocate independently from a
  ~2×10⁹ keyspace, so they effectively never pick the same ID on merge — no
  renumber/doctor tooling is required.

Hierarchy is expressed by the `parent` **field**, not by the ID. IDs are flat;
an epic is just an issue that others name as `parent`.

---

## 4. File types

Six file types exist. Each section gives the path, purpose, format, schema, and a
concrete example.

### 4.1 Store root

| | |
|---|---|
| **Path** | `.tasks/` |
| **Role** | Marker directory that anchors store discovery and holds the project. |
| **Contents** | `config.yaml`, `.lock`, the active `*.md` task files, and the `comments/`, `content/` and `closed/` subdirectories. |

### 4.2 Project config — `config.yaml`

| | |
|---|---|
| **Path** | `.tasks/config.yaml` |
| **Role** | Store configuration. Exactly one. |
| **Format** | A single YAML document. |

```yaml
prefix: proj

hook_timeout: 2s          # optional; max runtime for any single hook (HOOK-SPEC §3.1)
hooks:                    # optional; lifecycle-gate hooks run at issue transitions
  - id: tests-before-close
    event: pre-close
    run: ["make", "test"]
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `prefix` | string | yes | `^[a-z][a-z0-9]*$`, max length 32. The ID prefix for every issue in the store. |
| `hook_timeout` | duration | no | Global per-hook wall-clock limit (Go duration; default `2s`, `0` disables). See [HOOK-SPEC.md](HOOK-SPEC.md) §3.1. |
| `hooks` | list | no | Lifecycle-gate hooks run at issue transitions; full schema in [HOOK-SPEC.md](HOOK-SPEC.md) §3. |

Unknown keys must be ignored by readers, never rejected. The `hooks` block and
`hook_timeout` are validated when the store is opened for a write ([HOOK-SPEC.md](HOOK-SPEC.md) §3.4).

### 4.3 Task file — `<id>.md`

| | |
|---|---|
| **Path** | `.tasks/<id>.md` (active) or `.tasks/closed/<id>.md` (closed) |
| **Role** | The unit of storage: exactly one issue per file. |
| **Format** | A `---`-fenced YAML frontmatter block, then the markdown body. |

```markdown
---
id: proj-3k9f2x
title: Fix drill navigation
status: in_progress
type: bug
priority: 1
assignee: hans
creator: hans
labels: [area:details, triage:fix-as-is]
parent: proj-8mq04b
blocked_by: [proj-w1e7hd]
related: [proj-p52otu]
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
| `creator` | string | no | non-empty |
| `labels` | [string] | no | non-empty |
| `parent` | string | no | non-empty |
| `blocked_by` | [string] | no | non-empty |
| `related` | [string] | no | non-empty |
| `created` | timestamp | yes | always |
| `updated` | timestamp | yes | always |
| `closed` | timestamp | no | status is `closed` |
| `close_reason` | string | no | status is `closed` and a reason was given |
| `body_external` | bool | no | the body lives in the content sidecar (§4.6) |

**Field constraints** (rejected before any byte is written):

| Field | Constraint |
|---|---|
| `id` | `^[a-z][a-z0-9]*-[0-9a-z]+$`, max 64; equals the filename stem. |
| `title` | 1–200 chars after trim; single line (no `LF`); no control characters. |
| `status` | exactly one of `open`, `in_progress`, `blocked`, `deferred`, `closed`. |
| `type` | exactly one of `task`, `bug`, `feature`, `epic`, `chore`, `doc`. |
| `priority` | integer `0`–`4` (0 = critical … 4 = trivial); default `2`. |
| `assignee` | 0–128 chars; single line; no control characters. |
| `creator` | 0–128 chars; single line; no control characters. Set at creation; not editable afterward. |
| `labels` | 0–64 items; each 1–64 chars matching `^[a-z0-9][a-z0-9:._/-]*$`; unique. |
| `parent` | a valid ID (§3); must reference an existing issue; not self. |
| `blocked_by` | 0–256 items; each a valid ID; unique; no self; no cycles. |
| `related` | 0–256 items; each a valid ID; unique; no self. |
| `created` / `updated` / `closed` | `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$` (§6). |
| `close_reason` | 0–4096 chars; may be multi-line (block scalar). |
| `body_external` | bool; omitted when false. Written by the store only (§4.6); never set by a caller. |
| body (description) | Unbounded, but **at most 65536 bytes are stored in this file** — beyond that it moves to the content sidecar (§4.6). Verbatim markdown; trimmed of leading/trailing blank lines only. |

- **Body.** Everything after the closing `---` is the issue's free-form markdown
  description, stored **verbatim**. Because it is the body and not a YAML scalar,
  it is immune to YAML escaping — code fences, tables, and trailing whitespace
  survive unchanged. A body over the inline cap is not in this file at all; see
  §4.6.
- **`type: doc`.** A document — a design page, a session note, a handover, a
  review — is an ordinary issue whose type is `doc`. It shares the ID namespace,
  the commands, and the lifecycle of every other issue. It differs in exactly one
  way: it is not work, so it never appears as ready or blocked (§9). Its
  `status`, `priority` and `assignee` are therefore carried but carry no meaning.
  Which *kind* of document it is belongs in labels (`kind:design`,
  `kind:session`, `kind:handover`, `kind:review`), not in the type enum, so a new
  kind costs no schema change.
- **No `comments` in frontmatter.** Comments live in the sidecar (§4.4). This keeps
  task files small so the hot scan stays cheap.

### 4.4 Comment sidecar — `comments/<id>.yml`

| | |
|---|---|
| **Path** | `.tasks/comments/<id>.yml` |
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
   never rewritten. The sidecar is the **one** file extended in place: it is grown
   with an `O_APPEND` + `fsync` write under the store lock (§7), not the
   temp-file + `rename` used for every other file (the single exception to §6).
   Appending does not touch the task file.
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
   allowed (an edit of an edit, or a delete of an edit). If two documents `replaces`
   the **same** id (e.g. concurrent edits merged from two branches), the one
   appearing later in the stream wins — position gives sequence (rule 2).
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
| **Path** | `.tasks/.lock` |
| **Role** | Advisory exclusive write lock for the store (§7). Not an issue; never parsed. |
| **Format** | Empty; only its `flock` state matters. |

### 4.6 Content sidecar — `content/<id>`

| | |
|---|---|
| **Path** | `.tasks/content/<id>` (no extension) |
| **Role** | Holds an issue's body when it is too large to keep in the `.md`. Created lazily on the first write that overflows. |
| **Format** | The body verbatim, exactly as it would have appeared after the closing `---`. Not YAML, not framed, no header. |

An issue body over **65536 bytes** (`MaxInlineBody`) is written here instead of
into the task file, and the task file records `body_external: true` with an empty
body. This applies to **every issue type**, not only `doc`.

> **Why.** The hot scan reads whole files (§2). Without a ceiling, one pasted log
> or one generated HTML page makes every `list`, `ready` and `search` in the store
> pay for it forever. Overflow makes "no hot file exceeds ~64 KiB" an invariant of
> the format rather than a matter of discipline.

**Rules:**

1. **The flag is authoritative, not the file's existence.** A reader consults
   `body_external`: when false the body is the `.md`'s own, and any file at
   `content/<id>` is ignored entirely; when true the body is the sidecar's. This
   is load-bearing — see rule 4.
2. **Hysteresis.** A body splits out when it exceeds 65536 bytes, and returns
   inline only once it drops below **32768** bytes; between the two watermarks
   the current layout is kept. Without the gap, an issue hovering at the cap
   would change layout on every edit, and each change moves the whole body
   between two files — a maximal diff in both directions.
3. **The path is derived, never stored.** It is always `content/` + the issue ID,
   with no extension. Nothing user-supplied reaches a filesystem path, so
   traversal is impossible by construction rather than by validation. The
   trade-off is accepted: the format does not record whether the bytes are HTML,
   markdown or a log, and a reader that needs to know must be told by convention
   (e.g. a `format:html` label) or sniff.
4. **Write order is the crash contract.** A write touches two files and is *not*
   a single transaction. The `.md` is therefore written **last on a split** (after
   the sidecar) and **first on a join** (before the sidecar is removed). Either
   way a crash between the two leaves the `.md` and the body it names in
   agreement, and a sidecar nothing points at is inert garbage — never a silent
   override that resurrects an older body. Orphans are tolerated; nothing
   collects them.
   - Residual, accepted: on a split where the body was *already* external, a
     crash between the two writes can leave the new body beside the previous
     frontmatter. Nothing is silently reverted; the alternative is a journal.
5. **Sidecars do not move.** Closing an issue moves only its `.md` into
   `closed/`; the content sidecar stays at `content/<id>`, exactly as the comment
   sidecar does (§4.4 rule 6). No sidecar path ever changes.
6. **A closed issue's content sidecar is immutable**, like its `.md` (§5). The
   comment sidecar remains the one append-after-close exception.
7. **Comments never overflow.** A comment body is capped at 65536 bytes and
   rejected above it (§10). One sidecar holds an issue's whole comment stream, so
   an unbounded comment would be re-parsed on every read of every comment on that
   issue, and an overflowing one would need a per-comment file whose lifetime had
   to track edits and tombstones. Content that large belongs in a `doc` issue,
   `related` to this one.

---

## 5. Lifecycle, partitions, and immutability

An issue is **active** while its file lives in the store's hot directory and
**closed** once it lives in `closed/`.

- **Open / in_progress / blocked** issues are active; their files are mutable and
  sit in the hot directory.
- **Closing** an issue: set `status: closed`, set the `closed` timestamp (and
  `close_reason` if given), bump `updated`, then **move** the task `.md` into
  `closed/`. Both sidecars stay where they are — the comment sidecar in
  `comments/` and the content sidecar in `content/` — because only the task file
  moves. The move is a git rename — history is preserved.
- **Closed files are immutable in place.** While a file resides in `closed/`, no
  in-place write to it is allowed. This covers its content sidecar (§4.6 rule 6)
  as well as the `.md`; the one exception is its append-only comment sidecar
  (§4.4.6). Reopening is **not** an in-place edit: it moves the file out of
  `closed/` first, then edits it in the hot directory (see below).
- **Reopening** moves the file back to the hot directory, clears `closed` /
  `close_reason`, sets `status: open`, bumps `updated`, and re-enables writes.
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
  target, followed by an `fsync` of the parent directory so the new directory
  entry (the rename, or a newly created file) is itself durable across a crash —
  a reader never observes a torn file, and a survived rename never reverts. The
  **one** exception is the comment sidecar (§4.4), which is append-only — grown
  with an `O_APPEND` + `fsync` write rather than rewritten (a newly created
  sidecar still triggers a parent-dir `fsync`). Both forms happen under the store
  lock (§7).
- **Atomic per file, not across files.** Every individual write above is atomic.
  A body overflow (§4.6) writes *two* files — the `.md` and the content sidecar —
  and that pair is deliberately not a transaction. §4.6 rule 4 fixes the order so
  that every crash point leaves a consistent, readable issue.

---

## 7. Concurrency & durability

- **Single writer.** Every mutation serializes against all others — goroutines via
  an in-process mutex, processes via an exclusive advisory `flock` on `.tasks/.lock`;
  writes never interleave. The lock covers the whole store, including comment-sidecar
  appends.
- **Reads are lock-free** (atomic renames make this safe). A scan spanning the hot
  directory and `closed/` reads two directories, so it is not a single atomic
  snapshot; readers dedup by ID so a concurrent close/reopen can never yield a
  duplicate (a transient omission, if any, clears on the next read).
- **Throughput ceiling.** The write path `fsync`s inside the lock, so write
  throughput is bounded by `fsync` latency. In-lock work is kept minimal; the hot
  scan is the main cost, which §2/§5 keep O(open).

---

## 8. Relationships & derived edges

Stored on the dependent issue, one direction only:

- `parent` — grouping/epic membership.
- `blocked_by` — hard dependencies that gate readiness.
- `related` — soft, non-blocking references. **Symmetric**: stored on one issue,
  but the inverse is derived on read, so the link surfaces from both. Editable any
  time via `rel add`/`rel rm` (`rel rm` clears both stored sides).

Derived by scanning, never stored:

- **children** — issues whose `parent` is this issue.
- **blocks** — issues that list this issue in their `blocked_by`.
- **related (inverse)** — issues that list this issue in their `related`; merged
  with the forward edges into one symmetric related set.

There is no same-type constraint: any issue may parent or block any other.

---

## 9. Ready / blocked semantics

- **Ready** = `status: open` issues **of a work type** whose every `blocked_by`
  is closed. A blocker that exists in `closed/` counts as resolved; a reference
  that exists in neither the hot directory nor `closed/` is a dangling reference
  and is a validation error, not silently treated as satisfied. Ordering:
  priority (most urgent first), then oldest `created`, then ID.
- **Blocked** = non-closed issues **of a work type** with at least one open
  blocker.
- **Work type** = every type except `doc`. A document is not something to be
  done, so it is excluded from both sets by construction; otherwise every open
  design note would sit permanently at the head of the ready queue. The exclusion
  is a property of the engine, not of a caller's filter, so the `ready` /
  `blocked` query predicates (QUERY-SPEC §3) give the same answer as the
  corresponding read methods. Documents are excluded from these two views only —
  they remain fully visible to `list`, `search`, and every filter expression.
- **Cycles** in `blocked_by` are rejected at write time (DFS back-edge detection).

Ready/blocked are **derived from the dependency graph, not from the `status`
field**. The `blocked` *status value* is a manual label: the engine never sets or
clears it in response to dependencies, and it is not kept in sync with the
computed `blocked` predicate above. An issue may hold `status: blocked` with no
open blocker, or be blocked (by dependency) while its status is `open` or
`in_progress`.

---

## 10. Validation

A writer rejects, before anything touches disk:

- any field violating its §4 constraint (length, pattern, enum, range);
- empty `title`; a closed issue without a `closed` timestamp;
- self-parent, self-block, duplicate IDs in `blocked_by` / `related`;
- references (`parent`, `blocked_by`, `related`) to IDs that exist in neither the
  hot directory nor `closed/`;
- dependency cycles;
- an issue ID whose prefix does not match the store's configured `prefix`;
- a comment body that would serialize as a double-quoted (escaped) scalar;
- a comment body over 65536 bytes — comments do not overflow (§4.6 rule 7);
- a comment with neither a `body` nor `deleted: true`;
- a comment `replaces` that does not name an existing earlier comment in the same
  issue.

An issue body is **not** rejected for being large: it overflows to the content
sidecar instead (§4.6).
