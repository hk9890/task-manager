# CLI Specification — `taskmgr`

This document specifies the `taskmgr` command-line interface: every command, its
arguments and options, and what it does. `taskmgr` is the agent-facing front end to
a `.tasks` store (see [TASK-STORAGE-SPEC.md](TASK-STORAGE-SPEC.md) for the on-disk
format it operates on).

---

## 1. Invocation & global conventions

```
taskmgr <command> [subcommand] [args] [flags]
```

### Persistent flags (valid on every command)

| Flag | Default | Meaning |
|---|---|---|
| `--json` | off | Emit machine-readable JSON instead of the human table/detail view. |
| `-C, --dir <path>` | cwd | Start directory for locating the store; `.tasks` is found by walking up. |
| `--store-path <path>` | — | Override resolution: operate on the store at this explicit path (no walk-up, no registry). |
| `--store-name <name>` | — | Override resolution: operate on the central store with this registry name. Mutually exclusive with `--store-path`. |

### Environment variables

| Variable | Meaning |
|---|---|
| `TASKMGR_DIR` | Store-path override, equivalent to `--store-path`; the flag wins if both are set. |
| `TASKMGR_HOME` | The taskmgr home holding the global config and (by default) the central store root. Default `~/.taskmgr`. See [CONFIG-SPEC.md](CONFIG-SPEC.md) §1. |
| `TASKMGR_LOG` | Log level/destination for observability output (mapped to a logger and injected into the SDK). |

### Output modes

- **Human (default):** compact, aligned tables for lists; a labelled block for a
  single issue.
- **JSON (`--json`):** stable, `snake_case` shapes (§6). Pretty-printed, HTML
  escaping disabled. This is the contract for agents and tools.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Success. |
| `1` | Any error (not found, validation failure, no store, I/O). The message is printed to stderr, prefixed `taskmgr: `. |

### Errors & help on misuse

All errors go to stderr prefixed `taskmgr: ` and leave stdout empty (exit `1`), but
two classes are presented differently:

- **Runtime errors** (not found, validation failure, dependency cycle, no store, …)
  print the message alone — terse and self-explanatory. They are *not* wrapped in
  usage text.
- **Misuse** — wrong positional args, a missing required flag, or an unknown/bad
  flag — prints a compact **help block**: the error, the command's one-line purpose,
  its usage line and a synthesised example, its own flags (or, for a command group,
  its subcommands), and a `Run 'taskmgr <command> --help'` pointer.

Mistyped commands are corrected, not dead-ended: an unknown top-level command or an
unknown subcommand exits `1` with a `Did you mean this?` suggestion (a bare command
group with no subcommand prints its help and exits `0`).

### Store resolution

The store a command operates on is resolved by the engine (the same logic every
front end uses), in this order — full algorithm in [CONFIG-SPEC.md](CONFIG-SPEC.md)
§4:

1. an explicit **override** — `--store-path` / `TASKMGR_DIR`, or `--store-name`;
2. otherwise **local walk-up** from `--dir` (or cwd) for a `.tasks` directory (the
   long-standing behaviour — a local store always wins);
3. otherwise the **central registry** — if a central store is mapped to the current
   project path, use it.

Most commands fail with a "no store" error if none of these resolves; `init` and
`where` are the exceptions. The error is actionable rather than a dead end — `taskmgr:
no .tasks directory found — run 'taskmgr init' to create one`. `taskmgr where` (§2.1)
never errors on no-store — it reports the outcome (including "nothing resolved") and
exits `0`, since reporting resolution is its whole job.

Agents can self-orient without external docs: `taskmgr guide` (§5) prints a
workflow how-to, `taskmgr commands` (§5) prints the machine catalog, and every
command supports `--help`. The root help and `init` success output both point at
`taskmgr guide`.

---

## 2. Setup commands

### `taskmgr init`

Create a new store for the current project — locally by default, or centrally with
`--central`.

| Option | Default | Meaning |
|---|---|---|
| `--prefix <p>` | derived from directory name | ID prefix for the store (`^[a-z][a-z0-9]*$`). |
| `--central` | off | Create the store under the central root and register it (instead of a local `.tasks/`). See [CONFIG-SPEC.md](CONFIG-SPEC.md) §5. |
| `--store-name <n>` | project basename | With `--central`, the store's subfolder name under the central root. |

- **Local (default):** creates the `.tasks/` store directory and its `config.yaml`
  in the current project. Fails if a local store already exists.
