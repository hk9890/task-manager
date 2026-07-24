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
| `write` — a committed mutation | debug | `op` (the transition), `issue` |
| `hook` — a hook that **allowed** | debug | `event`, `hook`, `issue`, `decision=allow`, **`duration_ms`** |
| `hook` — a hook that **denied** | info | `event`, `hook`, `issue`, `decision=deny`, `duration_ms` |
| `hook` — a hook that **errored** (missing / timeout / signal) | warn | `event`, `hook`, `issue`, `decision=error`, `duration_ms` |
| `io_error` — a failed store write | error | `op`, `issue`, `error` |

Every hook invocation emits one `hook` record regardless of outcome — only the
level and `decision` differ — so allow/deny/error are one query away from each other.

## Hook timing

Pre-hooks run **inside** the store write lock ([HOOK-SPEC](specs/HOOK-SPEC.md) §8), so a
slow gate serializes every other writer for its duration. To make that cost visible rather
than mysterious, **every hook invocation is logged with its wall-clock `duration_ms`**,
alongside its event, `id`, issue id, and decision. This is the signal a project uses to
answer "how long are my close gates holding the lock?" and to decide whether to raise
`hook_timeout`, move a check to a post-hook, or push it to CI. A hook that exceeds
`hook_timeout` or errors is logged at `warn` with the same timing fields, so the timeout is
never silent.

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
grep 'msg=hook' run.log |
  sed -n 's/.*hook=\([^ ]*\).*duration_ms=\([0-9]*\).*/\2 \1/p' |
  sort -rn | head

# Deny/error rate across a batch run.
grep -c 'decision=deny'  run.log
grep -c 'decision=error' run.log
```

## Non-goals

- **No metrics / TSDB / tracing backend.** No Prometheus endpoint, no OpenTelemetry, no
  spans. Aggregate logs if you need a number.
- **No always-on telemetry or phone-home.** Nothing leaves the machine.
- **No audit log as a feature.** The git history of `.tasks/` already records every
  committed change; logs are for diagnosis, not a second source of truth.
