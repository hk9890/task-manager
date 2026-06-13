# Hook Specification — lifecycle gates

> **Status: Proposed — not yet implemented.** This is the design contract to build
> against, not current behaviour. Until it ships, the other specs in `specs/` are the
> authoritative description of the system.

**Hooks** are scripts the engine runs at issue state transitions. A **pre-hook** can
*block* a transition — refuse to close a task until tests pass. A **post-hook** *reacts*
to one after it commits — send a notification. Either kind may also hand back a short
**hint** for the agent that triggered it.

Hooks keep policy out of the core. The engine knows only issues, dependencies, and
ready-work (ARCHITECTURE-SPEC.md §1); rules like "tests must pass before close" or
"a feature needs a Definition-of-Done section" are hooks, declared per-repository in
`config.yaml`.

The contract is uniform:

- the **engine** classifies the transition and fires the matching named event
  (`pre-close`, `post-create`, …) — a script never diffs `old`/`new` to discover what
  happened;
- every event delivers the **same input** — `{event, old, new}` on stdin;
- a hook **decides or reacts; it never edits the issue**. The write path stays the sole
  author of on-disk state (ARCHITECTURE-SPEC.md §7).

---

## 1. Principles

1. **Gate or notify, never mutate.** A pre-hook allows/denies; a post-hook reacts.
   Neither edits the issue — the engine writes exactly what it validated.
2. **Classified by transition, not by command.** `pre-close` fires whenever an issue
   *becomes* closed — `taskmgr close` or `taskmgr update --status closed` alike. A gate
   keyed on a verb would be trivially evaded.
3. **One transition → one pre-event and one post-event.** The most specific event wins;
   a close is a close, never also an update.
4. **Pre fails closed; post cannot fail the operation.** If a pre-hook can't cleanly
   *allow* (missing, not executable, times out), the transition is **denied**. A post-hook
   runs after the write has committed, so its failure is a logged warning, never a rollback.
5. **Deterministic.** For a given event and store state, which hooks run, in what order,
   and the decision are fixed by `config.yaml`.
6. **Engine-level.** Hooks fire inside the `Store` write path, so every front end (the
   `taskmgr` CLI, any future consumer) is gated identically (ARCHITECTURE-SPEC.md §3).

---

## 2. Events and trigger points

Each transition fires up to two events: a **pre-event** before the write (gating) and a
**post-event** after it commits (notification).

| Transition | Pre-event (gates) | Post-event (notifies) | `old` | `new` |
|---|---|---|---|---|
| create a new issue | `pre-create` | `post-create` | `null` | proposed issue |
| modify a live issue (no closed-ness change) | `pre-update` | `post-update` | current | proposed |
| transition **into** `closed` | `pre-close` | `post-close` | current | proposed (`closed`) |
| transition **out of** `closed` | `pre-reopen` | `post-reopen` | current | proposed |

### 2.1 Transition classification (normative)

The engine computes the proposed `new` issue, compares it to `old`, and picks the
**single** transition by this priority:

1. `old` absent → **create**.
2. `old.status != closed` and `new.status == closed` → **close**.
3. `old.status == closed` and `new.status != closed` → **reopen**.
4. otherwise (a live issue changing among non-closed states/fields) → **update**.

Consequences:

- An update that *also* closes (`update --status closed --priority 0`) is a **close**;
  `new` carries *all* the changes, so a close-gate always sees the complete proposed state.
- A **no-op** mutation (nothing would change on disk) writes nothing and fires **nothing**.
- A denied `pre-create` creates no file — the issue simply never comes into existence.

### 2.2 Not hooked in v1

Out of scope, additive later:

- comment add/edit/remove;
- dependency-specific events — `dep add`/`dep rm` and `blocked_by`/`related` changes via
  `update` fire `pre-update`/`post-update` like any other edit, but there is no event
  dedicated to dependency changes.

---

## 3. Configuration

Hooks live in the store's `config.yaml` (TASK-STORAGE-SPEC.md §4.2), so they are
committed with the repository and shared across everyone — and every agent — who works
in it.

```yaml
prefix: dtt

hook_timeout: 2s                  # global: max runtime for ANY single hook. Default 2s.

hooks:
  - id: tests-before-close        # optional label, shown in messages and logs
    event: pre-close              # required — one of the eight events (§2)
    when: 'type == "feature"'     # optional — QUERY-SPEC filter over `new`; default: always
    run: ["make", "test"]         # required — argv, executed directly (no shell)

  - id: require-dod
    event: pre-create
    run: [".tasks/validators/dod.sh"]

  - id: notify-on-close
    event: post-close
    run: [".tasks/hooks/notify.sh"]
```

