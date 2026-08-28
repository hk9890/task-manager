# task-writing

A taskmgr package that carries a body standard: the prose that teaches it, the
templates that shape it, and the gates that hold it.

## What it contributes

| Part | Effective id | What it does |
|---|---|---|
| Overview fragment | `pkg:task-writing:overview` | Lands in `taskmgr guide` itself, so every caller learns the four sections and where the standard is |
| Guide section | `pkg:task-writing:bodies` | The standard in full — type contracts, what each section owns, the templates — fetched by name |
| Gate | `pkg:task-writing:body-sections` | Refuses a `bug`/`feature`/`chore`/`task` whose body skips a section |
| Gate | `pkg:task-writing:epic-sections` | Refuses an `epic` without Context, Outcome, Success criteria |
| Templates | — | `templates/<type>.md`, one per type, each with that type's contract at the top |

Docs are never gated: a `doc` holds a document, not a path through work.

The three parts are the point. A gate alone teaches by refusing, which costs a
round trip per rule. The overview fragment states the rule to everyone who runs
`taskmgr guide` — it is capped at 1 KiB, so it names the rule and the command that
explains it, and stops. The guide section is that explanation, fetched by whoever
is about to write a body. All of it ships in one directory at one version, so
prose and gate cannot drift apart.

## Install it

taskmgr never fetches a package: installing one is copying the directory and
naming it in a config file's `use:` list.

**For one project** — the entry travels in git, so everyone who works in the
repository gets the same gates:

```bash
mkdir -p <project>/.tasks/packages
cp -r task-manager-packages/task-writing <project>/.tasks/packages/
taskmgr -C <project> package add --path packages/task-writing
```

**For this machine** — applies to every store you open, and no colleague sees it:

```bash
mkdir -p "${TASKMGR_HOME:-$HOME/.taskmgr}/packages"
cp -r task-manager-packages/task-writing "${TASKMGR_HOME:-$HOME/.taskmgr}/packages/"
taskmgr package add task-writing --global
```

Check either with `taskmgr package list` and `taskmgr hook list`.

A store entry that names a package a colleague has not installed stops their
mutations until they install it. That is deliberate — a gate the configuration
depends on must not be silently absent — but it is a cost you put on other
people, so prefer the machine-wide install while you are trying the package out.

## The gate fails open when it cannot read the payload

`hooks/body-sections.sh` needs `python3` or `jq` to read the hook payload. With
neither on the machine it allows the write and returns a hint saying the body was
not checked.

That is a deliberate choice by this package, not taskmgr's default: a pre-hook
that cannot run denies the transition, which here would mean a machine without
`jq` could not write to the store at all. An unchecked body is the smaller
failure. If you would rather have the strict behaviour, change the two early
`exit 0` lines to `exit 1`.

## What the gate does not do

The check is structural. It sees that `## Acceptance criteria` is present with at
least one `- [ ]` item; it cannot see whether the criteria are any good. Clearing
the gate is the floor. The bar is the wish test in
`taskmgr guide pkg:task-writing:bodies`: can a competent stranger open the issue,
start without asking a question, and prove they are done without asking a second?
