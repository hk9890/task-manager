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
- **Logs are not output.** Logs go to stderr (or a configured sink), never mixed into the
  JSON a command prints to stdout — a machine consumer parsing `--json` is never polluted.

## What is logged

| Event | Level | Key fields |
|---|---|---|
| Write committed (`Create`/`Update`/`Close`/`Reopen`/`Import`) | debug | op, issue id, transition |
| Validation / referential rejection | info | op, issue id, field, reason |
| Lock wait beyond a threshold | warn | waited_ms |
| Hook invoked | debug | event, hook id, issue id, decision, **duration_ms** |
| Hook denied a transition | info | event, hook id, issue id, exit, reason |
| Hook error (missing / timeout / signal) | warn | event, hook id, issue id, category, duration_ms |
| Store / IO error | error | op, path, error |

## Hook timing

Pre-hooks run **inside** the store write lock ([HOOK-SPEC](../specs/HOOK-SPEC.md) §8), so a
slow gate serializes every other writer for its duration. To make that cost visible rather
than mysterious, **every hook invocation is logged with its wall-clock `duration_ms`**,
alongside its event, `id`, issue id, and decision. This is the signal a project uses to
answer "how long are my close gates holding the lock?" and to decide whether to raise
`hook_timeout`, move a check to a post-hook, or push it to CI. A hook that exceeds
`hook_timeout` or errors is logged at `warn` with the same timing fields, so the timeout is
never silent.

## Format & configuration

- **Format.** Structured key/value (Go `log/slog`); text for humans by default, JSON when a
  machine sink is configured.
- **Destination.** stderr by default; redirectable.
- **Level.** Settable via the environment (e.g. `TASKMGR_LOG=debug`); default `warn`.

These are environment-controlled and need no entry in the store's `config.yaml`.

## Non-goals

- **No metrics / TSDB / tracing backend.** No Prometheus endpoint, no OpenTelemetry, no
  spans. Aggregate logs if you need a number.
- **No always-on telemetry or phone-home.** Nothing leaves the machine.
- **No audit log as a feature.** The git history of `.tasks/` already records every
  committed change; logs are for diagnosis, not a second source of truth.