### 3.1 `hook_timeout` (top-level)

A single, **global** wall-clock limit applied to **every** hook process; there is no
per-hook timeout. Go duration string (`"2s"`, `"5m"`); default **`2s`**; `0` disables it.

The 2-second default suits fast structural validators. **A project that runs a test
suite on close must raise it** (e.g. `hook_timeout: 5m`) — and should weigh the lock
cost in §8. Exceeding the limit is a hook error (§7): a deny for pre-hooks, a warning
for post-hooks.

### 3.2 Hook fields

| Field | Required | Meaning |
|---|---|---|
| `id` | no | Label used in messages, logs, and the structured denial. Defaults to `<event>#<index>`. |
| `event` | **yes** | One of the eight events (§2). Any other value is a config error (§3.4). |
| `when` | no | A QUERY-SPEC.md filter expression. The hook runs only if it matches **`new`** (§3.3). Omitted → always. |
| `run` | **yes** | Non-empty argv array, executed **directly** via `execve` — **no shell**. For shell features use `["sh", "-c", "make lint && make test"]`. |

There is no per-hook `timeout`, `workdir`, or error policy. Timeout is the one global
`hook_timeout`; the working directory is always the **repo root** (the directory that
contains `.tasks/`); fail-closed (§4) is uniform.

### 3.3 `when` semantics

`when` reuses the filter-expression language unchanged (QUERY-SPEC.md) — the same fields,
operators, and grammar as `taskmgr list -q` — evaluated against the **`new`** issue:

- field predicates (`type`, `priority`, `label`, `status`, …) read `new`'s fields;
- the derived `ready` / `blocked` predicates are computed against the store as of the
  moment the hook fires.

`when` reads **only `new`**; there are no `old.`/`new.` qualifiers. It *scopes* a hook, it
does not decide the transition (the event already did). `event: pre-close` +
`when: 'type == "feature"'` reads as "gate the closing of features". A `when` that fails to
parse is a config error (§3.4).

### 3.4 Config validation

The `hooks:` block and `hook_timeout` are validated when the store is opened for a
**write**. If malformed — unknown `event`, empty/missing `run`, unparseable `when` or
`hook_timeout` — **every mutation fails** with a clear configuration error until fixed
(fail-closed config; §1.4). **Reads are never affected.** Unknown keys within a hook
entry are ignored for forward-compatibility (TASK-STORAGE-SPEC.md §4.2).

---

## 4. Execution and the write path

This extends the write path of ARCHITECTURE-SPEC.md §6. Pre-hooks run **inside** the
lock; post-hooks run **after** it is released.

1. **Acquire** the store lock.
2. **Apply** the change in memory → `new`; `old` is the current on-disk issue (or `null`).
3. **Validate** field invariants and referential integrity. *Hooks never run on an issue
   the engine itself would reject.*
4. **Classify** the transition (§2.1) → the pre-event. Select pre-hooks whose `event`
   matches **and** whose `when` matches `new`, in **config order**, and run each
   sequentially (each bounded by `hook_timeout`):
   - collect a **hint** from every hook that allows (§6);
   - the **first** hook that does not cleanly allow (a deny or a hook error) **stops the
     chain** and **aborts** the mutation — release the lock, write nothing, return its
     reason together with any hints collected so far.
5. **Write atomically** (temp + `fsync` + `rename`).
6. **Release** the lock.
7. **Post-hooks.** Select post-hooks for the transition (same `event`/`when` rule) and run
   each sequentially, **outside the lock**, bounded by `hook_timeout`. They are
   **non-vetoing**: the write has already committed, so an exit code or timeout never
   rolls it back — a failure is recorded as a **warning**. Hints are collected as for
   pre-hooks.
8. **Return** success, surfacing all collected hints and any post-hook warnings (§6.2).

Notes:

- **Deny short-circuits; hints aggregate.** Only the *decision* stops early (at the first
  deny). Advisory hints from every hook that ran are gathered and surfaced together.
- **No partial state.** A denied transition (step 4) leaves the store byte-for-byte
  unchanged.
- **"Fire-and-forget" = non-vetoing, not asynchronous.** Post-hooks run synchronously
  after the write so their hints and warnings can be surfaced; they simply cannot change
  the outcome. With the 2-second default the added wait is small.