- **Central (`--central`):** creates `<central_root>/<name>/` as an ordinary store and
  adds a registry entry mapping the current project path to it. Fails if that
  subfolder or a registry entry for this path already exists.
- If `--prefix` is omitted it is derived from the project directory name (lowercased,
  non-alphanumerics stripped, leading digits removed, truncated; falls back to `task`).
  This holds for both local and central stores — prefixes are per project, with no
  global default (CONFIG-SPEC §5).
- **Output:** the store path and chosen prefix (`{"dir","prefix"}` in JSON; with
  `--central`, also the registry `name`).

---

## 2.1 Store inspection

Two read-only commands surface the central setup. Registry *editing* verbs (`store
move` / `link` / `unlink`) are a deliberate, use-gated follow-up — until then the
registry is one short YAML file the user can hand-edit (CONFIG-SPEC §5).

### `taskmgr where`

Show which store the current working directory resolves to and **why** — the diagnostic
for the resolution rule above. It mirrors the engine's `ResolveKind` (SDK-SPEC §1)
verbatim, so the override distinction is not lost:

- `kind`: `local` | `central` | `override_path` | `override_name`, or `none` when
  nothing resolves.
- `store_path`: the resolved store directory (omitted when `kind` is `none`).
- `project_path`: the project the store tracks (the store's parent for a local store;
  omitted when `kind` is `none`).

Never errors on no-store; exits `0` with `kind: none`. **Output (JSON):** `whereDTO` (§6).

### `taskmgr store list`

Enumerate the registry entries — each entry's project `path`, `store` name, and the
store directory. A plain listing (no health classification in this slice; a dangling
entry, CONFIG-SPEC §3, is shown like any other).

- **Output (JSON):** array of `storeListDTO` (§6).

---

## 3. Read commands

### `taskmgr show <id>`

Show full detail for one issue: all fields, resolved relationships (parent,
blocked-by, related, plus derived **blocks** and **children**), the description
body, and comments (the **resolved** log — edits applied, deleted comments
removed; see storage spec §4.4).

- **Output (JSON):** `detailDTO` (§6).

### `taskmgr list [-q <expr>] [options]`

List issues selected by a **filter expression** (§3.1). Closed issues are excluded
unless the expression selects them or `--all` is given. Default order: priority
(most urgent first), then oldest.

| Flag | Meaning |
|---|---|
| `-q, --query <expr>` | Filter expression (§3.1). Omitted → all active issues. |
| `--all` | Include closed issues (reads the cold partition). |
| `--sort <field>` | `work` \| `id` \| `priority` \| `created` \| `updated` \| `closed`. Default `work` = priority, then oldest `created`; `priority` sorts by priority alone. Every sort breaks ties on `id` (deterministic order). |
| `--reverse` | Reverse the sort order. |
| `--limit <n>` | Cap the number of results (`0` = all). |

- **Output (JSON):** array of `issueDTO`.
- The CLI does not page: `--limit` is a simple cap and there is no `--offset`.
  Windowed paging with a total match count is an SDK concern (`ListPage` / `FindPage`,
  SDK-SPEC.md §4).

### 3.1 Filter expressions

`-q` takes a **filter expression** — `<field> <op> <value>` predicates combined with
`&&`, `||`, `!`, and parentheses (e.g. `status == "open" && priority <= 1`). The
grammar, the full field/operator table, value syntax, and error semantics are
defined once, at the engine layer, in **[QUERY-SPEC.md](QUERY-SPEC.md)**; the CLI
passes the string to the SDK unchanged. The `-q` flag help carries inline examples,
and `taskmgr guide` (§5) restates the grammar in brief, so an agent in a terminal —
without QUERY-SPEC.md in context — can still discover and use it.

```
status == "open"
status == "open" && priority <= 1
type == bug && label ~ "area:db"
ready && priority <= 2
text ~ "drill" && !blocked
closed > "2026-01-01"
```

Scope: closed issues are excluded unless `--all` is passed or the expression
satisfies the cold-scope predicate (a `status == "closed"` atom or a `closed`
comparison; `status != "closed"` does not). See QUERY-SPEC.md §5.

### `taskmgr search <text> [options]`

Shorthand for matching `<text>` against the ID, title, or description —
equivalent to `list -q 'text ~ "<text>"'`. Accepts `--all`, `--sort`, `--reverse`,
and `--limit`.

### `taskmgr ready [--limit <n>]`

List issues ready to work: status `open` with no open blockers, ordered by
priority then age. `--limit` caps results.

### `taskmgr blocked`

