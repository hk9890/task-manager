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
| `store_busy` — a write of a store opened `WithLockTimeout` gave up on a held write lock, and wrote nothing | warn | `store`, `waited_ms` |
| `publish_stage` — a publish or init stage **starts** | debug | `stage` |
| `publish_git` — a git command ended with exit `0`, or with an exit-`1` answer | debug | `cmd`, `exit`, `duration_ms` |
| `publish_git` — any other git outcome: another exit, not started, timed out, killed | warn | `cmd`, `exit` (`-1` when git did not exit on its own), `duration_ms`, `stderr`, `error` (when git did not exit) |
| `publish_lock_wait` — a busy store held the publish back for 5s | warn | `store`, `waited_ms` |
| `publish_error` — a busy store held the publish back for 2 minutes, and it gave up | error | `stage=commit`, `error` (names the store, or the central-root lock) |
| `publish` — a publish succeeded | debug | `machine`, `branch`, `committed`, `pushed`, `commit`, `retries`, `lock_wait_ms`, `duration_ms` |
| `publish_init` — an init succeeded | debug | `machine`, `branch`, `remote`, `duration_ms` |
| `publish_error` — a publish or init failed | error | `stage`, `error`; once this run committed, or at `stage=push`, also `committed` and `head` (the commit not yet pushed) |
| `publish_error` — refused because another publish runs | info | `stage=lock`, `error` |

Every hook invocation emits one `hook` record regardless of outcome — only the
level and `decision` differ — so allow/deny/warn/error are one query away from
each other.

A `hook` value is the effective id `pkg:<package>:<hook>`
([HOOK-SPEC](specs/HOOK-SPEC.md) §3.2), so the middle segment names the package the gate
came from. Two packages may each declare a hook called `doc-needs-path` — group on the
whole value, never on the last segment. `taskmgr hook list` maps a package back to the
config file that named it.

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

A **validation refusal is not an `io_error`.** The write is rejected before a file is
touched, so counting it here would fire an alert built on "the store failed to write" on
what is a rejected input. The caller still gets the `*ValidationError`
([SDK-SPEC](specs/SDK-SPEC.md) §6); a refused mutation is visible as the absent `write`
record, not as an error one.

`op` names the **operation**, never the file. A write that overflows a large body
touches two files — the task `.md` and the content sidecar
([TASK-STORAGE-SPEC](specs/TASK-STORAGE-SPEC.md) §4.6) — and a failure of either
emits one `io_error` under the operation's own `op`, because the sidecar write
happens inside the same gated closure. So a failed content write during a
dependency edit logs `op=dep_add`, not a storage-layer name. An `io_error` means
the mutation did not land: a body already in the sidecar is replaced only by a
rename performed after the `.md` is committed, so a failure anywhere before that
leaves the issue's previous body intact and readable
([TASK-STORAGE-SPEC](specs/TASK-STORAGE-SPEC.md) §4.6 rule 4).

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

## Publishing

`taskmgr sync` and `sync init` (SDK `Publish`, `InitPublish`) log through the same
logger. A sync runs unattended, from a timer, so its records are how a failed or hung run
is diagnosed after the fact.

- **`publish_stage` is logged when a stage starts.** Publish stages, in order: `config`
  (machine ID, and whether `sync init` has run), `lock` (the publish lock), `check`
  (remote, branch, exclude file),
  `commit` (store locks, add, locks released, commit), `push`. Init stages: `config`, `lock`, `init`. A
  publish with nothing to push logs no `push` stage. The last `publish_stage` of a run
  that never ends names the stage it hangs in.
- **One `publish_git` per git command.** `cmd` is the subcommand (`add`, `diff`, `commit`,
  `push`, `rev-parse`, `config`, `symbolic-ref`, `init`, `remote`, and after a failed push
  `ls-remote` and `merge-base`); `duration_ms` is the process's own wall-clock time.
  Exit `1` from `config --get`, `diff --quiet`, `rev-parse --verify`,
  `symbolic-ref --quiet` and `merge-base --is-ancestor` is an answer — no such key,
  something staged, no such ref, detached, not an ancestor — and logs at debug. A push the
  remote rejects also exits `1`, and is a failure. `stderr` is trimmed and cut at 1024 bytes.
