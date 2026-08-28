# Hook Specification — lifecycle gates

**Hooks** are scripts the engine runs at issue state transitions. A **pre-hook** can
*block* a transition — refuse to close a task until tests pass. A **post-hook** *reacts*
to one after it commits — send a notification. Either kind may also hand back a short
**hint** for the agent that triggered it.

Hooks keep policy out of the core. The engine knows only issues, dependencies, and
ready-work (ARCHITECTURE-SPEC.md §1); rules like "tests must pass before close" or
"a feature needs a Definition-of-Done section" are hooks. They are declared in a **hook
package** — a directory holding a manifest and the scripts the hooks run (§3.6) — which a
configuration file names in its `use:` list, per repository or, for a machine rather than
a repository, in the per-user config (§3.5).
Hooks are the project's **extension system**: the core stays minimal and
earns every feature it keeps, and anything that *can* live in a hook is a hook rather than
engine code (ARCHITECTURE-SPEC.md §9–§10).

The primary caller is an **autonomous agent** that files and closes its own work — the
same audience as the CLI (CLI-SPEC.md) and the query language (QUERY-SPEC.md). Hooks exist
mainly so such an agent cannot silently skip policy: a gate denies with a structured reason
and an optional hint, the agent reads it, fixes the work on its side, and retries. That
framing is why the surfaces are machine-readable (JSON denial, hints, a versioned payload)
and why hooks **never mutate** the issue themselves (§1, §10) — they tell the agent what to
change; they do not change it.

The contract is uniform:

- the **engine** classifies the transition and fires the matching named event
  (`pre-close`, `post-create`, …) — a script never diffs `old`/`new` to discover what
  happened;
- every event delivers the **same input** — `{event, old, new}` on stdin;
- a hook **decides or reacts; it never edits the issue**. The write path stays the sole
  author of on-disk state (ARCHITECTURE-SPEC.md §6 write path, §7 one-writer invariant).

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
   and the decision are fixed by the two configuration files — the store's `config.yaml`
   and the per-user one (§3.5) — plus the package manifests those files name. All are
   plain files a reader can inspect, and `taskmgr hook list` prints the result
   (CLI-SPEC.md §2.3); nothing else contributes a hook.
6. **Engine-level.** Hooks fire inside the `Store` write path, so every front end (the
   `taskmgr` CLI, any future consumer) is gated identically (ARCHITECTURE-SPEC.md §3).

---

## 2. Events and trigger points

Each transition fires up to two events: a **pre-event** before the write (gating) and a
**post-event** after it commits (notification).

| Transition | Pre-event (gates) | Post-event (notifies) | `old` | `new` |
|---|---|---|---|---|
| create a new issue | `pre-create` | `post-create` | `null` | candidate (new issue) |
| modify a live issue (no closed-ness change) | `pre-update` | `post-update` | current | candidate |
| transition **into** `closed` | `pre-close` | `post-close` | current | candidate (`closed`) |
| transition **out of** `closed` | `pre-reopen` | `post-reopen` | current | candidate |

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
  The engine detects this by comparing the materialized `new` to `old`, so the guarantee
  holds for every front end — it is not a CLI-level short-circuit.
- A denied `pre-create` creates no file — the issue simply never comes into existence.

### 2.2 Not hooked in v1

Out of scope, additive later:

- comment add/edit/remove;
- dependency-specific events — `dep add`/`dep rm` and `blocked_by`/`related` changes via
  `update` fire `pre-update`/`post-update` like any other edit, but there is no event
  dedicated to dependency changes.

---

## 3. Configuration

A configuration file declares no hooks. It lists the **packages** it takes them from:

```yaml
prefix: proj

hook_timeout: 2s                  # store-wide: max runtime for ANY single hook. Default 2s.

use:
  - name: doc-policy              # <taskmgr home>/packages/doc-policy
  - path: packages/repo-policy    # relative to the directory holding this file
```

The store's file travels with the repository, so the packages it names apply to everyone
who works in it (TASK-STORAGE-SPEC.md §4.2). A second list may be declared in the
per-user config (CONFIG-SPEC.md §2); the two are merged as §3.5 describes. The package
format itself is §3.6.

Either `use:` list is editable by hand or with `taskmgr package` (CLI-SPEC.md §2.3).

