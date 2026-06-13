# Hook Specification — lifecycle gates

> **Status: Proposed — not yet implemented.** This document specifies a feature that
> does not exist in the engine yet. It is the design contract to build against, not a
> description of current behaviour. Until it ships, the other specs in `specs/` remain
> the authoritative description of the system.

This document specifies **hooks**: user-supplied scripts the engine runs at specific
issue **state transitions** to *gate* the transition. A hook's job is to answer one
question — **may this change be written?** — and, on refusal, to say why.

Hooks exist so that policy lives **outside** the core schema. The engine knows only
about issues, dependencies, and ready-work (ARCHITECTURE-SPEC.md §1); everything else
— "tests must pass before a task closes", "a feature must carry a Definition-of-Done
section before it is created" — is expressed as a hook. The core stays minimal; the
policy is pluggable and lives with the repository that needs it.

The model is **engine-classified named transition events with a uniform contract**:

- the **engine** decides what kind of transition is happening and fires the matching
  named event (`pre-close`, `pre-create`, …) — a script never has to diff old/new to
  discover "a close happened";
- every event carries the **same input shape** (`{event, old, new}`), so there is one
  input contract to learn regardless of the event;
- a hook is a **pure decider**: it may *allow* or *deny* (with a reason). It may **not**
  return a rewritten issue. The engine's write path stays the sole author of on-disk
  state (ARCHITECTURE-SPEC.md §7, "one writer").

---

## 1. Principles

1. **Gate, don't mutate.** A hook decides allow/deny. It never edits the issue; the
   engine writes exactly what it validated.
2. **Classified by transition, not by command.** `pre-close` fires whenever an issue
   *becomes* closed — whether via `taskmgr close` or `taskmgr update --status closed`.
   A gate keyed on a CLI verb would be trivially evaded; gates key on the state
   transition.
3. **One transition → one event.** The most specific event wins. A close is a
   `pre-close`, never *also* a `pre-update`, so a slow close-gate never fires on an
   ordinary edit.
4. **Fail closed.** If a hook cannot produce a clean *allow* — it is missing, not
   executable, times out, or emits malformed output — the transition is **denied**. A
   gate that fails open is not a gate.
5. **Deterministic.** For a given event and store state, the set of hooks that run, the
   order they run in, and the decision are fully determined by `config.yaml`.
6. **Engine-level.** Hooks fire inside the `Store` write path, so every front end (the
   `taskmgr` CLI today, any future consumer) is gated identically with no per-front-end
   logic (ARCHITECTURE-SPEC.md §3).

---

## 2. Events and trigger points

There are exactly four events in v1. Each fires **once**, on the in-memory *proposed*
state, after engine validation has passed and before the atomic write (§4).

| Event | Fires when the engine is about to… | `old` | `new` |
|---|---|---|---|
| `pre-create` | write a brand-new issue (`create`) | `null` | the proposed issue (id already allocated) |
| `pre-update` | modify a live (non-closed) issue **without** changing its closed-ness | current | proposed |
| `pre-close` | transition an issue **into** `closed` (was not closed → will be closed) | current | proposed (`status: closed`) |
| `pre-reopen` | transition an issue **out of** `closed` (was closed → will be active) | current | proposed |

### 2.1 Transition classification (normative)

The engine computes the proposed `new` issue, compares it to `old`, and selects the
**single** event by this priority:

1. `old` is absent → **`pre-create`**.
2. `old.status != closed` and `new.status == closed` → **`pre-close`**.
3. `old.status == closed` and `new.status != closed` → **`pre-reopen`**.
4. otherwise (a live issue changing among non-closed states/fields) → **`pre-update`**.

Consequences:

- An update that *also* closes (`update --status closed --priority 0`) is a **`pre-close`**;
  `new` reflects *all* changes (the new priority included). A close-gate therefore always
  sees the complete proposed state.
- A **no-op** mutation (nothing would change on disk) writes nothing and fires **no**
  hook.
- A vetoed `pre-create` writes nothing and **burns no id** — id allocation is a
  `max+1` scan with no counter file (ARCHITECTURE-SPEC.md §7), so an aborted create
  leaves no trace and the number is reused by the next create.

### 2.2 Not hooked in v1

Deliberately out of scope, to keep the trigger set tight; all are additive later:

- comment add/edit/remove;
- dependency edits (`dep add` / `dep rm`) and other `blocked_by`/`related` changes made
  via `update` — these fire `pre-update` only insofar as they change the issue file, but
  no dependency-specific event exists;
