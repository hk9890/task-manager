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
| `--store-name <name>` | — | Override resolution: operate on the central store with this registry name. |

### Environment variables

| Variable | Meaning |
|---|---|
| `TASKMGR_HOME` | The taskmgr home holding the global config and (by default) the central store root. Default `~/.taskmgr`. See [CONFIG-SPEC.md](CONFIG-SPEC.md) §1. |
| `TASKMGR_LOG` | Log level for observability output: `debug`, `info`, `warn`, `error` (default `warn`; an unknown value falls back to `warn`). Records always go to stderr as text. See [MONITORING.md](../MONITORING.md). |
| `TASKMGR_DIR` | **Withdrawn — rejected, not ignored.** Any non-empty value fails the command with an error naming it. It once overrode the store directory, as `--store-path` did; that flag now fails as unknown, and an exported variable gets the same treatment rather than silently misfiling every write into whatever store the walk-up finds. See [CONFIG-SPEC.md](CONFIG-SPEC.md) §4. |

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

1. an explicit **override** — `--store-name`;
2. otherwise **local walk-up** from `--dir` (or cwd) for a `.tasks` directory (the
   long-standing behaviour — a local store always wins);
3. otherwise the **central registry** — if a central store is mapped to the current
   project path, use it.

The **resolution origin** is `--dir`/`-C` if given, else the cwd. It feeds *every*
step alike: the local walk-up start, the path matched against the registry, and the
project path recorded by `init --central` (CONFIG-SPEC §4, `W`).

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
- **Central (`--central`):** creates `<central_root>/stores/<name>/` as an ordinary
  store and adds a registry entry mapping the current project path to it. `--store-name`
  must match the store-name grammar (CONFIG-SPEC §3). Fails if that subfolder or a
  registry entry for this path already exists.
- If `--prefix` is omitted it is derived from the project directory name (lowercased,
  non-alphanumerics stripped, leading digits removed, truncated to 8 characters; falls back to `task`).
  This holds for both local and central stores — prefixes are per project, with no
  global default (CONFIG-SPEC §5).
- `--dir`/`-C` is made absolute against the working directory **before** the prefix and
  the store name are derived from its last element. `-C .` would otherwise derive from
  `"."` and take the `task` fallback — into a prefix that is then immutable — and `-C ..`
  a name the store-name grammar rejects.
- **Output:** the store path and chosen prefix (`{"dir","prefix"}` in JSON; with
  `--central`, also the registry `name`).

---

## 2.1 Store inspection and relocation

Two read-only commands surface the central setup, and one editing verb moves a
store between locations. `unlink` — dropping an entry — remains a use-gated
follow-up; the registry is one short YAML file the user can hand-edit
(CONFIG-SPEC §5).

### `taskmgr where`

Show which store the current working directory resolves to and **why** — the diagnostic
for the resolution rule above. It mirrors the engine's `ResolveKind` (SDK-SPEC §1)
verbatim, so the override distinction is not lost:

- `kind`: `local` | `central` | `override_name`, or `none` when nothing resolves.
- `store`: the registry name (omitted for a local store, which has none).
- `store_path`: the resolved store directory (omitted when `kind` is `none`).
- `project_path`: the project the store tracks (the store's parent for a local store;
  omitted when `kind` is `none`).

Never errors on no-store; exits `0` with `kind: none`. **Output (JSON):** `whereDTO` (§6).

### `taskmgr store list`

Enumerate the registry entries — each entry's project `path`, `store` name, the store
directory, and its `health`: `ok`, `dangling` (no subfolder) or `broken` (a subfolder
without `config.yaml`), the same three cases resolution acts on (CONFIG-SPEC §3).
Listing them is what makes a store switcher possible: the caller sees the entries that
will not open before it offers them, instead of one error per selected row.

Classification is a `stat` per entry, not an open, so the listing stays a read and
never takes a store's lock. Only "no such file or directory" makes an entry `dangling`;
a `stat` that fails for any other reason exits `1` with that failure, rather than
reporting stores it could not read as gone (CONFIG-SPEC §3).

- **Output (JSON):** array of `storeListDTO` (§6).