List non-closed issues that have at least one open blocker. Human output prints
each blocked issue as a standard list row, then its blockers indented one per line
as `↳ <id>  <status>  <title>`:

```
dtt-0042  in_progress  P1  Fix drill navigation
  ↳ dtt-0040  open  Land the rail refactor
dtt-0051  open         P2  Wire up export
  ↳ dtt-0047  open  Define export schema
```

- **Output (JSON):** array of `blockedDTO` (§6) — `issueDTO` plus `blocked_by_refs`
  (`refDTO[]`).

---

## 4. Mutation commands

All mutations validate before writing and run under the project write lock.

### `taskmgr create --title <t> [options]`

Create a new issue and allocate its ID.

| Option | Default | Meaning |
|---|---|---|
| `--title <t>` | — | **Required.** Issue title. |
| `--description <md>` | empty | Description (markdown body). |
| `--description-file <path>` | — | Read the description from a file (`-` = stdin). |
| `--type <t>` | `task` | `task` \| `bug` \| `feature` \| `epic` \| `chore`. |
| `--priority <n>` | `2` | `0` (critical) … `4` (trivial). |
| `--assignee <a>` | empty | Assignee. |
| `--creator <a>` | `$USER` | Creator — who filed the issue; recorded once at creation. |
| `--label <l>` | — | Label; repeatable. |
| `--parent <id>` | — | Parent (epic/grouping) issue ID. |
| `--blocked-by <id>` | — | Blocker issue ID; repeatable. |
| `--related <id>` | — | Related issue ID; repeatable. |

- **Output:** the new ID (`{"id"}` in JSON).

### `taskmgr import [--file <path>] [--batch] [--run-hooks]`

Import a complete, externally-sourced issue **verbatim** — its final status
(including `closed`), original `created`/`updated`/`closed` timestamps, labels,
edges, and full comment log — in a single validated write. Unlike `create` (which
authors a new, open issue stamped with the store clock), `import` is a direct
write of an end-state: it is the low-level primitive a migration adapter (beads,
Jira, …) drives. All source-specific mapping lives in the adapter; taskmgr only
validates the envelope against the data model and writes it.

| Option | Default | Meaning |
|---|---|---|
| `--file <path>` | `-` | Read the import envelope from a file (`-` = stdin). |
| `--batch` | off | Input is a stream of envelopes (NDJSON / concatenated JSON); each is imported independently (best-effort). |
| `--run-hooks` | off | Run lifecycle hooks for each imported issue (gated as a `pre-create`/`post-create`; [HOOK-SPEC.md](HOOK-SPEC.md) §9). Default omits hooks so bulk loading does not fire a gate per issue. |

The envelope is a JSON object (timestamps RFC3339):

```jsonc
{
  "source_id": "bd-1",            // optional; echoed in the result, not stored
  "id": "at-keepme",              // optional caller-supplied taskmgr ID (else allocated)
  "title": "…", "type": "bug", "priority": 1,
  "status": "closed",             // any valid status; default open
  "assignee": "…", "creator": "…",
  "labels": ["beads:bd-1"],
  "parent": "<id>", "blocked_by": ["<id>"], "related": ["<id>"],
  "created_at": "2025-01-02T10:00:00Z",
  "updated_at": "2025-03-01T09:00:00Z",
  "closed_at": "2025-03-01T09:00:00Z", "close_reason": "fixed",
  "description": "markdown body",
  "comments": [{"author": "alice", "created_at": "2025-02-01T12:00:00Z", "body": "…"}]
}
```

- **Edges** (`parent`/`blocked_by`/`related`) are taskmgr IDs that **must already
  exist** — `import` enforces referential integrity and acyclicity exactly like
  `create`. Import in dependency order and translate foreign IDs to taskmgr IDs in
  the adapter.
- **Timestamps** are preserved as given. An unset `updated_at` inherits
  `created_at`; an unset `created_at` inherits the store clock. A `closed` status
  requires (or defaults `closed_at` to `updated_at`).
- **Validation is strict and atomic**: the whole envelope — fields, references,
  and every comment — is validated before anything is written, so control
  characters, bad enums, or dangling edges reject the record wholesale. The adapter
  is responsible for sanitizing source data to fit the model.
- **Output:** `{"source_id", "id"}` for a single import; with `--batch`, a JSON
  array of `{"source_id", "id", "error"}` (one per record) and a **non-zero exit
  if any record failed** (the others still land).

### `taskmgr update <id> [options]`

Apply a partial update. Only the flags you pass change; everything else is left
as-is.

