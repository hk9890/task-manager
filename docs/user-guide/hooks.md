# Hooks

A hook is a script `taskmgr` runs when an issue changes state. A **pre-hook** can refuse
the change — "no closing a feature until the tests pass". A **post-hook** reacts after it
has already happened — send a notification. Either can hand back a short note for whoever
made the change.

Hooks are how a project adds its own rules without those rules being built into `taskmgr`.

## Declare one

Hooks live in the store's `config.yaml`, so they are committed with the project and apply
to everyone — and every agent — working in it.

```yaml
prefix: proj

hook_timeout: 2s                   # max runtime for any single hook. Default 2s.

hooks:
  - id: tests-before-close         # optional label; shown in messages
    event: pre-close               # required
    when: 'type == "feature"'      # optional filter; default: always
    run: ["make", "test"]          # required: a command and its arguments

  - id: notify
    event: post-close
    run: [".tasks/hooks/notify.sh"]
```

`run` is executed **directly — there is no shell**. For a pipeline or a `&&`, ask for one:
`["sh", "-c", "make lint && make test"]`.

`when` takes the same filter expressions as `taskmgr list -q`
([Filtering and search](queries.md)), evaluated against the issue *as it would be after the
change*. It scopes a hook; it does not decide which event fires.

## The eight events

Each change fires one pre-event before the write and one post-event after it:

| The change | Pre-event | Post-event |
|---|---|---|
| a new issue | `pre-create` | `post-create` |
| an edit that does not open or close it | `pre-update` | `post-update` |
| it becomes closed | `pre-close` | `post-close` |
| it stops being closed | `pre-reopen` | `post-reopen` |

**Events are keyed on what happened, not on which command you typed.** `taskmgr close` and
`taskmgr update --status closed` both fire `pre-close`, so a gate cannot be sidestepped by
picking a different verb. An edit that also closes the issue is a close, and the hook sees
the complete proposed issue including the other changes.

A change that would alter nothing writes nothing and fires nothing.

## What a hook receives, and what it says back

**In:** one JSON object on standard input, then the stream closes.

```json
{
  "schema": 1,
  "event": "pre-close",
  "issue_id": "proj-3k9f2x",
  "old": { "…the issue before…" },
  "new": { "…the issue as it would be written…" }
}
```

`old` is `null` for a create. Both carry the issue's fields plus its `description`. The
same values arrive as environment variables for convenience — `TASKMGR_HOOK_EVENT`,
`TASKMGR_HOOK_ID`, `TASKMGR_ISSUE_ID`, `TASKMGR_STORE` (the absolute path to the store) —
and the working directory is always the project root.

**Out:** the exit code is the decision, and the message is stdout, or stderr if stdout is
empty.

| Exit | A pre-hook | A post-hook |
|---|---|---|
| `0` | allow — any message is a **hint**, passed along as advice | ok, same |
| `1`–`125` | **deny** — the message is the reason shown to the caller | a warning; the change already happened |
| anything else | the hook itself broke (missing, killed, timed out) — treated as a deny | a warning |

A hook never edits the issue. It decides, or it reports. If a gate wants a label added, it
denies and says so, and the caller adds it and retries — which is exactly the loop an agent
can follow on its own.

## A worked example

Refuse to close an epic that still has open children:

```sh
#!/bin/sh
# .tasks/hooks/epic-children.sh — pre-close
open=$(taskmgr -C "$TASKMGR_STORE/.." list --json \
  -q "parent == \"$TASKMGR_ISSUE_ID\" && status != \"closed\"" | jq length)
[ "$open" -eq 0 ] || { echo "epic still has $open open children" >&2; exit 1; }
```

```yaml
hooks:
  - id: epic-children
    event: pre-close
    when: 'type == "epic"'
    run: [".tasks/hooks/epic-children.sh"]
```

A denial reaches the caller as a message and exit code `1`, and as a structured error under
`--json`:

```json
{ "error": "hook_denied", "event": "pre-close", "hook": "epic-children",
  "issue_id": "proj-3k9f2x", "exit": 1, "reason": "epic still has 2 open children" }
```

Hooks run in the order they are listed. The first pre-hook to deny stops the chain and
nothing is written; hints from the ones that already ran are still passed along.

## Rules worth knowing before you write one

- **A pre-hook holds the store's write lock while it runs.** Every other `taskmgr` command
  that writes to this store waits. `hook_timeout` (default `2s`) bounds that wait — plus a
  fixed 2-second grace for a hook that ignores the first signal, so the real ceiling is
  ~4s at the default. Raising it to run a test suite on close serializes all writes for
  that long; a slow check is usually better as a post-hook, or left to CI.
- **Pre-hooks fail closed.** A missing script, a bad command, a timeout — all deny. This is
  the point: a gate you can skip is not a gate, and **there is no bypass flag**. To relax
  one, edit `config.yaml`.
- **A broken `hooks:` block blocks every write** until you fix it, with a clear
  configuration error. Reads are never affected, so you can still `list` and `show` your
  way out.
- **Never run a `taskmgr` mutation from a hook.** A pre-hook would deadlock against the
  lock it is already holding, and a post-hook would trigger further hooks. Read-only
  commands are fine — the example above is one.
- **Post-hooks cannot undo anything.** The write has already committed; a failure is
  reported as a warning and nothing rolls back.
- Bulk `taskmgr import` skips hooks by default, so loading a hundred issues does not fire a
  hundred gates. Pass `--run-hooks` when you want them.

Every hook run is logged with its event, id, decision and how long it took. Set
`TASKMGR_LOG=debug` and read stderr to see where the time goes.