- **Environment.** Each hook process inherits the parent environment plus:

  | Variable | Value |
  |---|---|
  | `TASKMGR_HOOK_EVENT` | the event, e.g. `pre-close` / `post-close` |
  | `TASKMGR_HOOK_ID` | the hook's `id` |
  | `TASKMGR_ISSUE_ID` | the issue's id |
  | `TASKMGR_STORE` | absolute path to the `.tasks/` directory |
  | `TASKMGR_PAYLOAD_SCHEMA` | the input-payload schema version (§5) |

  The **canonical** input is the JSON on **stdin** (§5); the env vars are conveniences.
  `cwd` is always the repo root.

---

## 5. Input contract (stdin)

The engine writes one JSON object to the hook's **stdin** and closes it:

```json
{
  "schema": 1,
  "event": "pre-close",
  "issue_id": "dtt-0042",
  "old": { "...hook issue object..." },
  "new": { "...hook issue object..." }
}
```

| Field | Type | Notes |
|---|---|---|
| `schema` | int | Payload schema version. `1` for this spec. Additive growth only (§9). |
| `event` | string | The event being fired (§2). |
| `issue_id` | string | The issue's canonical id (equals `new.id`). |
| `old` | object \| null | The issue **before** the change. `null` for create. |
| `new` | object | The issue **as it would be / has been written**. |

### 5.1 The hook issue object

`old` and `new` are the stable issue DTO (CLI-SPEC.md §6 `issueDTO`) **plus** the
`description` body. Empty optional fields are omitted, exactly as in the CLI's JSON output.

```json
{
  "id": "dtt-0042", "title": "Fix drill navigation",
  "status": "closed", "type": "bug", "priority": 1,
  "assignee": "hans", "creator": "hans",
  "labels": ["area:details"],
  "parent": "dtt-0007", "blocked_by": ["dtt-0040"], "related": ["dtt-0012"],
  "created": "2026-06-01T10:00:00Z", "updated": "2026-06-13T09:00:00Z",
  "closed": "2026-06-13T09:00:00Z", "close_reason": "fixed",
  "description": "## Description\nDrilling a related issue should navigate fully."
}
```

**Derived relationships (`blocks`, `children`) are not included** — they need a store
scan and most hooks don't use them. A hook that does can query the store itself (it has
`TASKMGR_STORE` and the CLI on `PATH`), e.g. to refuse closing an epic with open children:

```sh
open_children=$(taskmgr -C "$TASKMGR_STORE/.." list --json \
  -q "parent == \"$TASKMGR_ISSUE_ID\" && !closed" | jq length)
[ "$open_children" -eq 0 ] || { echo "epic has $open_children open children" >&2; exit 1; }
```

### 5.2 Example — `pre-create` (structure validation)

```json
{
  "schema": 1,
  "event": "pre-create",
  "issue_id": "dtt-0050",
  "old": null,
  "new": {
    "id": "dtt-0050", "title": "Add export", "status": "open",
    "type": "feature", "priority": 2, "creator": "hans",
    "created": "2026-06-13T11:00:00Z", "updated": "2026-06-13T11:00:00Z",
    "description": "## Goal\nExport tasks as CSV.\n"
  }
}
```

A DoD validator reads `new.description`, checks for a `## Definition of Done` section with
at least one checklist item, and exits `0` or non-zero accordingly.

---

## 6. Output contract

A hook communicates through its **exit code** (the decision) and its **stdout/stderr**
(a message). It never returns a modified issue.

### 6.1 Decision and message