| Option | Meaning |
|---|---|
| `--title <t>` | New title. |
| `--description <md>` | New description. |
| `--description-file <path>` | New description from a file (`-` = stdin). |
| `--status <s>` | New status (`open`/`in_progress`/`blocked`/`deferred`/`closed`). |
| `--type <t>` | New type. |
| `--priority <n>` | New priority. |
| `--assignee <a>` | New assignee. |
| `--parent <id>` | New parent (empty string clears it). |
| `--add-label <l>` | Add a label; repeatable. |
| `--remove-label <l>` | Remove a label; repeatable. |
| `--set-labels <l,…>` | Replace the entire label set. |
| `--clear-labels` | Remove all labels. |

- Setting `--status closed` transitions the issue to closed (stamps the close time
  and moves it to the cold partition) but records **no** reason — use `close
  --reason` for that. Setting a non-closed `--status` on a closed issue reopens it
  and lands on the status you asked for (`--status in_progress` → `in_progress`, not
  `open`).
- `creator` is provenance — set once at `create` and not editable here.
- **Output:** the updated `issueDTO`.

### `taskmgr close <id> [--reason <r>]`

Close an issue: set status `closed`, stamp the close time, optionally record
`--reason`, and move the file into the cold partition. Idempotent.

### `taskmgr reopen <id>`

Move a closed issue back to the active set, clear its closed timestamp/reason, and
set its status to `open`. No-op on an already-active issue. (To reopen directly into
another status, use `update --status`.)

### `taskmgr dep add <dependent> <blocker>`

Record that `<dependent>` is blocked by `<blocker>`. Idempotent; rejects
self-dependency and any edge that would create a cycle.

### `taskmgr dep rm <dependent> <blocker>`

Remove a blocking dependency.

### `taskmgr rel add <a> <b>`

Record a non-blocking **related** link between `<a>` and `<b>`. Idempotent;
rejects a self-link and a dangling reference. The relationship is **symmetric**:
the edge is stored on `<a>` and the inverse is derived on read, so the link shows
from both issues. (No cycle check — related is non-blocking.)

### `taskmgr rel rm <a> <b>`

Remove the related link between `<a>` and `<b>`. Removes the edge from **both**
sides so the link is fully severed (the primary `<a>` must be writable; the
inverse side is best-effort and skipped if `<b>` is closed).

### `taskmgr comment add <id> [body] [options]`

Append a comment to an issue's sidecar. The body comes from the positional
argument or `--file`.

| Option | Default | Meaning |
|---|---|---|
| `--author <a>` | `$USER` | Comment author. |
| `--file <path>` | — | Read the body from a file (`-` = stdin). |

- Empty bodies are rejected. Bodies are sanitized (trailing whitespace stripped,
  CRLF normalized) so they store as readable block scalars.
- **Output (JSON):** `commentDTO` for the new comment (including its `id`), so
  callers can use the id for a later `comment edit` or `comment rm`.

### `taskmgr comment edit <id> <comment-id> [body] [options]`

Append a revision that supersedes an earlier comment (`replaces`). The original
stays in the log; readers render the newest revision. Same body source/options as
`comment add`. The body must be non-empty — use `comment rm` to delete.

| Option | Default | Meaning |
|---|---|---|
| `--author <a>` | `$USER` | Comment author. |
| `--file <path>` | — | Read the body from a file (`-` = stdin). |

- **Output (JSON):** `commentDTO` for the new revision comment.

### `taskmgr comment rm <id> <comment-id> [--author <a>]`

Delete a comment: append a tombstone that retracts the target (`replaces` it with
no body). The original stays in the log as history; the resolved view omits it.
Idempotent.

| Option | Default | Meaning |
|---|---|---|
| `--author <a>` | `$USER` | Author of the tombstone record. |

---

## 5. Catalog & discovery commands

| Command | Output |
|---|---|
| `taskmgr labels` | Distinct labels in use, sorted. |
| `taskmgr statuses` | The valid status values, in display order. |
| `taskmgr types` | The valid issue types, in display order. |
| `taskmgr version` | Version, commit, build date (`{"version","commit","date"}` in JSON). |
| `taskmgr commands` | Machine-readable catalog of every command — name, purpose, flags, and a usage example — derived from the live command tree (never drifts). YAML by default; `--json` for JSON. Intended for agents. |
| `taskmgr guide` | A compact, workflow-shaped how-to: the issue model, the everyday command loop, the filter language in brief, and where to find more. Owned and emitted by the binary; hand-maintained prose (unlike the derived `commands`), with a conformance test keeping its model lists in step with the SDK. Plain text to stdout; `--json` wraps it as `{"guide": "..."}`. The prose companion to `commands` — both are kept. |