- any **`post-*`** (after-write, non-vetoing notification) event. Hooks in v1 are
  *pre*-only. A post-event for notifications/side-effects is a likely future addition.

---

## 3. Configuration

Hooks are declared in the store's `config.yaml` (TASK-STORAGE-SPEC.md §4.2), under a
top-level `hooks:` key, as an ordered list. Being in `config.yaml` means hooks are
committed with the repository and shared across everyone (and every agent) working in it.

```yaml
prefix: dtt

hooks:
  - id: tests-before-close          # optional label, used in messages and logs
    event: pre-close                # required — one of the four events (§2)
    when: 'type == "feature"'       # optional — QUERY-SPEC filter over `new`; default: always
    run: ["make", "test"]           # required — argv, executed directly (no shell)
    timeout: 300s                   # optional — Go duration; default 60s; 0 = no timeout
    workdir: "."                    # optional — relative to repo root; default: repo root

  - id: require-dod
    event: pre-create
    run: [".tasks/validators/dod.sh"]
```

### 3.1 Fields

| Field | Required | Meaning |
|---|---|---|
| `id` | no | Stable label for the hook. Used in denial messages, logs, and (future) audit. Need not be unique, but unique ids make diagnostics clearer. Defaults to `<event>#<index>`. |
| `event` | **yes** | One of `pre-create`, `pre-update`, `pre-close`, `pre-reopen`. Any other value is a config error (§3.3). |
| `when` | no | A QUERY-SPEC.md filter expression. The hook runs only if it matches the **`new`** issue. Omitted/empty → always matches. See §3.2. |
| `run` | **yes** | A non-empty argv array. Executed **directly** via `execve`, with **no shell**. For shell features use `["sh", "-c", "make lint && make test"]`. |
| `timeout` | no | Go duration string (`"90s"`, `"5m"`). Default `60s`. `0` disables the timeout (the write lock is then held for as long as the hook runs — see §8). A test-suite gate will usually need a value well above the default. |
| `workdir` | no | Working directory for the process, resolved relative to the **repo root** (the directory that contains `.tasks/`). Default: the repo root. |

### 3.2 `when` semantics

`when` reuses the filter-expression language unchanged (QUERY-SPEC.md): the same fields,
operators, and grammar used by `taskmgr list -q`. It is evaluated against the **proposed
(`new`)** issue:

- field predicates (`type`, `priority`, `label`, `assignee`, `status`, `created`, …)
  read `new`'s fields;
- the derived predicates `ready` / `blocked` are computed against the store state as of
  the moment the hook fires (pre-write).

`when` only **scopes** a hook; it does not decide the transition (the event already did
that). `event: pre-close` + `when: 'type == "feature"'` reads as "gate the closing of
features". A `when` that fails to parse is a config error (§3.3).

> v1 deliberately does **not** add `old.`/`new.` qualifiers to the grammar. `when`
> sees only `new`. Cross-state predicates ("priority dropped") are a possible additive
> extension (§10); they are not needed for the motivating use cases.

### 3.3 Config validation

The `hooks:` block is validated when the store is opened for a **write**. If it is
malformed — an unknown `event`, an empty/missing `run`, a `when` that does not parse, an
unparseable `timeout` — **every mutation fails** with a clear configuration error until
it is fixed. This is the fail-closed principle (§1.4) applied to configuration: a broken
gate config must not silently let changes through. **Reads are never affected** by hook
configuration.

Unknown keys *within* a hook entry are ignored (forward-compatibility), consistent with
TASK-STORAGE-SPEC.md §4.2. An unknown top-level key other than `hooks`/`prefix` is also
ignored.

---

## 4. Execution and the write path

Hooks extend the write path of ARCHITECTURE-SPEC.md §6. The added step is **4a**:

1. **Acquire the store lock** (in-process mutex, then `flock` on `.tasks/.lock`).
2. **Apply** the change to an in-memory `Issue`, producing `new`; `old` is the current
   on-disk issue (or `null` for create).
3. **Validate** field invariants and referential integrity (existing engine validation).
   *Hooks never run on an issue the engine itself would reject.*
4. **Classify** the transition → the event (§2.1). Select the hooks whose `event`
   matches **and** whose `when` matches `new`, in **config order**.
   - **4a. Run each selected hook** (§5–§7), in order, **sequentially**.
     - The first hook that does not cleanly *allow* (a deny or a hook error) **aborts**
       the mutation: release the lock, write nothing, surface the reason.
     - If every selected hook allows, continue.