### `taskmgr store move`

Move a store between locations, in one of three modes. Exactly one mode flag is
required, and they are mutually exclusive.

| Option | Default | Meaning |
|---|---|---|
| `--central` | off | Promote the local store resolving here into the central root and register it. |
| `--rename` | off | Rename the central store resolving here (folder and registry entry). |
| `--relink` | off | Re-point the registry entry named by `--to` at this directory. |
| `--to <name>` | project basename (with `--central`) | The registry name: the new name for `--central`/`--rename`, the existing entry for `--relink`. Required for `--rename` and `--relink`. |

- **`--central`** requires a store that resolves as `local`; a store that is already
  central is an error. The store moves to `<central_root>/stores/<to>` and an entry is
  added for the project path. Fails if that subfolder or an entry for this path already
  exists. **The local `.tasks` directory is gone afterwards** — there is no confirmation
  prompt (the CLI is agent-facing and never prompts) and no git interaction, so
  committing the removal is the user's to do.
- **`--rename`** requires a store that resolves as `central` or `override_name`; use
  `--store-name` to rename a store you are not standing in. Fails if `--to` is already
  taken.
- **`--relink`** touches no files. Fails if `--to` is not registered, if its store
  subfolder is not a finished store — missing, or present without a `config.yaml`; it
  will not write an entry that resolution then skips — if the target directory does not
  exist, or if another entry already maps this project path (matched on both the raw and
  the symlink-resolved form, as entry creation is, so a project cannot be claimed twice
  across a symlink). `--dir`/`-C` is made absolute against the working directory before
  it is recorded.
- A store keeps its `config.yaml` verbatim across all three modes, so the ID prefix and
  the `use:` list travel with it and existing IDs stay valid. A package the store holds
  by path — under `.tasks/packages/` — moves with the tree, and its hooks' relative
  `argv[0]` resolves against wherever the package now is
  ([HOOK-SPEC](HOOK-SPEC.md) §3.6), so a gate that shipped with the repository keeps
  working after a promote. What does **not** follow the store is a `name:` entry, which
  resolves per machine, or an argv path a hook builds itself at run time from its working
  directory — that is still the project root, which the move does not change.
- **Output:** the store name, store path, and project path (`storeMoveDTO` in JSON, §6).

---

## 2.2 Configuration commands

`taskmgr config` reads and writes configuration. Two files are addressable and every
subcommand selects between them the same way:

| | Target | Needs a store |
|---|---|---|
| default | the resolved store's `config.yaml` (TASK-STORAGE-SPEC §4.2) | yes |
| `--global` | the per-user `config.yaml` (CONFIG-SPEC §2) | no |

Because `--global` resolves no store, it works in a directory where nothing resolves.
`--global` is a persistent flag on the group, so it is accepted in both positions:
`taskmgr config --global list` and `taskmgr config list --global` are the same command.

Every write goes through the engine, which re-reads the file under its lock and applies
the change there, so two concurrent invocations editing different keys keep both edits
(CONFIG-SPEC §2, SDK-SPEC §1). The engine validates before a byte lands: an unparseable
`hook_timeout` is refused at the command that wrote it, rather than at the next mutation
(HOOK-SPEC §3.4). A refused command leaves the file byte-for-byte unchanged.

`taskmgr package` (§2.3) edits the same two files under the same locks; it is a separate
group because the `use:` list is a list rather than a scalar key.

### `taskmgr config keys`

List every supported key with its scope (`store` / `global`), whether it is writable,
and what it means. It is the static catalog, so it reads nothing and needs no store.

The `use:` list of hook packages is a list, not a scalar, and is absent from this table:
it is managed with `taskmgr package` (§2.3).

- **Output (JSON):** array of `configKeyDTO` (§6).

**The keys:**

| Key | Scope | Writable | Meaning |
|---|---|---|---|
| `prefix` | store | no | The store's ID prefix. Read-only: it is part of every issue ID and of every stored reference, so changing it would orphan the store. |
| `hook_timeout` | store | yes | Per-hook wall-clock limit (`2s`, `5m`; `0` disables). Unset means the 2s default. |
| `version` | global | no | Schema version of the per-user config. |
| `central_root` | global | yes | Directory holding the registry and central stores. Unset means the taskmgr home. Setting it does **not** move existing stores. |
| `hook_timeout` | global | yes | Fallback limit for a store that sets none (HOOK-SPEC §3.5). |