**Why a package and not an inline block.** A hook is argv, and its working directory is
the project root (§3.2), so an inline entry could name a script only by absolute path —
which is machine-specific, and therefore un-shippable. A package binds the hooks and
their scripts into one directory and gives a relative `argv[0]` a meaning that survives
being copied (§3.6). It also collapses the merge: two files each naming packages, rather
than two files each carrying hooks *and* naming packages.

### 3.1 `hook_timeout` (top-level)

A single, **store-wide** wall-clock limit applied to **every** hook process the store
runs, inherited ones included; there is no per-hook timeout. Go duration string (`"2s"`,
`"5m"`); default **`2s`**; `0` disables it. It may also be set in the per-user config as
a fallback, where the store's own value wins (§3.5 rule 3).

**A package cannot set it.** The limit is how long the store lock may be held (§8), so a
package that could raise it would extend that for every project on the machine, from a
file the machine's owner did not write. A package whose gate needs longer says so in its
own documentation, and the person installing it sets `hook_timeout` once.

With `0`, nothing bounds a hook — a hang blocks the command indefinitely, and for a
pre-hook holds the store lock for that whole time (§8). Such a hook stays in taskmgr's
process group so `Ctrl-C` can still end it (§7.1); prefer a generous limit over none.

`hook_timeout` bounds when the hook is **signalled**, not when it is guaranteed
gone: a hook that does not exit on SIGTERM is SIGKILLed after a fixed 2-second
grace (§7). The worst case is therefore **`hook_timeout` + 2s** — with the default,
~4s, not 2s. For a pre-hook that is lock-hold time (§8).

The 2-second default suits fast structural validators. **A project that runs a test
suite on close must raise it** (e.g. `hook_timeout: 5m`) — and should weigh the lock
cost in §8. Exceeding the limit is a hook error (§7): a deny for pre-hooks, a warning
for post-hooks.

A test gate's value over the existing commit/CI gate (docs/TESTING.md) is **real-time,
per-transition feedback**: the agent that closes a task learns in the same call that the
work is not green, with a structured reason to act on, instead of discovering it later at
commit or push. The cheaper, lower-overlap cases — fast structural validators (DoD section
present, label shape) and post-hook notifications — should lead a project's hook
configuration.

### 3.2 Hook fields

A hook is one entry of a package manifest's `hooks:` list (§3.6).

| Field | Required | Meaning |
|---|---|---|
| `id` | **yes** | The hook's label within its package. The **effective id** — what a denial reason, the logs and `taskmgr hook list` report — is `pkg:<package>:<id>`. A declared `id` **must not contain `:`**, which separates those three parts. There is no positional default: a package is replaced whole when it is updated, so a numbered id would move to a different hook as soon as an entry was added above it, and a denial reason would stop naming the same gate across versions. |
| `event` | **yes** | One of the eight events (§2). Any other value is a config error (§3.4). |
| `when` | no | A QUERY-SPEC.md filter expression. The hook runs only if it matches **`new`** (§3.3). Omitted → always. |
| `run` | **yes** | Non-empty argv array, executed **directly** via `execve` — **no shell**. For shell features use `["sh", "-c", "make lint && make test"]`. A relative `argv[0]` is resolved inside the package (§3.6). |

There is no per-hook `timeout`, `workdir`, or error policy. Timeout is the one global
`hook_timeout`; the working directory is the **project root** (for a local store, the
directory that contains `.tasks/`); fail-closed (§4) is uniform.

**One fallback:** when the project root no longer exists, the hook runs in the store's
own data directory instead. A central store outlives the project directory it is
registered for — the entry still matches and still opens (CONFIG-SPEC.md §3), and every
issue file is intact — so spawning in a directory that is gone would make one global
hook turn every such store permanently un-writable, reported as a hook that "could not
be executed".

### 3.3 `when` semantics

`when` reuses the filter-expression language unchanged (QUERY-SPEC.md) — the same fields,
operators, and grammar as `taskmgr list -q` — evaluated against the **`new`** issue:

- field predicates (`type`, `priority`, `label`, `status`, …) read `new`'s fields;
- the derived `ready` / `blocked` predicates are computed against the store as of the
  moment the hook fires: for a **pre-hook** that is the pre-write store with the
  materialized `new` overlaid in memory (the change is not yet on disk); for a
  **post-hook** it is the committed store.