**Exit code is the single source of truth for the decision** (it matters only for
pre-hooks; a post-hook's code only distinguishes success from a warning):

| Exit code | Meaning |
|---|---|
| `0` | **Allow** (pre) / **OK** (post). |
| `1`–`125` | **Deny** (pre) / **warning** (post). A well-formed refusal. |
| `126`, `127` | **Hook error** — not executable / not found (§7). |
| `128 + N` | **Hook error** — killed by signal `N` (§7). |

The hook's **message** is plain text — its **stdout**, or its **stderr** if stdout is
empty — interpreted by outcome:

- **on allow (exit 0): the message is a hint** — short advice surfaced to the caller
  (e.g. for the LLM that triggered the change: "remember to update CHANGELOG"). Optional.
- **on deny (non-zero): the message is the reason.** If both streams are empty, the
  engine supplies a generic reason naming the hook.

Hooks are **not** expected to emit JSON, and the engine does **not** parse their output as
a structured verdict. There is no mechanism for a hook to write labels or any other field
onto the issue — hooks never change tasks.

### 6.2 Surfacing

- **Hints aggregate.** Every hint from every hook that ran (pre and post) is collected and
  returned together, even when the overall result is *allow*.
- **First deny wins.** The pre-chain stops at the first denying hook (§4); that hook's
  reason is the denial reason. Hints gathered before it are still surfaced.
- **CLI.** On success, hints print as notes (and post-hook warnings, if any). With
  `--json`, the result carries `"hints": [...]` and `"warnings": [...]`. On a pre-deny:
  exit `1`, a `taskmgr: ` message on stderr, and with `--json` a structured error:

  ```json
  { "error": "hook_denied", "event": "pre-close", "hook": "tests-before-close",
    "issue_id": "dtt-0042", "exit": 1,
    "reason": "3 unit tests failing; HEAD not clean",
    "hints": ["run `make fmt` before retrying"] }
  ```

- **SDK.** A typed error on denial (event, hook id, exit code, reason); hints and
  post-hook warnings are returned to the caller on success.

---

## 7. Errors

A **deny** (exit `1`–`125` on a pre-hook) is the gate doing its job. A **hook error** is
the hook itself misbehaving. For pre-hooks both **block the write** (fail-closed); for
post-hooks both are **warnings** (the write already committed). They differ only in the
reported category, for diagnosis.

| Condition | Pre-hook | Post-hook |
|---|---|---|
| Binary missing / not executable (`126`/`127`, spawn failure) | Deny, category `hook_error` | Warning |
| `hook_timeout` exceeded (§3.1) | `SIGTERM`, then `SIGKILL` after a grace period → Deny, `hook_error` | Warning |
| Killed by a signal | Deny, `hook_error` | Warning |

A hook **must not** invoke `taskmgr` *mutations*: a pre-hook runs while the store lock is
held and would deadlock; a post-hook could trigger further hooks. Read-only queries are
fine.

Fail-closed means a **misconfigured pre-hook wedges the relevant mutations** until fixed
(a typo'd `run` blocks all closes). This is intentional — the point of a gate is that it
cannot be skipped. **There is no bypass flag** (§10): to relax or remove a gate you edit
`config.yaml`. Up-front config validation (§3.4) makes the failure a clear config error
rather than a mysterious per-close one.

---

## 8. Concurrency and the lock

Pre-hooks run **inside** the store write lock (§4), after validation and before the atomic
write. It is the simplest fully-correct model: the decision is made against exactly the
state that will be written (no check/use gap), and a denial is atomic.

**The cost:** while a pre-hook runs, the store-wide `flock` is held, so all other writers
block until it returns. With the 2-second default this is negligible; **if you raise
`hook_timeout` to run a test suite on close, you serialize all writes for that duration.**
Post-hooks avoid this by running outside the lock.

> **Future option (not v1): optimistic, out-of-lock pre-hooks.** A slow gate could run
> before taking the lock, then the engine would lock, re-validate, confirm the issue is
> unchanged since the snapshot (else abort for retry), and write — removing the stall at
> the cost of a conflict/retry concept. Deferred: the dominant workflow is a single writer
> per repo, where there is nothing to stall.

---

## 9. Surface and versioning

- **Engine-level.** Hooks fire from the `Store` mutation path, so the CLI and every SDK
  consumer are gated uniformly (ARCHITECTURE-SPEC.md §3); there is no CLI-only hook path.
- **Suppression for bulk tooling.** The SDK exposes a way to run a mutation with hooks
  disabled (for import, migration, or `bench/` tooling that writes many issues). The
  `taskmgr` CLI always runs with hooks **enabled**.
- **Payload version.** The stdin payload carries `schema` (§5). Adding a field is additive;
  a removal/repurpose is breaking and is versioned with the SDK module (cf. QUERY-SPEC.md §7).
- **Spec sync.** When implemented, this extends TASK-STORAGE-SPEC.md §4.2 (the
  `config.yaml` schema gains `hook_timeout` and `hooks`) and is referenced from
  OVERVIEW.md; per CODING.md those updates land in the same change.

---

## 10. Non-goals

Deliberately excluded, with rationale:

- **No bypass / skip mechanism.** A gate that can be skipped is not a gate. To relax or
  remove one, edit `config.yaml`.
- **Hooks never mutate issues.** No writing labels or any field from a hook output; hooks
  gate (pre) or notify (post) only. The engine stays the sole author.
- **No per-hook `timeout`, `workdir`, or error policy.** One global `hook_timeout`; cwd is
  always the repo root; fail-closed (pre) / warn (post) is uniform.
- **`when` reads only `new`** — no `old.`/`new.` cross-state qualifiers.
- **No comment- or dependency-specific events** (§2.2).