- **`publish_lock_wait` is logged once per publish**, when its back-off on busy store
  locks reaches 5s. `store` names the store that was busy at that try, or
  `(central root)` when the registry lock was. `retries` and `lock_wait_ms` on the
  `publish` record carry the whole wait. A back-off that reaches 2 minutes gives up with
  a `publish_error` at `stage=commit` whose `error` names the busy store and the wait.
- **`publish_error` is error, except for a busy publish lock, which is info.** A timer
  that fires during a manual sync expects to be refused, and the next tick publishes.
  Count real failures with `level=ERROR`, never with `msg=publish_error` alone.
- **No credentials.** A remote URL may carry a token (`https://user:token@host/…`). Every
  `remote`, `stderr` and `error` value has the user and password removed from each URL
  with a scheme in it.

**Reading a hung or failed sync.** Capture it at debug, as below. Then:

- **Killed:** a run that ended has a last record of `publish`, `publish_init` or
  `publish_error`. A log with none of them is a process that was killed, by a Ctrl-C or
  a signal, at its last `publish_stage`. On Linux the git it ran, the push included, was
  killed with it, whatever the signal; after a signal other than Ctrl-C, git's transport
  (an `ssh` or `git-remote-https` process) can still run until its connection gives up.
  On macOS only a Ctrl-C at the terminal ends git
  too; a `taskmgr` killed alone leaves a push running until git gives up on the
  connection.
- **Hung:** the last `publish_stage` is where it is. A `push` stage lasts at most 10
  minutes plus the seam's 2s kill grace; then the push is killed and logs
  `publish_git cmd=push exit=-1` with `error`. An HTTPS push that moves under 1000
  bytes/s for 60 seconds ends sooner: git aborts it itself, and `publish_git cmd=push`
  logs git's exit status and its `stderr`. An ssh push has only the 10 minutes. The
  limits and why are SDK-SPEC's
  ([§ Publishing the central stores](specs/SDK-SPEC.md#publishing-the-central-stores)).
  A hang in `commit` with
  `publish_lock_wait` names the store a long write or a slow pre-hook holds
  ([Hook timing](#hook-timing)); it lasts at most 2 minutes, then ends in
  `publish_error`.
- **Failed:** the `publish_error` record names the stage; the last `publish_git` warn of
  the failed command carries git's `stderr`. After a failed push, read the one with
  `cmd=push`: the `ls-remote` that asks the remote for its tip comes after it and can
  fail too, with the same cause. A failure after the commit, or in the push, keeps
  the commit local: `head` names the commit that is not on the remote yet (empty when
  the failure came before it was read), and `committed` says whether this run made it.
  The next sync tries the push again, and succeeds once what the error names is fixed.

```bash
TASKMGR_LOG=debug taskmgr sync 2> sync.log
grep 'msg=publish_stage' sync.log | tail -1            # where it is, or where it stopped
grep -E 'msg=publish_(git|error)' sync.log | grep -v 'level=DEBUG'  # what failed, with git's stderr
grep 'msg=publish ' sync.log                           # the summary of a run that succeeded
```

## Format & configuration

For the CLI:

- **Level.** `TASKMGR_LOG` accepts `debug`, `info`, `warn`, `error`. The default is
  `warn`, and an unrecognised value silently falls back to `warn`.
- **Format.** Always `slog`'s text handler — human-readable key/value. The CLI has no
  JSON log mode.
- **Destination.** Always stderr — the invocation's stderr writer, which is the
  process's for a real run and the caller's buffer under `cmd.Run`, so an in-process
  test can assert on a record. There is no destination setting; redirect at the shell.

This is environment-controlled and needs no entry in the store's `config.yaml`.

SDK embedders pick their own format and sink — `tasks.WithLogger` takes any
`*slog.Logger`:

```go
h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
store, err := tasks.Open("", tasks.WithLogger(slog.New(h)))
res, err := tasks.Publish(tasks.WithLogger(slog.New(h)))   // the publishing records
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