A package cannot set `hook_timeout`; the limit bounds how long the store lock is held, so
it is the machine owner's to set (HOOK-SPEC §3.1).

### `taskmgr config list [--global]`

Show the current value of every key of one file, with the scope and the file's path.

- **Output (JSON):** `configListDTO` (§6). In human output an empty value renders
  `(unset)`.

### `taskmgr config get <key> [--global]`

Print one key's value and nothing else, so a script can consume it without parsing. An
unset key prints an empty line and exits `0`. An unknown key exits `1` and names the keys
that would have worked.

- **Output (JSON):** `configValueDTO` (§6).

### `taskmgr config set <key> <value> [--global]`

Set one key. A read-only key exits `1`, and so does an **empty** `<value>`: clearing a key
is `unset`'s job, and a wrapper passing an unset shell variable would otherwise delete the
key and exit `0`.

### `taskmgr config unset <key> [--global]`

Clear one key, restoring its documented default. A read-only key exits `1`.

---

## 2.3 Hook packages

A hook package is a directory holding a manifest and the scripts its hooks run
(HOOK-SPEC §3.6). taskmgr never creates, downloads or extracts one: a package has a
single owner, so authoring it is making a directory and writing a YAML file, with nothing
for taskmgr to mediate. What these commands manage is the shared, lock-protected `use:`
list, and the merged chain that no single file shows.

`taskmgr package` takes the same `--global` selector as `taskmgr config`, in either
position, with the same two targets.

### `taskmgr package add <name> [--path] [--global]`

Append one entry to the target file's `use:` list.

| Option | Default | Meaning |
|---|---|---|
| `--path` | off | Treat the argument as a directory rather than a package name. The path resolves against the directory holding the config file: `.tasks/` for a store, the taskmgr home for `--global`. |
| `--global` | off | Act on the per-user config instead of the store's. |

Without `--path` the argument is a package name, resolved to
`<taskmgr home>/packages/<name>`. The two forms are separate because a reference states
which it is rather than leaving a loader to guess (HOOK-SPEC §3.5).

The package is **loaded and checked before the entry is written**, so one that could
never run is refused here rather than at the next unrelated mutation. A package that is
merely **not installed yet** is written and reported as a warning: a store's config
travels in git, so it legitimately names a package the machine writing it does not have.
An entry that duplicates one already in the file exits `1`.

- **Output:** the entry and what it resolves to (`packageDTO` in JSON, §6).

### `taskmgr package list [--global]`

List the `use:` entries that apply, each with the directory it resolves to on this
machine and its status:

| Status | Means |
|---|---|
| `ok` | The directory is there and holds a manifest that loads. |
| `missing` | Nothing at the resolved path. |
| `broken` | A directory that is not a finished package: no manifest, or one that does not load. |

The vocabulary is `store list`'s (§2.1), so a listing and a mutation never disagree about
an entry. An entry whose package name was already taken by an earlier one is reported
`shadowed` (HOOK-SPEC §3.5 rule 3) rather than omitted.

Without `--global` the listing covers **both** files in the order their hooks run, because
that is what gates the store; with `--global` it covers the per-user file alone and needs
no store.

- **Output (JSON):** array of `packageDTO` (§6).

### `taskmgr hook list`

List every hook that gates the resolved store, in the order it runs: the per-user
config's packages first, then the store's, each package's hooks in manifest order
(HOOK-SPEC §3.5).

This is the authoritative answer to what gates a store. Neither config file can give it
alone — the hooks live in the packages the two files name — so ordering is settled by
reading this rather than by tracing two files by hand.

Each row carries the **effective id**, `pkg:<package>:<hook>`: the id a denial reason
reports and the logs record. The scope column names the file whose `use:` list brought
the package in, and therefore the file to edit.

- **Output (JSON):** array of `hookDTO` (§6).

