# Troubleshooting

Every error goes to stderr prefixed `taskmgr: `, leaves stdout empty, and exits `1`. This
page is the ones worth explaining.

## `no .tasks directory found — run 'taskmgr init' to create one`

Nothing resolved for this directory: no `.tasks/` at or above it, and no registry entry
covering it.

```bash
taskmgr where     # says what it looked at; never fails, never writes
```

If you expected a central store, `taskmgr store list` shows which project paths are
registered — you are probably standing outside the one that is mapped, or the project has
moved (`taskmgr store move --relink --to <name>` re-points it).

## `issue not found: proj-xxxxxx`

The ID does not exist in this store. Three usual causes:

- **It was invented.** IDs are random, never sequential; find one with `list` or `search`.
- **It belongs to another store.** Check `taskmgr where` — the prefix of your IDs and the
  store's prefix have to match.
- **You are looking at closed work.** `show` finds closed issues fine, but `list` and
  `search` exclude them unless you pass `--all`.

## `parse error at byte 7: unexpected character '='`

A filter expression is malformed. The byte offset points at the token that broke it. The
three common ones:

- `=` instead of `==`.
- An unquoted multi-word value: write `text ~ "drill nav"`, not `text ~ drill nav`.
- An operator the field does not take — `text` only accepts `~`, and `status` only `==`
  and `!=`. [Filtering and search](queries.md) has the table.

An expression that parses but matches nothing is not an error; it prints an empty list.

## `TASKMGR_DIR is set … but is no longer supported`

The variable used to point at a store directory and no longer does. It is rejected rather
than ignored on purpose: left unread, a shell profile or CI job that exported it would keep
exiting `0` while every write landed in whichever store the walk-up found. Unset it, and
either run from inside the project or register the store centrally and select it with
`--store-name`.

## A command is refused by a hook

```
taskmgr: pre-close denied for proj-3k9f2x by hook "tests-before-close": 3 unit tests failing
```

The project has a gate ([Hooks](hooks.md)). The message after the colon is the hook's own
reason — fix that, then retry. There is no bypass flag by design.

If **every** write is failing with a configuration error instead, the `hooks:` block in
`.tasks/config.yaml` is malformed — an unknown `event`, an empty `run`, an unparseable
`when` or `hook_timeout`. Reads keep working, so you can still `list` and `show` while you
fix it.

If a write simply hangs, a pre-hook is running and holding the store lock. It is bounded by
`hook_timeout` (default `2s`, plus a 2-second grace).

## `unknown command "redy" for "taskmgr"`

Followed by a `Did you mean this?` list. A command group typed with no subcommand prints
its help and exits `0` instead.

## `taskmgr ready` is empty but I have open issues

`ready` is derived from the dependency graph, not from the status field. An issue is
missing from it if:

- it has an open blocker — `taskmgr blocked` shows what is holding each one;
- its status is not `open` (`in_progress`, `deferred` and `blocked` are all excluded);
- it is a `doc`, which is never work.

`taskmgr list -q 'ready'` is the same set; `taskmgr list` shows everything active.

## An issue vanished from `list`

Closing moves the file into `.tasks/closed/`, and `list` and `search` read only active work
by default. Add `--all`, or ask for it: `taskmgr list -q 'status == "closed"'`.
`taskmgr show <id>` finds it either way, and `taskmgr reopen <id>` brings it back.

## Two commands at once

Writes serialize on a lock over the whole store, so a second writer waits rather than
corrupting anything. Reads never take the lock. If a command seems stuck, look for a hook
(above) or another `taskmgr` process holding it.

## Getting more detail

```bash
TASKMGR_LOG=debug taskmgr close <id> --reason done 2> run.log
```

`debug` records every committed write and every hook invocation with its decision and
duration, on stderr. `--json` output on stdout stays clean, so you can capture both
separately.
