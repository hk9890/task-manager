# Monitoring & Logging

## Philosophy

We do monitoring through **structured logging** — that is the whole mechanism. There is no
metrics daemon, time-series database, tracing backend, or always-on telemetry. A log line
is a fact about one operation; anything to *measure* (latency, hook cost, error rate) is
derived by aggregating logs after the fact, not by a second runtime system. This matches
the subtractive architecture (ARCHITECTURE-SPEC §10).

- **Off by default, opt-in by level.** A successful CLI run stays silent; logs are emitted
  only at or above a configured level (default: warnings and errors).
- **One mechanism.** The engine and the CLI write through the same structured logger; there
  is no separate metrics path.
- **Logs are not output.** Logs go to stderr, never mixed into the JSON a command prints
  to stdout — a machine consumer parsing `--json` is never polluted.

## What is logged

Each row is one `slog` record (`msg` in the first column):

| `msg` | Level | Key fields |
|---|---|---|
| `write` — a committed lifecycle transition | debug | `op` (the transition), `issue` |
| `hook` — a hook that **allowed** | debug | `event`, `hook`, `issue`, `decision=allow`, **`duration_ms`** |
| `hook` — a **pre**-hook that **denied**, blocking the write | info | `event`, `hook`, `issue`, `decision=deny`, `duration_ms` |
| `hook` — a **post**-hook that **reported a failure** | info | `event`, `hook`, `issue`, `decision=warn`, `duration_ms` |
| `hook` — a hook that **errored** (missing / timeout / signal) | warn | `event`, `hook`, `issue`, `decision=error`, `duration_ms` |
| `io_error` — a failed store write | error | `op`, `issue`, `error` |

Every hook invocation emits one `hook` record regardless of outcome — only the
level and `decision` differ — so allow/deny/warn/error are one query away from
each other.

### What `write` covers

`write` is keyed on the **lifecycle transition** ([HOOK-SPEC](specs/HOOK-SPEC.md)
§2.1), so `op` is always one of `create`, `update`, `close`, `reopen`. An import
is a create and logs as one.

Everything else that writes to the store — comment add/edit/delete, dependency
and related-link edits — is **not** a transition: it fires no hooks and emits no
`write` record. This is deliberate (those writes have no gate to measure), but it
means **`write` is not a complete audit of what changed on disk**. Use the git
history of `.tasks/` for that; it already records every committed change.

A *failed* write is logged in every case, transition or not: `io_error` carries
`op=create|update|close|reopen` for a transition and
`op=comment_add|comment_edit|comment_delete|dep_add|dep_remove|rel_add|rel_remove`
otherwise. Nothing fails silently.

`op` names the **operation**, never the file. A write that overflows a large body
touches two files — the task `.md` and the content sidecar
([TASK-STORAGE-SPEC](specs/TASK-STORAGE-SPEC.md) §4.6) — and a failure of either
emits one `io_error` under the operation's own `op`, because the sidecar write
happens inside the same gated closure. So a failed content write during a
dependency edit logs `op=dep_add`, not a storage-layer name. The sidecar is
written before the `.md`, so such a failure leaves the issue's previous body
intact and readable.

### `deny` vs `warn` vs `error`

The three non-allow outcomes answer different questions, and conflating them
corrupts the rates below:

- **`deny`** — a pre-hook refused and **the write did not happen**. Only pre-hooks
  can deny.
- **`warn`** — a post-hook exited non-zero. The write **already committed** and
  cannot be undone ([HOOK-SPEC](specs/HOOK-SPEC.md) §7); the message surfaces as a
  warning on the result. Nothing was blocked.
- **`error`** — the hook itself misbehaved (missing binary, timeout, signal),
  in either phase. For a pre-hook this also blocks the write (fail-closed).

## Hook timing

Pre-hooks run **inside** the store write lock ([HOOK-SPEC](specs/HOOK-SPEC.md) §8), so a
slow gate serializes every other writer for its duration. To make that cost visible rather
than mysterious, **every hook invocation is logged with its wall-clock `duration_ms`**,
alongside its event, `id`, issue id, and decision. This is the signal a project uses to
answer "how long are my close gates holding the lock?" and to decide whether to raise
`hook_timeout`, move a check to a post-hook, or push it to CI. A hook that exceeds
`hook_timeout` or errors is logged at `warn` with the same timing fields, so the timeout is
never silent.

**Only pre-hooks hold the lock.** Post-hooks run after the write, outside it
(HOOK-SPEC §4), so a slow post-hook costs the caller latency but blocks no other
writer. Any query about lock cost must filter on `event=pre-` — a ranking over
all `duration_ms` values mixes in hooks that hold nothing.

**The ceiling is `hook_timeout` + 2s, not `hook_timeout`.** A hook that ignores
SIGTERM is SIGKILLed after a fixed 2s grace (HOOK-SPEC §7), and that grace is
additive: with the 2s default, a hanging pre-close hook holds the lock for ~4s,
and `duration_ms` will report it. Size `hook_timeout` accordingly.

## Format & configuration

For the CLI:

- **Level.** `TASKMGR_LOG` accepts `debug`, `info`, `warn`, `error`. The default is
  `warn`, and an unrecognised value silently falls back to `warn`.
- **Format.** Always `slog`'s text handler — human-readable key/value. The CLI has no
  JSON log mode.
- **Destination.** Always stderr. There is no destination setting; redirect at the
  shell.

This is environment-controlled and needs no entry in the store's `config.yaml`.

SDK embedders pick their own format and sink — `tasks.WithLogger` takes any
`*slog.Logger`:

```go
h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
store, err := tasks.Open("", tasks.WithLogger(slog.New(h)))
```

## Capturing and aggregating

Measuring means capturing stderr and aggregating the fields above:

```bash
# Capture a debug-level run; stdout (--json) stays clean.
TASKMGR_LOG=debug taskmgr close <id> --reason done 2> run.log

# Which hooks are holding the write lock, slowest first?
# Filter to pre-hooks: post-hooks run outside the lock and would otherwise rank
# in a list that claims to be about lock cost.
grep 'msg=hook' run.log | grep 'event=pre-' |
  sed -n 's/.*hook=\([^ ]*\).*duration_ms=\([0-9]*\).*/\2 \1/p' |
  sort -rn | head

# How many writes were actually blocked, and how many hooks broke?
grep -c 'decision=deny'  run.log   # pre-hook refusals — these blocked a write
grep -c 'decision=error' run.log   # missing binary / timeout / signal
grep -c 'decision=warn'  run.log   # post-hook failures — nothing was blocked
```

`decision=deny` counts blocked writes only. Counting `deny` and `warn` together
inflates the deny rate with post-hook failures, which by definition arrive too
late to block anything.

## Non-goals

- **No metrics / TSDB / tracing backend.** No Prometheus endpoint, no OpenTelemetry, no
  spans. Aggregate logs if you need a number.
- **No always-on telemetry or phone-home.** Nothing leaves the machine.
- **No audit log as a feature.** The git history of `.tasks/` already records every
  committed change; logs are for diagnosis, not a second source of truth.