---

## 6. JSON output shapes

Stable `snake_case` DTOs. Optional fields are omitted when empty.

**`issueDTO`** — emitted by `create` (id only), `list`, `search`, `ready`, and
nested in others:

```json
{
  "id": "dtt-0042", "title": "…", "status": "open", "type": "bug",
  "priority": 1, "assignee": "hans", "creator": "hans", "labels": ["area:x"],
  "parent": "dtt-0007", "blocked_by": ["dtt-0040"], "related": ["dtt-0012"],
  "created": "2026-06-01T10:00:00Z", "updated": "2026-06-04T09:00:00Z",
  "closed": "2026-06-05T08:00:00Z", "close_reason": "fixed"
}
```

**`refDTO`** — a lightweight reference (no body): `{id, title, type, status, priority}`.

**`commentDTO`** — `{id, author, created, replaces, body}` where `id` is the
comment's random token (`^[0-9a-z]{8}$`); `author`/`replaces` are omitted when
empty. The `comments` array (in `detailDTO`) is the **resolved** log: each
`replaces`-chain collapsed to its newest revision, tombstoned comments omitted.

**`detailDTO`** — `issueDTO` plus: `description`, `parent_ref` (`refDTO`),
`blocked_by_refs`, `related_refs`, `blocks`, `children` (each `refDTO[]`), and
`comments` (`commentDTO[]`). Emitted by `show`.

**`blockedDTO`** — `issueDTO` plus `blocked_by_refs` (`refDTO[]`). Emitted by
`blocked`.

**`whereDTO`** — emitted by `where`. `kind` is one of `local` | `central` |
`override_path` | `override_name` | `none` (mirrors the engine's `ResolveKind`,
SDK-SPEC §1). `store_path` and `project_path` are omitted when `kind` is `none`:

```json
{ "kind": "central", "store_path": "/home/hans/.taskmgr/my-project",
  "project_path": "/home/hans/dev/my-project" }
```

**`storeListDTO`** — emitted by `store list`, one per registry entry:
`{path, store, store_path}` (the project path, the registry name, and the resolved
store directory). No health/status field in this slice.

**Hook output ([HOOK-SPEC.md](HOOK-SPEC.md) §6.2).** A mutation that runs hooks surfaces
their output alongside the normal result. On success the JSON carries optional
`"hints": [string]` (advisory notes from any hook that ran) and `"warnings": [string]`
(post-hook failures, which never fail the write). A pre-hook **denial** exits non-zero and
prints a structured error:

```json
{ "error": "hook_denied", "event": "pre-close", "hook": "tests-before-close",
  "issue_id": "dtt-0042", "exit": 1, "reason": "3 unit tests failing",
  "hints": ["run `make fmt` before retrying"] }
```

---

## 7. Command summary

```
taskmgr init     [--prefix X] [--central [--store-name N]]
taskmgr where                                # which store resolves here, and why
taskmgr store    list                        # enumerate central registry entries
taskmgr create   --title T [--description[-file] --type --priority --assignee
                          --creator --label… --parent --blocked-by… --related…]
taskmgr import   [--file <path>] [--batch] [--run-hooks]   # JSON envelope on stdin/file
taskmgr show     <id>
taskmgr list     [-q <expr>] [--all --sort --reverse --limit]
taskmgr search   <text> [--all --sort --reverse --limit]
taskmgr ready    [--limit]
taskmgr blocked
taskmgr update   <id> [--title --description[-file] --status --type --priority
                     --assignee --parent --add-label --remove-label
                     --set-labels --clear-labels]
taskmgr close    <id> [--reason]
taskmgr reopen   <id>
taskmgr dep      add|rm <dependent> <blocker>
taskmgr rel      add|rm <a> <b>              # symmetric related link
taskmgr comment  add  <id> [body] [--author --file]
taskmgr comment  edit <id> <comment-id> [body] [--author --file]
taskmgr comment  rm   <id> <comment-id> [--author]
taskmgr labels | statuses | types
taskmgr version
taskmgr commands                             # machine catalog (YAML/JSON)
taskmgr guide                                # workflow how-to (start here)

Global: --json, -C/--dir <path>, --store-path <path> | --store-name <name>
Env:    TASKMGR_DIR, TASKMGR_HOME, TASKMGR_LOG
```