5. **Write atomically** (temp + `fsync` + `rename`).
6. **Release the lock.**

Notes:

- **Short-circuit.** Execution stops at the first non-allow. The returned reason is that
  hook's reason. (Aggregating all failures is a possible future ergonomic improvement,
  §10.)
- **No partial state.** Because hooks run before step 5, a denied transition leaves the
  store byte-for-byte unchanged.
- **Environment.** Each hook process receives the parent environment plus:

  | Variable | Value |
  |---|---|
  | `TASKMGR_HOOK_EVENT` | the event name, e.g. `pre-close` |
  | `TASKMGR_HOOK_ID` | the hook's `id` |
  | `TASKMGR_ISSUE_ID` | the issue's id |
  | `TASKMGR_STORE` | absolute path to the `.tasks/` directory |
  | `TASKMGR_PAYLOAD_SCHEMA` | the input-payload schema version (§5) |

  The **canonical** contract is the JSON on **stdin** (§5); the env vars are conveniences
  for scripts that need only an id or a path. `cwd` is the resolved `workdir`.

---

## 5. Input contract (stdin)

The engine writes a single JSON object to the hook's **stdin** and closes it. The object:

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
| `old` | object \| null | The issue **before** the change. `null` for `pre-create`. |
| `new` | object | The issue **as it would be written** if allowed. |

### 5.1 The hook issue object

`old` and `new` are a **hook issue object**: the stable JSON DTO of an issue
(CLI-SPEC.md §6 `issueDTO`) **plus** the `description` body. Optional fields are omitted
when empty, exactly as in the CLI's JSON output.

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

**Derived relationships (`blocks`, `children`) are intentionally not included** — they
require a store scan and most gates do not need them. A hook that needs them queries the
store itself (it has `TASKMGR_STORE` and the CLI on `PATH`), e.g. to refuse closing an
epic with open children:

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

A DoD validator reads `new.description`, checks for a `## Definition of Done` section
with at least one checklist item, and exits `0` or non-zero accordingly.

---

## 6. Output contract

**The exit status is the single source of truth for the decision.**

| Exit code | Meaning |
|---|---|
| `0` | **Allow.** The transition may proceed. |
| `1`–`125` | **Deny.** A well-formed refusal. The mutation is aborted. |
| `126`, `127` | **Hook error** — not executable / not found. Treated as deny (fail-closed, §7). |
| `128 + N` | **Hook error** — killed by signal `N`. Treated as deny (fail-closed, §7). |

A hook does **not** override its exit code by printing `allow` in JSON: to allow, exit
`0`; to deny, exit non-zero. There is exactly one source of truth.

### 6.1 Reason resolution

When a hook denies, the engine resolves a human/agent-readable reason, in order:

1. If **stdout**, trimmed, **begins with `{`**, it is parsed as a verdict object
   `{ "reason": string }`. A parse failure or a non-object is a **hook error** (§7),
   reported as "hook emitted invalid output". On success the reason is `.reason` (which
   may be empty).
2. Otherwise the reason is the trimmed **stderr**.
3. If both are empty, the engine supplies a generic reason naming the hook
   (`denied by hook 'tests-before-close'`).

stdout that does **not** begin with `{` is treated as informational text, not a verdict,
and does not by itself change the decision (the exit code does).

> v1 defines only `reason` in the stdout verdict object. A future version may let a hook
> return `labels` to stamp on the issue (e.g. `dod:ok`) so policy outcomes become
> queryable; this is an explicit non-goal here (§10) to keep the core schema untouched.

### 6.2 How callers surface a denial

A denied mutation fails the operation:

- **CLI:** exit code `1`, message on stderr prefixed `taskmgr: ` (CLI-SPEC.md §1). With
  `--json`, a structured error so an agent can act on it:

  ```json
  { "error": "hook_denied", "event": "pre-close", "hook": "tests-before-close",
    "issue_id": "dtt-0042", "exit": 1,
    "reason": "3 unit tests failing; HEAD not clean" }
  ```

- **SDK:** a typed error carrying the same fields (event, hook id, exit code, reason),
  distinguishable from validation and I/O errors.

---

## 7. Errors and invalid output

A **deny** (§6, exit `1`–`125`) is an *expected* outcome — the gate did its job. A
**hook error** is the gate itself misbehaving. Both **block the write** (fail-closed,
§1.4); they differ only in the reported category, for diagnosis. Hook errors:

| Condition | Handling |
|---|---|
| Binary missing / not executable (exit `126`/`127`, or spawn failure) | Deny. Reason: `hook '<id>': command not found` / `not executable`. Category `hook_error`. |
| **Timeout** (§3.1) exceeded | The process is sent `SIGTERM`, then `SIGKILL` after a short grace period. Deny. Reason: `hook '<id>' timed out after <d>`. Category `hook_error`. |
| Killed by a signal | Deny. Reason names the signal. Category `hook_error`. |
| stdout begins with `{` but is not a valid verdict object | Deny. Reason: `hook '<id>' emitted invalid output`. Category `hook_error`. |
| Hook writes to the store while running | Undefined and unsupported. The store lock is held (§4); a hook **must not** invoke `taskmgr` *mutations* (it would deadlock on the lock). Read-only queries are fine. |

The fail-closed default means a **misconfigured hook wedges the relevant mutations**
until fixed (e.g. a typo'd `run` blocks all closes). This is intentional — the safety of
a gate is the point — and is why the **bypass mechanism (§10) is the operational escape
hatch**, and why config is validated up front (§3.3) so the failure is a clear config
error rather than a mysterious per-close failure.

---

## 8. Concurrency and the write lock

Hooks run **inside** the store write lock (§4), after validation and before the atomic
write. This is the v1 choice, and it is the simplest model that is fully correct:

- the decision is made against exactly the state that will be written — no
  time-of-check/time-of-use gap;
- a denied transition is atomic — nothing is written;
- it slots into the existing write path as a single inserted step.

**The cost:** while a hook runs, the store-wide `flock` is held, so other writers (any
process, any agent, the human) block until it finishes. A slow gate — running a full
test suite on `pre-close` — serializes *all* writes for its duration, not just other
closes. The per-hook `timeout` (§3.1) bounds this; choose it deliberately.

> **Future option (not v1): optimistic, out-of-lock guards.** Slow guards could run
> *before* taking the lock, then the engine would acquire the lock, re-validate, confirm
> the issue has not changed since the snapshot the hook saw (else abort with a conflict
> for the caller to retry), and write. This removes the global stall at the cost of a
> conflict/retry concept. It is deliberately deferred: the dominant workflow is a single
> writer per repo, where there is nothing to stall. Revisit if multi-agent write
> contention proves to be a real problem.

---

## 9. Surface and versioning

- **Engine-level.** Hooks fire from the `Store` mutation path, so the CLI and every SDK
  consumer are gated uniformly (ARCHITECTURE-SPEC.md §3). There is no CLI-only hook path.
- **Suppression for bulk tooling.** The SDK exposes a way to run a mutation with hooks
  disabled (e.g. an option/variant used by import, migration, or `bench/` tooling that
  must write many issues without firing per-issue gates). The `taskmgr` CLI always runs
  with hooks **enabled** (the only exception being a future bypass flag, §10).
- **Payload version.** The stdin payload carries `schema` (§5). Adding a field is
  additive and does not bump it in a breaking way; a removal/repurpose is breaking and is
  versioned with the SDK module, mirroring QUERY-SPEC.md §7.
- **Spec sync.** When implemented, this feature also extends TASK-STORAGE-SPEC.md §4.2
  (the `config.yaml` schema gains `hooks`) and is referenced from OVERVIEW.md; per
  CODING.md, those updates land in the same change.

---

## 10. Open decisions

Tracked here so they are not silently resolved by implementation:

1. **Bypass / enforcement (the primary open decision).** What, if anything, lets a
   transition skip its gates? Candidates: (a) hard-enforce, no bypass; (b) a `--no-verify`
   flag that is always recorded (e.g. as a comment) so it is visible in review;
   (c) bypass available to humans but not agents (gated on something an agent lacks, e.g.
   an interactive TTY or an env flag). Recommendation: **(a) for v1**, revisit if it
   proves too rigid — (b)/(c) are additive.
2. **Failure aggregation.** v1 short-circuits at the first deny (§4). Running all matching
   hooks and returning every reason would give an agent the full fix-list in one pass, at
   the cost of always paying every hook's time.
3. **`old.`/`new.` qualifiers in `when`** (§3.2) for cross-state predicates.
4. **`labels` in the stdout verdict** (§6.1) to make policy outcomes queryable, weighed
   against keeping the core schema untouched.
5. **`post-*` events** (§2.2) for non-vetoing notifications/side-effects.
6. **Per-hook `on_error: allow|deny`** to opt specific hooks out of fail-closed.