`when` reads **only `new`**; there are no `old.`/`new.` qualifiers. It *scopes* a hook, it
does not decide the transition (the event already did). `event: pre-close` +
`when: 'type == "feature"'` reads as "gate the closing of features". A `when` that fails to
parse is a config error (§3.4).

### 3.4 Config validation

The `use:` list and `hook_timeout` are read when the store is opened for a **write**,
and every package the list names is loaded then. If anything is wrong — a `use` entry
that sets neither or both of `name` and `path`, an invalid package name, a package that
is **not installed** or holds no manifest, a missing or duplicated hook `id`, an unknown
`event`, empty/missing `run`, unparseable `when` or `hook_timeout` — **every mutation
fails** with a configuration error naming the package and what is wrong, until fixed
(fail-closed config; §1 principle 4). **Reads are never affected.** Unknown keys within a
`use` entry or a hook entry are ignored for forward-compatibility (TASK-STORAGE-SPEC.md §4.2).

A missing package fails closed rather than being skipped, and there is no per-entry
switch to soften it. A `use:` entry states that the configuration depends on that
package; running a store with the gate silently absent is the outcome the whole
arrangement exists to prevent. The store config travels in git, so an entry naming a
package a colleague has not installed will stop their mutations with a message naming
what to install — visible, and one action away from fixed.

This applies to the per-user file too, where the same failure is wider: a package named
there that will not load fails mutations in **every store on the machine**. `taskmgr
package add` loads the package before it writes the entry, so a package that could never
run is normally refused at the command that named it rather than at the next unrelated
mutation.

**A write checks what it introduces, not what it finds.** An entry already in the file is
left alone, and only the timeout is re-checked when the write changes it. Checking the
whole list instead makes a bad entry refuse the write that *removes* it: two of them in
the per-user file then fail every write on the machine, and the only way out is a hand
edit.

### 3.5 The chain: two `use:` lists

The per-user config (CONFIG-SPEC.md §2) carries a `use:` list with the same schema as a
store's. It applies to **every** store this machine resolves.

**A `use:` entry.**

| Field | Required | Meaning |
|---|---|---|
| `name` | one of | A package name, resolved to `<taskmgr home>/packages/<name>`. Machine-independent, so a store config can carry it into git and every machine finds its own copy. |
| `path` | one of | A directory, resolved against the directory holding **this** config file — `.tasks/` for a store, the home for the per-user one. A package under `.tasks/` travels with the store, `store move --central` included. |

Exactly one of the two is set; an entry with both, or neither, is a config error (§3.4).
Which is meant is always stated rather than inferred from the string, matching the rule
CONFIG-SPEC.md applies to naming a store. Nothing about a package's **origin** is
recorded — no URL, no revision — so resolution never reaches the network and a hook is
never fetched during a `create` or a `close`.

**Merge (normative).**

1. **The per-user config's packages run first**, then the store's, each list in its own
   order and each package's hooks in manifest order. The effective chain for an event is
   `[per-user…] ++ [store…]`; §4's "first deny wins" then selects within it, so a
   machine-wide gate is what surfaces when both would deny.
2. **Effective ids are `pkg:<package>:<hook>`** (§3.2). The package name is part of the
   id, so a denial always says which package to open, and `taskmgr hook list` prints the
   same string.
3. **A package named by both files contributes once**, from the list that named it first
   — which is the per-user one. The shadowed entry is reported by `taskmgr package list`
   rather than dropped silently. Making the duplicate an error instead would let one
   person's machine-wide install break the repository for every colleague who has it.
4. **`hook_timeout`**: the store's value when it sets one, else the per-user one, else
   the 2s default. One limit still bounds every hook in the merged chain, and no package
   contributes one (§3.1).
5. **No opt-out.** A store cannot suppress inherited packages; the escape hatch is
   editing the per-user config. A gate configured for the machine applies to every
   project on it.
6. **The working directory is unchanged** — always the project root (§3.2). Only
   `argv[0]` is resolved inside the package (§3.6), so a hook's own arguments and any
   path it reads at run time still mean what they mean in the project.

**When to use which file.** A store's config travels with the repository; the per-user
config does not. A package named there holds on your machine and nowhere else — not for a
colleague, not in CI — so it fits personal ergonomics and not an invariant the data
depends on. Anything in the second category belongs in the store's `config.yaml`.