---

## 3. Read commands

### `taskmgr show <id>`

Show full detail for one issue: all fields, resolved relationships (parent,
blocked-by, related, plus derived **blocks** and **children**), the description
body, and comments (the **resolved** log — edits applied, deleted comments
removed; see storage spec §4.4).

A body larger than 4096 bytes is **truncated in human output**, followed by a
notice giving its full size and, when the body lives in the content sidecar
(TASK-STORAGE-SPEC §4.6), its path. Truncation is a display choice only: `--json`
always carries the complete body, because a script or an agent asked for all of
it. Bodies are unbounded, so a doc holding a generated page would otherwise flood
a terminal on every `show`.

- **Output (JSON):** `detailDTO` (§6) — never truncated.

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

### `taskmgr search <text> [more words...] [options]`

Matches `<text>` against the ID, title, or description. Multiple words use
**AND-of-words** (order-independent): every word must appear, e.g. `search drill nav`
is equivalent to `list -q 'text ~ "drill" && text ~ "nav"'` and matches regardless of
order or adjacency. Matching is per-word substring, so `search cat` also matches
"category". A single word is equivalent to `list -q 'text ~ "<word>"'`. The shared
semantic is `tasks.SearchExpr` (SDK-SPEC §3), so the CLI and any UI search
identically. A `-q` expression, if given, is AND-ed with the text match. Accepts
`--all`, `--sort`, `--reverse`, and `--limit`.

The description is matched wherever it is stored: an issue whose body overflowed
to the content sidecar is searched on its full content (QUERY-SPEC §2). Documents
(`type: doc`) are searched like any other issue — they are excluded from `ready`
and `blocked`, nothing else.

For an exact **contiguous phrase** instead of AND-of-words, use the query form
directly: `list -q 'text ~ "drill nav"'` (matches only where the words are adjacent
and in order). This is the documented phrase escape hatch — `search` itself stays
AND-of-words with no extra flag, in keeping with "filter via `-q`."

### `taskmgr ready [--limit <n>]`

List issues ready to work: status `open` with no open blockers, ordered by
priority then age. `--limit` caps results.

### `taskmgr blocked`

List non-closed issues that have at least one open blocker. Human output prints
each blocked issue as a standard list row, then its blockers indented one per line
as `↳ <id>  <status>  <title>`:

