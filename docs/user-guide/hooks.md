# Hooks

A hook is a script `taskmgr` runs when an issue changes state. A **pre-hook** can refuse
the change — "no closing a feature until the tests pass". A **post-hook** reacts after it
has already happened — send a notification. Either can hand back a short note for whoever
made the change.

Hooks are how a project adds its own rules without those rules being built into `taskmgr`.

## Hooks live in packages

You do not write a hook into a configuration file. You put it in a **package**: a
directory holding a manifest and the scripts the hooks run. A configuration then lists the
packages it uses.

Make the directory and write the manifest:

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
    run: ["./hooks/doc-path.sh"]
```

Then tell a store to use it:

```bash
taskmgr package add doc-policy   # ~/.taskmgr/packages/doc-policy
taskmgr package list             # every package that gates this store, and whether it loads
taskmgr hook list                # every hook that gates this store, in the order it runs
```

`taskmgr` never downloads, unpacks or writes a package. Installing one is putting the
directory where the reference points — `~/.taskmgr/packages/<name>` for a package you use
everywhere, or a directory inside `.tasks/` for one that belongs to a single project. Copy
it, clone it, unzip it: `taskmgr` only ever reads it.

## A package can teach as well as refuse

A gate teaches by refusing. The agent files, is denied, reads the reason and retries —
which works, and costs a round trip for every rule it has not met yet.

Add a `guide:` list and the package states the rule up front. `taskmgr guide` prints the
text after its own sections, so whoever reads the guide before starting has the rule
already:

```
doc-policy/
├── taskmgr-package.yaml
├── guide/paths.md
└── hooks/doc-path.sh
```

```yaml
version: 1
guide:
  - id: paths
    file: ./guide/paths.md
hooks:
  - id: doc-needs-path
    event: pre-create
    when: 'type == "doc" && !(label ~ "path:")'
    run: ["./hooks/doc-path.sh"]
```

```bash
taskmgr guide                       # your text, after the built-in sections
taskmgr guide pkg:doc-policy:paths  # just yours
taskmgr guide packages              # every package's text, and nothing built in
```

The prose and the gate ship in one directory at one version, so they cannot drift apart.
Write the fragment as instructions to whoever files the issue, and say what the gate will
refuse — a reader who knows the rule does not have to discover it by being denied.

Three limits are worth knowing. A fragment is a Markdown file **inside** the package, and
an absolute path is refused — a package has to survive being copied to another machine. It
is capped at 8 KiB, cut on a line boundary and marked, because the text lands in a
reader's context whole. And nothing about a guide can fail a command: a fragment whose
file is missing is reported in the output, and `taskmgr guide` still exits `0`. Fail-closed
protects a write from running without its gate, and a guide is not a gate.

A package may carry `guide:` with no `hooks:` at all — that is the ordinary shape for a
convention worth stating but not mechanically checkable.

### Why the script sits next to the manifest

A hook runs with the **project root** as its working directory, so a path written into a
configuration file could only ever be an absolute one — which is different on every
machine. Inside a package the rule is different, and that is the whole point: a **relative
`run` path is found inside the package**, wherever the package was put. So `doc-policy`
works on your machine and on a colleague's without either of you editing a path.

Two details follow from it. Only the *first* element of `run` is treated this way; every
other argument is passed through untouched. And a first element with no `/` in it is left
alone, because that is a `PATH` lookup — which is what makes `["sh", "-c", "…"]` still
find your shell.

### Writing the entries

`run` is a list, one argv element per item, because a hook is executed **directly — there
is no shell**, so there is no quoting rule to guess at. For a pipeline or a `&&`, ask for
a shell: `run: ["sh", "-c", "make lint && make test"]`.

`when` takes the same filter expressions as `taskmgr list -q`
([Filtering and search](queries.md)), evaluated against the issue *as it would be after
the change*. It scopes a hook; it does not decide which event fires.

`id` is required, and must not contain `:`. Everywhere a hook is named — a refusal,
`taskmgr hook list`, the logs — it appears as `pkg:<package>:<id>`, so the name always
says which package to open.

### Naming a package that lives in the project

A package can live inside the store, where it travels with the repository:

```bash
taskmgr package add --path packages/repo-policy
```

The path is relative to `.tasks/`, so the package is committed alongside the tasks and
every clone of the repository has it without installing anything.

## Which file names the package: the project's, or yours

Two files carry a `use:` list, and `taskmgr package` picks between them the same way —
`--global` selects the second:

| File | Applies to | Travels |
|---|---|---|
| the store's `config.yaml` | that project | committed with the repository, so it binds everyone and every agent working in it |
| your `~/.taskmgr/config.yaml` | **every** store on this machine | nowhere — it is yours alone |

Put a rule the data's integrity depends on in the store's file. Keep the per-user file for
personal ergonomics — a reminder, a notification, a local lint — because a colleague and CI
will never see it.

Three consequences of the split:

- **Your machine's packages run first**, then the project's. First deny still wins, so a
  machine-wide gate is the one that surfaces when both would refuse.
- **Naming the same package in both files runs it once**, from your file. `taskmgr package
  list` marks the project's entry `shadowed` so the duplicate is visible rather than
  puzzling.
- **`taskmgr hook list` is the answer to "what gates this store"**, because neither file
  can tell you on its own — the hooks are in the packages the two files name.

A store cannot switch inherited packages off. The escape hatch is editing your own file.
For `hook_timeout` the store's value wins, then yours, then the 2s default. A package
cannot set it: the limit is how long everyone else waits for the lock, so it stays yours.

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
and `TASKMGR_PAYLOAD_SCHEMA` — and the working directory is the project root. If that
directory has been deleted — which a store kept outside the project outlives — the hook
runs in the store directory instead, so your tasks stay writable. A hook that needs one
of the two whatever happens should read `TASKMGR_STORE`.

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
# epic-policy/hooks/epic-children.sh — pre-close
open=$(taskmgr -C "$TASKMGR_STORE/.." list --json \
  -q "parent == \"$TASKMGR_ISSUE_ID\" && status != \"closed\"" | jq length)
[ "$open" -eq 0 ] || { echo "epic still has $open open children" >&2; exit 1; }
```

```yaml
# epic-policy/taskmgr-package.yaml
version: 1
hooks:
  - id: epic-children
    event: pre-close
    when: 'type == "epic"'
    run: ["./hooks/epic-children.sh"]
```

A denial reaches the caller as a message and exit code `1`, and as a structured error under
`--json`:

```json
{ "error": "hook_denied", "event": "pre-close", "hook": "pkg:epic-policy:epic-children",
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
  one, edit the package, or take it out of the `use:` list.
- **A package that will not load blocks every write** until you fix it, with a clear
  configuration error naming it. That includes a package you have not installed: a
  `use:` entry says the project depends on it, so `taskmgr` stops rather than running
  with the gate quietly absent. Reads are never affected, so you can still `list`,
  `show` and `taskmgr package list` your way out. A bad entry in your own file blocks
  writes in *every* project on the machine.
- **Never run a `taskmgr` mutation from a hook.** A pre-hook would deadlock against the
  lock it is already holding, and a post-hook would trigger further hooks. Read-only
  commands are fine — the example above is one.
- **Post-hooks cannot undo anything.** The write has already committed; a failure is
  reported as a warning and nothing rolls back.
- Bulk `taskmgr import` skips hooks by default, so loading a hundred issues does not fire a
  hundred gates. Pass `--run-hooks` when you want them.

Every hook run is logged with its event, id, decision and how long it took. Set
`TASKMGR_LOG=debug` and read stderr to see where the time goes.
