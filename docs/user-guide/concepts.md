# Concepts

What an issue is, how issues relate, and what "ready" means. Read this once and the rest of
the commands explain themselves.

## An issue

Every issue carries a type, a status, a numeric priority, and a Markdown body.

| Field | Values |
|---|---|
| type | `task` (default) · `bug` · `feature` · `epic` · `chore` · `doc` |
| status | `open` · `in_progress` · `blocked` · `deferred` · `closed` |
| priority | `0` critical · `1` high · `2` normal (default) · `3` low · `4` trivial |

`taskmgr types` and `taskmgr statuses` print the live lists; `taskmgr labels` prints the
labels actually in use in your store. Labels are free-form (`area:db`, `kind:design`) and
are how you add any dimension the fixed fields do not cover — the type and status sets are
deliberately closed.

**IDs are opaque.** `proj-3k9f2x` is a prefix plus a random token, so two people working on
separate branches never allocate the same ID and a merge never renumbers anything. An ID
never changes once assigned.

## The description body

Each issue has exactly one Markdown body. Acceptance criteria, reproduction steps and
context all go there — there is no separate field for them.

```bash
taskmgr update <id> --description-file - <<'EOF'
## Acceptance criteria
- [ ] UTF-8 with BOM
- [ ] RFC 4180 quoting
EOF
```

`--description "..."` takes a single inline string; for anything multi-line use
`--description-file <path>`, or `-` to read standard input as above. `\n` inside
`--description` is stored literally, not as a newline.

`update --description` **replaces** the body. To amend one, read it back with
`taskmgr show <id> --json` and resubmit the full modified text.

A body of any size is accepted. A very large one is stored in a file beside the issue
rather than inside it; `show` truncates it on screen and says so, while `--json` always
returns the whole thing.

## Comments

`taskmgr comment add <id> "..."` appends to an issue's comment log. Comments are
append-only: an edit adds a revision that supersedes the earlier text, and a delete adds a
tombstone. The original stays in the file as history while `show` renders only what is
current — so nothing is ever silently rewritten, and two branches that both commented
merge without conflict.

Comments keep working after an issue is closed, which is where post-close verification
notes belong.

## The three kinds of link

| Link | Meaning |
|---|---|
| `parent` | Grouping. One parent per issue; an epic is just an issue others name as their parent. |
| `blocked_by` | A hard dependency. The dependent is not workable until every blocker is closed. Cycles are rejected. |
| `related` | A soft, non-blocking reference. Symmetric: set it from one side and it shows on both. |

```bash
taskmgr dep add <dependent> <blocker>   # dependent is now blocked by blocker
taskmgr dep rm <dependent> <blocker>
taskmgr rel add <a> <b>                 # symmetric; rel rm clears both sides
```

Only one direction of each link is stored. The reverse — an issue's children, what it
blocks, the other half of a `related` pair — is computed when you read it, so the graph
can never contradict itself. `taskmgr show` displays both directions.

## Ready and blocked

Two views are derived from the dependency graph:

- **`taskmgr ready`** — open issues whose every blocker is closed. What you can start now,
  ordered by priority, then oldest first.
- **`taskmgr blocked`** — non-closed issues with at least one open blocker, each listed
  with what is holding it.

**These come from the graph, not from the `status` field.** The `blocked` *status* is a
label you set by hand and nothing ever clears: an issue can carry `status: blocked` with
no open blocker at all, or sit in `taskmgr blocked` while its status is `open`. If you
want the computed answer, use `ready` / `blocked` — the commands, or the predicates of the
same name in a filter ([Filtering and search](queries.md)).

Epics appear in `ready` like anything else. Add `-q 'type != epic'` when you want leaf work
only.

## Documents

`--type doc` stores a document *as an issue*: a design page, a session note, a handover, a
review. It gets an ID, a body, labels, `related` edges and the same commands as everything
else. It differs in exactly one way — **a document is not work**, so it never appears in
`ready` or `blocked`.

```bash
taskmgr create --title "Export format decision" --type doc \
    --label kind:design --description-file decision.md
taskmgr rel add <doc-id> <task-id>
```

Which *kind* of document it is goes in a label (`kind:design`, `kind:session`,
`kind:handover`), not in the type — so a new kind costs nothing. Documents stay fully
visible to `list`, `search` and every filter.

## What is on disk

```
.tasks/
├── config.yaml         the ID prefix, and any hooks
├── proj-3k9f2x.md      one active issue: YAML frontmatter, then the Markdown body
├── comments/           one append-only comment log per issue
├── content/            oversized bodies, kept out of the issue files
└── closed/             closed issues
```

Everything is UTF-8 text you can read, grep and diff. Closing an issue **moves** its file
into `closed/`, which git records as a rename, so history survives.

Two things follow from this that are worth knowing:

- **Merge conflicts are per-issue.** Two branches editing different issues touch different
  files. There is no shared index or counter file to collide on.
- **`taskmgr` should be the only writer.** It validates every field and takes a lock before
  writing, so nothing malformed or half-written lands. Hand-editing a file is possible —
  they are just files — but you own the consequences, and an ID that no longer matches its
  filename will not load.