```
proj-0042  in_progress  P1  Fix drill navigation
  ↳ proj-0040  open  Land the rail refactor
proj-0051  open         P2  Wire up export
  ↳ proj-0047  open  Define export schema
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
| `--type <t>` | `task` | `task` \| `bug` \| `feature` \| `epic` \| `chore` \| `doc`. `doc` carries a document rather than work: it is an ordinary issue but never appears in `ready` / `blocked` (TASK-STORAGE-SPEC §9). Use `--description-file` to load a page from disk; a large body is stored in the content sidecar automatically (§4.6). |
| `--priority <n>` | `2` | `0` (critical) … `4` (trivial). |
| `--assignee <a>` | empty | Assignee. |
| `--creator <a>` | `$USER` | Creator — who filed the issue; recorded once at creation. |
| `--label <l>` | — | Label; repeatable. |
| `--parent <id>` | — | Parent (epic/grouping) issue ID. |
| `--blocked-by <id>` | — | Blocker issue ID; repeatable. |
| `--related <id>` | — | Related issue ID; repeatable. |

- **Output:** the new ID (`{"id", "store"}` in JSON; `store` is the registry name of
  the store it landed in, omitted for a local store — §6).

### `taskmgr import [--file <path>] [--batch] [--run-hooks]`

Import a complete, externally-sourced issue **verbatim** — its final status
(including `closed`), original `created`/`updated`/`closed` timestamps, labels,
edges, and full comment log — in a single validated write. Unlike `create` (which
authors a new, open issue stamped with the store clock), `import` is a direct
write of an end-state: it is the low-level primitive a migration adapter (e.g.
Jira, GitHub) drives. All source-specific mapping lives in the adapter; taskmgr only
validates the envelope against the data model and writes it.

| Option | Default | Meaning |
|---|---|---|
| `--file <path>` | `-` | Read the import envelope from a file (`-` = stdin). |
| `--batch` | off | Input is a stream of envelopes (NDJSON / concatenated JSON); each is imported independently (best-effort). |
| `--run-hooks` | off | Run lifecycle hooks for each imported issue (gated as a `pre-create`/`post-create`; [HOOK-SPEC.md](HOOK-SPEC.md) §9). Default omits hooks so bulk loading does not fire a gate per issue. |

The envelope is a JSON object (timestamps RFC3339):

```jsonc
{
  "source_id": "ext-1",           // optional; echoed in the result, not stored
  "id": "at-keepme",              // optional caller-supplied taskmgr ID (else allocated)
  "title": "…", "type": "bug", "priority": 1,
  "status": "closed",             // any valid status; default open
  "assignee": "…", "creator": "…",
  "labels": ["ext:ext-1"],
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
- **Output:** `{"source_id", "id", "store"}` for a single import; with `--batch`, a
  JSON array of `{"source_id", "id", "store", "error"}` (one per record) and a
  **non-zero exit if any record failed** (the others still land). `store` is the
  registry name the records landed in, omitted for a local store (§6), so the
  source-ID → taskmgr-ID map an adapter builds says which store it maps into.

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

Remove a blocking dependency. Removing one that is not present succeeds and
writes nothing — the file is not rewritten and `updated` is not bumped
(SDK-SPEC §4, the shared no-op contract of the four edge commands).

### `taskmgr rel add <a> <b>`

Record a non-blocking **related** link between `<a>` and `<b>`. Idempotent;
rejects a self-link and a dangling reference. The relationship is **symmetric**:
the edge is stored on `<a>` and the inverse is derived on read, so the link shows
from both issues. (No cycle check — related is non-blocking.)

### `taskmgr rel rm <a> <b>`

Remove the related link between `<a>` and `<b>`. Removes the edge from **both**
sides so the link is fully severed (the primary `<a>` must be writable; the
inverse side is best-effort and skipped if `<b>` is closed). Removing a link that
is not present writes nothing, as for `dep rm`.

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
| `taskmgr guide [topic...]` | A workflow-shaped how-to in named parts: the issue model, the everyday command loop, the filter language, and what this store adds. Bare, it prints the **overview** — the roster and where to go next, not the whole guide. Owned and emitted by the binary; hand-maintained prose (unlike the derived `commands`), with conformance tests keeping its model lists and the flags it names in step with the live tree. Plain text to stdout; `--json` wraps it as `{"guide": "..."}`. The prose companion to `commands` — both are kept. Topics: §5.1 below. |

### 5.1 Guide topics

The guide is in named parts, and a caller takes the part it needs.

**With no argument it prints the overview** — what the tool is, the roster of parts
with the command that fetches each, and whatever the store's packages put in the
overview themselves (HOOK-SPEC.md §3.7). Never the whole guide. The reader injects
this output into its own instructions before it acts, so the no-argument form is
the small constant every caller can afford, and it names what to fetch next; a
caller that needs the filter language and one that needs the filing loop do not
each pay for the other's sections.

The roster is **generated** from the sections the binary carries and the fragments
the store's packages contribute. A part that exists is therefore always named, which
is what makes fetch-on-demand safe: a caller can only ask for what the overview told
it exists.

**With arguments it prints exactly the topics named**, in the order they were named
— the caller composes the slice it wants rather than accepting this command's order.

| Topic | Selects |
|---|---|
| *(none)* | The overview: the roster, and the packages' overview fragments. |
| a core id | One built-in section. `--list` is the roster as data. |
| `pkg:<package>:<id>` | One package's fragment, its `overview` one included. |
| `packages` | Every package fragment at once, and nothing built in. |

| Option | Default | Meaning |
|---|---|---|
| `--list` | off | Print the topic roster instead of the guide: id, kind (`core`/`package`), and one line of contents. `--json` gives `guideTopicDTO[]` (§6). |

**This command does not fail on the state of the machine.** No store resolving, a
`use:` entry naming an uninstalled package, a fragment whose file is missing — each
is reported in the output, and the exit code stays `0`. The reader is an agent that
pastes this output into its own instructions before it acts, so a non-zero exit
denies it the whole guide in order to report one missing part. HOOK-SPEC.md §3.7
states the rule this is the command surface for: fail-closed protects a write from
running without its gate, and a guide is not a gate.

Two consequences, both normative:

- **An unknown core topic exits `1`.** No package can make one exist, so naming one
  is only ever a mistake in the caller, catchable when it is written.
- **An unknown `pkg:` topic exits `0`** with a line saying it is not available here.
  Whether a package is installed is a property of the machine, so refusing would
  turn a colleague's missing install into a failed command.

---

## 6. JSON output shapes

Stable `snake_case` DTOs. Optional fields are omitted when empty.

**`issueDTO`** — emitted by `create` (`id` and `store` only), `list`, `search`,
`ready`, and nested in others:

```json
{
  "id": "proj-0042", "store": "my-project", "title": "…", "status": "open", "type": "bug",
  "priority": 1, "assignee": "hans", "creator": "hans", "labels": ["area:x"],
  "parent": "proj-0007", "blocked_by": ["proj-0040"], "related": ["proj-0012"],
  "created": "2026-06-01T10:00:00Z", "updated": "2026-06-04T09:00:00Z",
  "closed": "2026-06-05T08:00:00Z", "close_reason": "fixed"
}
```

`store` is the registry name of the store the issue came from, **omitted for a local
store**, which has no registry entry to take a name from. It is what makes a merged
result set from several stores unambiguous; the ID does not, because a prefix is
derived from the project directory name, so two projects whose directories share a
name share a prefix (CONFIG-SPEC §5). `create` carries it beside the `id` for the same
reason.

**`refDTO`** — a lightweight reference (no body): `{id, title, type, status, priority}`.

**`commentDTO`** — `{id, author, created, replaces, body}` where `id` is the
comment's random token (`^[0-9a-z]{8}$`); `author`/`replaces` are omitted when
empty. The `comments` array (in `detailDTO`) is the **resolved** log: each
`replaces`-chain collapsed to its newest revision, tombstoned comments omitted.

**`detailDTO`** — `issueDTO` plus: `description`, `body_external` (bool, omitted
when false), `parent_ref` (`refDTO`), `blocked_by_refs`, `related_refs`,
`blocks`, `children` (each `refDTO[]`), and `comments` (`commentDTO[]`). Emitted
by `show`. `description` is always the complete body; `body_external` only says
it was read from the content sidecar rather than the `.md`
(TASK-STORAGE-SPEC §4.6). `issueDTO` carries no description, so list-shaped
output is unaffected by body size.

**`blockedDTO`** — `issueDTO` plus `blocked_by_refs` (`refDTO[]`). Emitted by
`blocked`.

**`whereDTO`** — emitted by `where`. `kind` is one of `local` | `central` |
`override_name` | `none` (mirrors the engine's `ResolveKind`, SDK-SPEC §1).
`store_path` and `project_path` are omitted when `kind` is `none`, and `store` for a
local store:

```json
{ "kind": "central", "store": "my-project",
  "store_path": "/home/you/.taskmgr/stores/my-project",
  "project_path": "/home/you/dev/my-project" }
```

**`storeListDTO`** — emitted by `store list`, one per registry entry:
`{path, store, store_path, health}` (the project path, the registry name, the resolved
store directory, and `ok` | `dangling` | `broken` — §2.1). `health` is always present;
it mirrors the engine's `StoreHealth` (SDK-SPEC §1).

**`storeMoveDTO`** — emitted by `store move`, describing the store after the move:
`{store, store_path, project_path}` (the registry name, the store directory, and the
project it now tracks).

**`configKeyDTO`** — emitted by `config keys`, one per supported key:
`{key, scope, writable, description}`. `scope` is `store` | `global`. It is the static
catalog, so the same array is returned wherever the command runs.

**`configValueDTO`** — emitted by `config get`, `config set` and `config unset`:
`{key, value, writable}`. `value` is the empty string for an unset key (and after
`unset`).

**`configListDTO`** — emitted by `config list`: `{scope, path, keys}` where `keys` is a
`configValueDTO[]` and `path` is the file the values came from.

**`packageDTO`** — emitted by `package add` (the one entry) and `package list` (an
array): `{name, path, scope, status, detail, hooks, guide, shadowed}`. `name` is the
package name — the `name:` given, or the base name of `path:`; `path` is the directory it
resolves to on this machine; `scope` is `store` | `global`; `status` is `ok` | `missing`
| `broken` (§2.3). `detail` explains a status that is not `ok` and is omitted otherwise,
`hooks` and `guide` count the hooks and the guide fragments the package contributes
(HOOK-SPEC §3.7), and `shadowed` marks an entry whose name an earlier one already
took (HOOK-SPEC §3.5).

**`guideTopicDTO`** — emitted by `guide --list` (an array):
`{id, kind, summary, package, scope, detail}`. `kind` is `core` for a section the
binary owns and `package` for one a package contributes; `package` and `scope` name
where a `package` row came from and are omitted for a `core` one; `detail` explains a
fragment that could not be read. The rows are in print order, so the roster and the
guide agree about sequence.

**`edgeResultDTO`** — emitted by `dep add`, `dep rm`, `rel add` and `rel rm`:
`{op, from, to}`. `op` is one of `dep_add` | `dep_remove` | `rel_add` |
`rel_remove`, and `from`/`to` name the two issues: a dependency reads from the
dependent to its blocker, a related link from the side the edge is stored on to
its peer.

**`commentDeleteDTO`** — emitted by `comment rm`: `{op, issue, comment_id}` with
`op` always `comment_delete`.

The `op` values of both are the engine's own log operation names
([MONITORING.md](../MONITORING.md)), so one operation carries one name in the
JSON and in the records. These five shapes were undocumented and untested until
v0.9.0, and printed three different key vocabularies — `dependent`/`blocker` for
one pair, `a`/`b` for the other, and an `op` of `add`/`rm` that did not match the
log. Nothing held those keys in place, so a rename during ordinary work would
have changed released output with no gate to catch it.

**`hookDTO`** — emitted by `hook list` (an array): `{id, event, when, run, package,
scope}`. `id` is the **effective** id `pkg:<package>:<hook>` (HOOK-SPEC §3.2), so it is
the value a `hook_denied` error reports and the logs record. `when` is omitted when
empty. The array is in run order.

**Hook output ([HOOK-SPEC.md](HOOK-SPEC.md) §6.2).** A mutation that runs hooks surfaces
their output alongside the normal result. On success the JSON carries optional
`"hints": [string]` (advisory notes from any hook that ran) and `"warnings": [string]`
(post-hook failures, which never fail the write). This holds for **every** mutation
that runs hooks, `import --run-hooks` included, in both its single-envelope and
`--batch` forms — `--json` is the contract for the adapter driving an import, and
a post-hook that failed on an imported issue must not leave exit 0 and a result
object with no trace of it. A pre-hook **denial** exits non-zero and
prints a structured error:

```json
{ "error": "hook_denied", "event": "pre-close", "hook": "pkg:repo-policy:tests-before-close",
  "issue_id": "proj-0042", "exit": 1, "reason": "3 unit tests failing",
  "hints": ["run `make fmt` before retrying"] }
```

---

## 7. Command summary

```
taskmgr init     [--prefix X] [--central [--store-name N]]
taskmgr where                                # which store resolves here, and why
taskmgr store    list                        # enumerate central registry entries
taskmgr store    move --central [--to N]     # promote the local store here to central
                 move --rename  --to N       # rename the central store here
                 move --relink  --to N       # entry N now tracks this directory
taskmgr config   keys                        # supported keys, both scopes
                 list | get K | set K V | unset K          [--global]
taskmgr package  add <name> [--path]                       [--global]
                 list                                      [--global]
taskmgr hook     list                        # the effective chain, in run order
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
taskmgr guide    [topic...] [--list]         # workflow how-to (start here)

Global: --json, -C/--dir <path>, --store-name <name>
Env:    TASKMGR_HOME, TASKMGR_LOG
```
