# task-writing

A taskmgr package that carries a body standard: the prose that teaches it, the
templates that shape it, and the gates that hold it.

## What it contributes

| Part | Effective id | What it does |
|---|---|---|
| Guide section | `pkg:task-writing:filing` | The gate's rule, printed inside `taskmgr guide filing` — so it reaches the caller that is about to file, without a second command |
| Guide section | `pkg:task-writing:types` | Choosing a type, the six rules a body clears, the templates — a job of its own, fetched by name |
| Guide section | `pkg:task-writing:decomposing` | Turning a review, a plan or a spec into a set: grounding, one issue per what, real edges, approval before filing |
| Gate | `pkg:task-writing:body-sections` | Refuses a `bug`/`feature`/`chore`/`task` whose body skips a section |
| Gate | `pkg:task-writing:epic-sections` | Refuses an `epic` without Context, Outcome, Success criteria |
| Templates | — | `templates/<type>.md`, one per type, each with that type's contract at the top |

Docs are never gated: a `doc` holds a document, not a path through work.

The pairing is the point. A gate alone teaches by refusing, which costs a round
trip per rule. Placing the prose into `filing` means an agent that runs one
command — the one it was going to run anyway, before writing a body — already has
the rule the gate will hold it to. All of it ships in one directory at one
version, so prose and gate cannot drift apart.

This package states no `overview:` fragment on purpose. Its rule governs one job,
so it belongs in that job rather than in the text every caller receives whatever
they came to do; the job's line in `taskmgr guide` says the store adds rules there.

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
Doing both is safe: the machine-wide entry runs and the store's is listed
`shadowed`, because one package name is one package. Remove the one you no longer
want with `taskmgr package rm task-writing`.

## The gate fails open when it cannot read the payload

`hooks/body-sections.sh` needs `python3` or `jq` to read the hook payload. With
neither on the machine it allows the write and returns a hint saying the body was
not checked.

That is a deliberate choice by this package, not taskmgr's default: a pre-hook
that cannot run denies the transition, which here would mean a machine without
`jq` could not write to the store at all. An unchecked body is the smaller
failure. For the strict behaviour instead, change both `exit 0` lines that print
`so the body was not checked` to `exit 1`.

## What the gate does not do

The check is structural. It sees that `## Acceptance criteria` is present with at
least one `- [ ]` item — ticked as `- [x]` or `- [X]` counts — and it cannot see
whether the criteria are any good. Clearing the gate is the floor. The bar is the
wish test in `taskmgr guide pkg:task-writing:types`: can a competent stranger
open the issue, start without asking a question, and prove they are done without
asking a second?

**It does not check a write that leaves the body alone.** `new` is the whole
candidate issue rather than a delta, so a gate that re-read it on every edit would
refuse `taskmgr update --priority 3` on any issue written before you installed
this package. Instead the gate compares `new` against `old` and stands aside when
the description is unchanged. So installing into a store that already has issues
costs nothing up front: existing bodies keep working, and each one is held to the
standard the next time somebody edits it. To adopt the standard across a store
deliberately, rewrite the bodies — `taskmgr list -q 'status != "closed"'` names
the candidates, and `templates/<type>.md` is what to fill in.