**Neither `use:` list is read on a read path.** Both are loaded where the hook set is
compiled — the first write (§3.4) — so no query, list or show consults them, and the
local walk-up of store resolution stays free of global config (CONFIG-SPEC.md §4). This
is about the *packages*, not about the file: resolving a **central** store already reads
the per-user config on every command to find `central_root`, and that is unchanged. A
machine with no locatable home simply inherits nothing; that is not an error.

### 3.6 The package format

A **hook package** is a directory. Its **directory name is the package name** — there is
no `name` key in the manifest, so a copy that lands in a differently-named directory
cannot disagree with its own manifest. The name follows the store-name grammar
(CONFIG-SPEC.md §3): one path segment, leading alphanumeric, at most 64 characters.

```
doc-policy/
├── taskmgr-package.yaml
└── hooks/doc-path.sh
```

```yaml
# taskmgr-package.yaml
version: 1
hooks:
  - id: doc-needs-path
    event: pre-create
    when: 'type == "doc" && !(label ~ "path:")'
    run: ["./hooks/doc-path.sh"]   # resolved inside this directory
```

| Field | Required | Meaning |
|---|---|---|
| `version` | no | Manifest schema version; defaults to `1`. |
| `hooks` | no | The hooks this package contributes, in run order. Entry schema in §3.2. |

**Resolving `argv[0]` (normative).** When the first element of a hook's `run` is a
relative path **containing a path separator**, it resolves against the package directory.
An absolute path is used as given. A first element with **no** separator is left alone: it
is a `PATH` lookup, exactly as `execve` treats it, so the documented `["sh", "-c", …]`
idiom finds the system shell rather than searching the package. Only `argv[0]` is
rewritten; every other argument is passed verbatim, and the working directory is still
the project root (§3.2).

This one rule is what makes a package portable. Without it a hook could name its script
only by absolute path, which differs per machine, and "shipping a gate" would mean
shipping a path the recipient has to edit.

**A package carries hooks and nothing else.** No `hook_timeout` (§3.1), no `prefix`, no
`central_root`. The config surface a package could otherwise reach is either
store-identity (which a package must not touch) or machine policy (which the machine's
owner sets), and ARCHITECTURE-SPEC.md §10 keeps that surface from growing: a behaviour
that can be a hook is a hook.

**Packages do not nest.** A manifest may not name other packages. There is therefore no
cycle detection, no depth limit, and no shared-dependency resolution — a chain is exactly
the two `use:` lists and the manifests they name.

**A directory, never an archive.** taskmgr never fetches a package and never extracts
one. A program cannot be executed inside an archive, so supporting one would mean
taskmgr extracting it — and then owning the execute bit it must preserve and the
path-traversal safety extraction demands. Installing a package is creating the directory
and putting the manifest and scripts in it.

---

## 4. Execution and the write path

This extends the write path of ARCHITECTURE-SPEC.md §6. Pre-hooks run **inside** the
lock; post-hooks run **after** it is released.

1. **Acquire** the store lock.
2. **Apply** the change in memory → `new`; `old` is the current on-disk issue (or `null`).
   The engine materializes `new` itself and never re-reads it from disk, so pre-hooks and
   `when` (§3.3) evaluate against this in-memory candidate; post-hooks read the committed store.
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
- **Observability.** Every hook invocation is logged with its event, `id`, issue id,
  decision, and **wall-clock duration**; a hook that exceeds `hook_timeout` or errors is
  logged at a higher level. The logged decision distinguishes the phases, because only a
  pre-hook can block a write: a pre-hook refusal is `deny`, a post-hook failure is `warn`,
  and a hook that misbehaved is `error` in either phase (§7). Hook timing is the main
  signal for the in-lock cost of §8 — see [MONITORING.md](../MONITORING.md).
- **Environment.** Each hook process inherits the parent environment plus:

  | Variable | Value |
  |---|---|
  | `TASKMGR_HOOK_EVENT` | the event, e.g. `pre-close` / `post-close` |
  | `TASKMGR_HOOK_ID` | the hook's **effective** id, `pkg:<package>:<hook>` (§3.2) |
  | `TASKMGR_ISSUE_ID` | the issue's id |
  | `TASKMGR_STORE` | absolute path to the `.tasks/` directory |
  | `TASKMGR_PAYLOAD_SCHEMA` | the input-payload schema version (§5) |

  The **canonical** input is the JSON on **stdin** (§5); the env vars are conveniences.
  `cwd` is the project root, with the one fallback in §3.2. `TASKMGR_STORE` always names
  the data directory, so a hook that must not depend on either reads it from there.

