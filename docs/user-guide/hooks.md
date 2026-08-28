# Hooks

A hook is a script `taskmgr` runs when an issue changes state. A **pre-hook** can refuse
the change — "no closing a feature until the tests pass". A **post-hook** reacts after it
has already happened — send a notification. Either can hand back a short note for whoever
made the change.

Hooks are how a project adds its own rules without those rules being built into `taskmgr`.

## Declare one

```bash
taskmgr config hook add --event pre-close --id tests-before-close \
    --when 'type == "feature"' --run make --run test
taskmgr config hook list      # what is configured, in the order it runs
taskmgr config hook rm tests-before-close
```

`--run` is repeatable, one argv element per occurrence, because a hook is executed
**directly — there is no shell**, so there is no quoting rule to guess at. For a pipeline
or a `&&`, ask for a shell: `--run sh --run -c --run 'make lint && make test'`.

`--when` takes the same filter expressions as `taskmgr list -q`
([Filtering and search](queries.md)), evaluated against the issue *as it would be after the
change*. It scopes a hook; it does not decide which event fires.

`--id` is optional: a hook without one is named `<event>#<index>` after its place in the
list, which is the name `hook rm` and every message use. An id you write yourself must not
contain `#`, so that it can never collide with a name given out that way.

`taskmgr config hook --help` has the rest. Every write is validated before a byte lands, so
a malformed hook is refused by the command that wrote it rather than by your next `close`.

What lands on disk is readable, and hand-editing it stays supported:

```yaml
prefix: proj

hook_timeout: 2s                   # max runtime for any single hook. Default 2s.

hooks:
  - id: tests-before-close
    event: pre-close
    when: 'type == "feature"'
    run: ["make", "test"]

  - id: notify
    event: post-close
    run: [".tasks/hooks/notify.sh"]
```

## Which file: the project's, or yours

Hooks live in two places, and every `taskmgr config` command picks between them the same
way — `--global` selects the second:

| File | Applies to | Travels |
|---|---|---|
| the store's `config.yaml` | that project | committed with the repository, so it binds everyone and every agent working in it |
| your `~/.taskmgr/config.yaml` | **every** store on this machine | nowhere — it is yours alone |

Put a rule the data's integrity depends on in the store's file. Keep the per-user file for
personal ergonomics — a reminder, a notification, a local lint — because a colleague and CI
will never see it.

Three consequences of the split:

- **Global hooks run first**, then the project's. First deny still wins, so a machine-wide
  gate is the one that surfaces when both would refuse.
- **A global hook's id is prefixed `global:`** wherever it appears — in a denial, in
  `config hook list`, and in `config hook rm`. That prefix is how a refusal tells you which
  file to edit.
- **Give a global hook an absolute path.** A hook's working directory is always the project
  root, which for a global hook is whichever project it happens to be running in.

A store cannot switch inherited hooks off. The escape hatch is editing the per-user file.
For `hook_timeout` the store's value wins, then the global one, then the 2s default.

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

`old` is `null` for a create. Both carry the issue's fields and its `description`, and
**an empty field is left out** rather than sent empty — `description` included, so a
validator that reads the body has to treat "absent" as "no body" instead of expecting an
empty string.

The same values arrive as environment variables for convenience — `TASKMGR_HOOK_EVENT`,
`TASKMGR_HOOK_ID`, `TASKMGR_ISSUE_ID`, `TASKMGR_STORE` (the absolute path to the store)
and `TASKMGR_PAYLOAD_SCHEMA` — and the working directory is always the project root.

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
  one, remove it with `taskmgr config hook rm <id>` — adding `--global` when the id is
  prefixed `global:`.
- **A broken `hooks:` block blocks every write** until you fix it, with a clear
  configuration error. Reads are never affected, so you can still `list` and `show` your
  way out. A broken block in the per-user file blocks writes in *every* project on the
  machine.
- **Never run a `taskmgr` mutation from a hook.** A pre-hook would deadlock against the
  lock it is already holding, and a post-hook would trigger further hooks. Read-only
  commands are fine — the example above is one.
- **Post-hooks cannot undo anything.** The write has already committed; a failure is
  reported as a warning and nothing rolls back.
- Bulk `taskmgr import` skips hooks by default, so loading a hundred issues does not fire a
  hundred gates. Pass `--run-hooks` when you want them.

Every hook run is logged with its event, id, decision and how long it took. Set
`TASKMGR_LOG=debug` and read stderr to see where the time goes.
