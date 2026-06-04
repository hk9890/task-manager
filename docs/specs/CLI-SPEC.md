# CLI Specification — `atctl`

This document specifies the `atctl` command-line interface: every command, its
arguments and options, and what it does. `atctl` is the agent-facing front end to
a `.tasks` store (see [TASK-STORAGE-SPEC.md](TASK-STORAGE-SPEC.md) for the on-disk
format it operates on).

---

## 1. Invocation & global conventions

```
atctl <command> [subcommand] [args] [flags]
```

### Persistent flags (valid on every command)

| Flag | Default | Meaning |
|---|---|---|
| `--json` | off | Emit machine-readable JSON instead of the human table/detail view. |
| `-C, --dir <path>` | cwd | Start directory for locating the store; `.tasks` is found by walking up. |
| `--project <prefix>` | auto | Select a project when the workspace holds more than one. |

### Output modes

- **Human (default):** compact, aligned tables for lists; a labelled block for a
  single issue.
- **JSON (`--json`):** stable, `snake_case` shapes (§5). Pretty-printed, HTML
  escaping disabled. This is the contract for agents and tools.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Success. |
| `1` | Any error (not found, validation failure, no store, I/O). The message is printed to stderr, prefixed `atctl: `. |

### Discovery

The store is located by walking up from `--dir` (or cwd) until a `.tasks`
directory is found. Most commands fail with a "no store" error if none exists;
`init` is the exception.

---

## 2. Setup commands

### `atctl init`

Create a new store in the current project.

| Option | Default | Meaning |
|---|---|---|
| `--prefix <p>` | derived from directory name | ID prefix for the project (`^[a-z][a-z0-9]*$`). |

- Creates the project directory and its `config.yaml`.
- If `--prefix` is omitted it is derived from the directory name (lowercased,
  non-alphanumerics stripped, leading digits removed, truncated; falls back to
  `task`).
- Fails if a store already exists.
- **Output:** the store path and chosen prefix (`{"dir","prefix"}` in JSON).

---

## 3. Read commands

### `atctl show <id>`

Show full detail for one issue: all fields, resolved relationships (parent,
blocked-by, related, plus derived **blocks** and **children**), the description
body, and comments.

- **Output (JSON):** `detailDTO` (§5).

### `atctl list [-q <expr>] [options]`

List issues selected by a **filter expression** (§3.1). Closed issues are excluded
unless the expression selects them or `--all` is given. Default order: priority
(most urgent first), then oldest.

| Flag | Meaning |
|---|---|
| `-q, --query <expr>` | Filter expression (§3.1). Omitted → all active issues. |
| `--all` | Include closed issues (reads the cold partition). |
| `--sort <field>` | `id` \| `priority` \| `created` \| `updated` \| `closed` (default: priority). |
| `--reverse` | Reverse the sort order. |
| `--limit <n>` | Cap the number of results (`0` = all). |

- **Output (JSON):** array of `issueDTO`.

### 3.1 Filter expressions

A filter expression selects issues by combining field predicates with boolean
operators. The CLI and the SDK share this grammar.

A predicate is `<field> <op> <value>`, or a bare boolean field.

| Field | Values | Notes |
|---|---|---|
| `status` | `open` / `in_progress` / `blocked` / `closed` | |
| `type` | `task` / `bug` / `feature` / `epic` / `chore` | |
| `priority` | `0`–`4` | numeric comparisons |
| `assignee` | string | |
| `parent` | issue ID | |
| `label` | string | `==` exact match, `~` substring / membership |
| `text` | string | `~` matches ID, title, or description |
| `created` / `updated` / `closed` | ISO-8601 date | date comparisons |
| `ready` / `blocked` | — | bare boolean predicates |

Operators: `==` `!=` `<` `<=` `>` `>=` `~` (contains). Combine predicates with
`&&`, `||`, `!`, and parentheses.

```
status == "open"
status == "open" && priority <= 1
type == "bug" && label ~ "area:db"
ready && priority <= 2
text ~ "drill" && !blocked
closed > "2026-01-01"
```

Closed issues are excluded unless the expression references `status == "closed"`
or `--all` is passed.

### `atctl search <text> [options]`

Shorthand for matching `<text>` against the ID, title, or description —
equivalent to `list -q 'text ~ "<text>"'`. Accepts `--all`, `--sort`, `--reverse`,
and `--limit`.

### `atctl ready [--limit <n>]`

List issues ready to work: status `open` with no open blockers, ordered by
priority then age. `--limit` caps results.

### `atctl blocked`

List non-closed issues that have at least one open blocker, each followed by the
blocking issues (ID, status, title).

- **Output (JSON):** array of `issueDTO` extended with `blocked_by_refs` (`refDTO[]`).

---

## 4. Mutation commands

All mutations validate before writing and run under the project write lock.

### `atctl create --title <t> [options]`

Create a new issue and allocate its ID.