---

## 5. Input contract (stdin)

The engine writes one JSON object to the hook's **stdin** and closes it:

```json
{
  "schema": 1,
  "event": "pre-close",
  "issue_id": "proj-0042",
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

`old` and `new` use the same **shape** as the stable issue DTO (CLI-SPEC.md §6 `issueDTO`)
**plus** the `description` body, with empty optional fields omitted exactly as in the CLI's
JSON output. Because hooks fire inside the `Store` (§4), the **engine** owns this payload
serializer: CLI-SPEC §6 `issueDTO` defines the field shape — a contract the two must keep
identical — not an importable symbol. The `taskmgr` rendering DTO lives in the CLI package
and is deliberately not reachable from the engine (the CLI imports the SDK, never the reverse).

```json
{
  "id": "proj-0042", "title": "Fix drill navigation",
  "status": "closed", "type": "bug", "priority": 1,
  "assignee": "hans", "creator": "hans",
  "labels": ["area:details"],
  "parent": "proj-0007", "blocked_by": ["proj-0040"], "related": ["proj-0012"],
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
  -q "parent == \"$TASKMGR_ISSUE_ID\" && status != \"closed\"" | jq length)
[ "$open_children" -eq 0 ] || { echo "epic has $open_children open children" >&2; exit 1; }
```

`closed` is a date field: the only bare booleans are `ready` and `blocked`
([QUERY-SPEC](QUERY-SPEC.md) §2), so `!closed` is a parse error.

### 5.2 Example — `pre-create` (structure validation)

```json
{
  "schema": 1,
  "event": "pre-create",
  "issue_id": "proj-0050",
  "old": null,
  "new": {
    "id": "proj-0050", "title": "Add export", "status": "open",
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
  { "error": "hook_denied", "event": "pre-close", "hook": "pkg:repo-policy:tests-before-close",
    "issue_id": "proj-0042", "exit": 1,
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
| `hook_timeout` exceeded (§3.1) | `SIGTERM`, then `SIGKILL` after a 2s grace → Deny, `hook_error` | Warning |
| Killed by a signal | Deny, `hook_error` | Warning |

### 7.1 Timeout kill and the process group

A **bounded** hook runs in **its own process group**, and the timeout signals the
whole group, not just the process taskmgr spawned. This matters because §3.2
points at `["sh", "-c", ...]` for shell features: the shell exits on SIGTERM but
its children do not receive it, keep running, keep the captured stdout/stderr
open — which stalls the engine for the full grace on top of `hook_timeout` (§3.1,
§8) — and are orphaned once it gives up. Signalling the group ends the whole tree.

Three consequences:

- The **2-second SIGKILL grace is fixed and additive** to `hook_timeout` (§3.1).
  It is only reached by a hook that ignores SIGTERM.
- **Both signals go to the group.** The escalation after the grace is a SIGKILL to
  the whole group, not to the spawned process alone — otherwise a child that traps
  or ignores SIGTERM outlives the command as an orphan, which is the exact leak
  the process group exists to prevent.
- A bounded hook is **detached from the terminal's foreground group**, so a
  `Ctrl-C` aimed at `taskmgr` no longer reaches it. `hook_timeout` is what bounds
  it instead.

**`hook_timeout: 0` is not detached.** With the limit disabled there is no timeout
to enforce and therefore no signal to deliver to a group — and detaching anyway
would leave a hook that nothing bounds *and* that `Ctrl-C` cannot reach, so a
hanging hook could only be ended by finding it with `ps`. An unlimited hook
therefore stays in taskmgr's own process group, where an interrupt still reaches
both. This is the one case in which a hook is not group-isolated.

A hook **must not** invoke `taskmgr` *mutations*: a pre-hook runs while the store lock is
held and would deadlock; a post-hook could trigger further hooks. Read-only queries are
fine.

Fail-closed means a **misconfigured pre-hook wedges the relevant mutations** until fixed
(a typo'd `run` blocks all closes). This is intentional — the point of a gate is that it
cannot be skipped. **There is no bypass flag** (§10): to relax or remove a gate you edit
the package, or drop it from the `use:` list. Up-front checking (§3.4) makes the failure
a clear config error rather than a mysterious per-close one.

---

## 8. Concurrency and the lock

Pre-hooks run **inside** the store write lock (§4), after validation and before the atomic
write. This is a **deliberate, settled** choice, not a v1 shortcut. The decision is made
against exactly the state that will be written (no check/use gap), the engine hands the hook
the materialized `old` and `new` as one atomic snapshot, and a denial is atomic. Running a
pre-hook *outside* the lock would mean it could not be given a stable `old`/`new` pair — the
store could move under it — so the gate would decide against state that is no longer the
state being written. The in-lock model is the price of a correct gate, and it is the chosen
model.

**The cost:** while a pre-hook runs, the store-wide `flock` is held, so all other writers
block until it returns. The worst case is `hook_timeout` + the 2-second SIGKILL grace
(§3.1, §7.1) — ~4s at the default. **If you raise `hook_timeout` to run a test suite on
close, you serialize all writes for that duration.**
Post-hooks avoid this by running outside the lock. The cost is not hidden: every hook's
wall-clock duration is logged (§4, [MONITORING.md](../MONITORING.md)), so a
project can see exactly how long its gates hold the lock and decide whether to raise
`hook_timeout`, move a slow check to a post-hook, or push it to CI.

---

## 9. Surface and versioning

- **Engine-level.** Hooks fire from the `Store` mutation path, so the CLI and every SDK
  consumer are gated uniformly (ARCHITECTURE-SPEC.md §3); there is no CLI-only hook path.
  The one exception is bulk import, below.
- **Suppression is scoped to bulk import, not a general flag.** The everyday mutations
  (`Create` / `Update` / `Close` / `Reopen`) always run hooks — there is no
  `WithHooks(false)` on them, so the no-bypass guarantee (§7) is a property of those
  methods. Bulk loading is instead a **distinct call** — `Store.Import` (SDK-SPEC.md §4),
  the direct write of a complete end-state used for import and migration tooling —
  which takes an explicit option to run hooks or omit them, defaulting to
  **omit** (re-importing N issues should not fire N create/close gates). The only ungated
  path is therefore an import a caller opts into deliberately; the `taskmgr` CLI's ordinary
  commands always run with hooks **enabled**.
- **Payload version.** The stdin payload carries `schema` (§5). Adding a field is additive;
  a removal/repurpose is breaking and is versioned with the SDK module (cf. QUERY-SPEC.md §7).
- **Spec sync.** Hooks span several specs, which stay consistent (per CODING.md): the
  `config.yaml` schema carries `hook_timeout` and `use` (TASK-STORAGE-SPEC.md §4.2); the
  pre/post-hook steps sit in the write path (ARCHITECTURE-SPEC.md §6); the run-or-omit-hooks
  flag on `Import` and the hook-denied error are in SDK-SPEC.md (§3/§4/§6); and the `hints` /
  `warnings` fields and the `hook_denied` error shape are in CLI-SPEC.md §6. A change to the
  hook contract updates all of them together.

---

## 10. Non-goals

Deliberately excluded, with rationale:

- **No bypass / skip mechanism.** A gate that can be skipped is not a gate. To relax or
  remove one, edit the package or drop it from the `use:` list.
- **Hooks never mutate issues.** No writing labels or any field from a hook output; hooks
  gate (pre) or notify (post) only. The engine stays the sole author. This is deliberate
  even though auto-labeling is a common lightweight-tracker hook: the chosen ergonomic is
  **"gate, don't fix"** — a hook denies with a reason/hint (e.g. "label `area:*` required")
  and the agent, the primary caller, applies the change on its side and retries. Keeping
  hooks side-effect-free preserves the one-writer invariant (ARCHITECTURE-SPEC.md §7) and
  the validation/atomicity guarantee.
- **No per-hook `timeout`, `workdir`, or error policy.** One `hook_timeout` per store (§3.1); cwd is
  the project root (§3.2); fail-closed (pre) / warn (post) is uniform.
- **`when` reads only `new`** — no `old.`/`new.` cross-state qualifiers.
- **No comment- or dependency-specific events** (§2.2).
- **No package installer, and no origin in a `use:` entry** (§3.5). taskmgr never fetches
  or extracts; a package is a directory somebody put there. Recording where one came from
  and at what revision is additive — two optional keys on an entry — and is left until a
  package is actually distributed.
- **No per-entry override of the missing-package failure** (§3.4). A `use:` entry means
  the configuration depends on that package, and one fixed behaviour is easier to reason
  about than a switch that can silently disable a gate.