| Option | Default | Meaning |
|---|---|---|
| `--title <t>` | — | **Required.** Issue title. |
| `--description <md>` | empty | Description (markdown body). |
| `--description-file <path>` | — | Read the description from a file (`-` = stdin). |
| `--type <t>` | `task` | `task` \| `bug` \| `feature` \| `epic` \| `chore`. |
| `--priority <n>` | `2` | `0` (critical) … `4` (trivial). |
| `--assignee <a>` | empty | Assignee. |
| `--label <l>` | — | Label; repeatable. |
| `--parent <id>` | — | Parent (epic/grouping) issue ID. |
| `--blocked-by <id>` | — | Blocker issue ID; repeatable. |
| `--related <id>` | — | Related issue ID; repeatable. |

- **Output:** the new ID (`{"id"}` in JSON).

### `atctl update <id> [options]`

Apply a partial update. Only the flags you pass change; everything else is left
as-is.

| Option | Meaning |
|---|---|
| `--title <t>` | New title. |
| `--description <md>` | New description. |
| `--description-file <path>` | New description from a file (`-` = stdin). |
| `--status <s>` | New status (`open`/`in_progress`/`blocked`/`closed`). |
| `--type <t>` | New type. |
| `--priority <n>` | New priority. |
| `--assignee <a>` | New assignee. |
| `--parent <id>` | New parent (empty string clears it). |
| `--add-label <l>` | Add a label; repeatable. |
| `--remove-label <l>` | Remove a label; repeatable. |
| `--set-labels <l,…>` | Replace the entire label set. |
| `--clear-labels` | Remove all labels. |

- Setting `--status closed` is equivalent to `close` (sets the closed timestamp);
  moving away from `closed` reopens.
- **Output:** the updated `issueDTO`.

### `atctl close <id> [--reason <r>]`

Close an issue: set status `closed`, stamp the close time, optionally record
`--reason`, and move the file into the cold partition. Idempotent.

### `atctl reopen <id>`

Move a closed issue back to the active set, clear its closed timestamp/reason, and
restore a non-closed status.

### `atctl dep add <dependent> <blocker>`

Record that `<dependent>` is blocked by `<blocker>`. Idempotent; rejects
self-dependency and any edge that would create a cycle.

### `atctl dep rm <dependent> <blocker>`

Remove a blocking dependency.

### `atctl comment add <id> [body] [options]`

Append a comment to an issue's sidecar. The body comes from the positional
argument or `--file`.

| Option | Default | Meaning |
|---|---|---|
| `--author <a>` | `$USER` | Comment author. |
| `--file <path>` | — | Read the body from a file (`-` = stdin). |

- Empty bodies are rejected. Bodies are sanitized (trailing whitespace stripped,
  CRLF normalized) so they store as readable block scalars.

### `atctl comment edit <id> <comment-id> [body] [options]`

Append a revision that supersedes an earlier comment (`replaces`). The original
stays in the log; readers render the newest revision. Same body source/options as
`comment add`. An empty body retracts (tombstones) the target comment.

---

## 5. Catalog commands

| Command | Output |
|---|---|
| `atctl labels` | Distinct labels in use, sorted. |
| `atctl statuses` | The valid status values, in display order. |
| `atctl types` | The valid issue types, in display order. |
| `atctl version` | Version, commit, build date (`{"version","commit","date"}` in JSON). |

---

## 6. JSON output shapes

Stable `snake_case` DTOs. Optional fields are omitted when empty.

**`issueDTO`** — emitted by `create` (id only), `list`, `search`, `ready`, and
nested in others:

```json
{
  "id": "dtt-0042", "title": "…", "status": "open", "type": "bug",
  "priority": 1, "assignee": "hans", "labels": ["area:x"],
  "parent": "dtt-0007", "blocked_by": ["dtt-0040"], "related": ["dtt-0012"],
  "created": "2026-06-01T10:00:00Z", "updated": "2026-06-04T09:00:00Z",
  "closed": "2026-06-05T08:00:00Z", "close_reason": "fixed"
}
```

**`refDTO`** — a lightweight reference (no body): `{id, title, type, status, priority}`.

**`commentDTO`** — `{id, author, created, replaces, body}` (`author`/`replaces`
omitted when empty).

**`detailDTO`** — `issueDTO` plus: `description`, `parent_ref` (`refDTO`),
`blocked_by_refs`, `related_refs`, `blocks`, `children` (each `refDTO[]`), and
`comments` (`commentDTO[]`). Emitted by `show`.

**`blockedDTO`** — `issueDTO` plus `blocked_by_refs` (`refDTO[]`). Emitted by
`blocked`.

---

## 7. Command summary

```
atctl init     [--prefix X]
atctl create   --title T [--description[-file] --type --priority --assignee
                          --label… --parent --blocked-by… --related…]
atctl show     <id>
atctl list     [-q <expr>] [--all --sort --reverse --limit]
atctl search   <text> [--all --sort --reverse --limit]
atctl ready    [--limit]
atctl blocked
atctl update   <id> [--title --description[-file] --status --type --priority
                     --assignee --parent --add-label --remove-label
                     --set-labels --clear-labels]
atctl close    <id> [--reason]
atctl reopen   <id>
atctl dep      add|rm <dependent> <blocker>
atctl comment  add  <id> [body] [--author --file]
atctl comment  edit <id> <comment-id> [body] [--author --file]
atctl labels | statuses | types
atctl version

Global: --json, -C/--dir <path>, --project <prefix>
```
