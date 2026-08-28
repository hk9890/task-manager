# Agents

`taskmgr` is built to be driven by a coding agent as much as by a person. This page is what
to tell one, and the conventions it can rely on.

## Point an agent at it in one line

`taskmgr` explains itself, so a project's agent instructions do not need a taskmgr section:

> Track work with `taskmgr`. Run `taskmgr guide` first.

`taskmgr guide` prints the issue model, the everyday command loop and the filter language
as plain text. `taskmgr commands` prints a catalog of every command with its flags and an
example — derived from the live command tree, so it cannot fall out of date. Both ship
inside the binary, so an agent with the binary needs no files and no network.

## Paste the guide into the agent's instructions

The guide is written to be **injected**, not read. Most agent frameworks can run a command
and paste its output into a prompt before the model sees it, and that is the intended use:
the agent starts holding the rules instead of being told where to find them.

Two properties make that safe:

- **It never fails on the state of the machine.** No store, a package that is not
  installed, an unreadable section — each is reported inside the output and the command
  still exits `0`. A framework that treats a non-zero exit as "abort" never loses the
  guide because of a project it happens to be standing in.
- **It is addressable.** `taskmgr guide --list` names the parts;
  `taskmgr guide <topic>...` prints only those, in the order you name them. A caller that
  only files issues asks for `loop`, and pays nothing for the filter language.

```bash
taskmgr guide --list             # what the parts are
taskmgr guide model loop         # just the two an issue-filing agent needs
taskmgr guide packages           # only this project's own conventions
```

The one thing that *is* an error is naming a built-in topic that does not exist — no
install can make it appear, so it is a typo worth catching. Naming a package topic that is
not installed here prints a line saying so and still exits `0`.

## The conventions it can rely on

- **`--json` on any command** gives stable `snake_case` output. Parse that; never scrape
  the human table, which is formatted for a terminal and free to change.
- **Exit `0` on success, `1` on any error.** The message goes to stderr prefixed
  `taskmgr: `, and stdout stays empty — so a failed command never leaves half a JSON
  document to parse.
- **Logs never mix into output.** `TASKMGR_LOG=debug` writes structured records to stderr;
  stdout stays clean.
- **It never prompts.** No confirmations, no interactive editors, no TTY required. Every
  answer comes from flags.
- **A misuse prints help.** A wrong argument or an unknown flag prints the command's usage
  and an example rather than a bare error, and an unknown command suggests the closest
  match.

## Two things a script gets wrong

- **Capture IDs; there is no way to derive one.** They are random tokens, so
  `--json | jq -r .id` is the only source ([Getting started](getting-started.md#file-a-task)
  has the pattern).
- **Write bodies through `--description-file -`, not `--description`,** which stores a
  literal backslash-n ([Concepts](concepts.md#the-description-body)). `comment add --file -`
  reads standard input the same way.

A mutation's `--json` echoes the issue's scalar fields, not its description or comments —
run `show` to confirm what landed.

## The loop worth prescribing

```bash
taskmgr ready --json                       # pick up work
taskmgr update <id> --status in_progress
taskmgr comment add <id> "…what was decided and why…"
taskmgr close <id> --reason "shipped in <commit>"
```

Prefer `close --reason` over `update --status closed`: the reason is the part a later
reader needs, and only `close` records it.

## Give the agent rules it cannot skip

An agent that files and closes its own work will skip a policy that lives in prose.
[Hooks](hooks.md) are the same policy as a gate: `pre-close` runs your check, denies with a
structured reason, and the agent reads the reason, fixes the work, and retries. That loop
needs no supervision, and there is no flag to bypass it.

**Teach the rule as well as enforcing it.** A gate alone costs a round trip per rule: the
agent files, is refused, and tries again. A package that carries a gate can also carry the
prose explaining it, and `taskmgr guide` prints that prose alongside its own — so the agent
reads the rule before its first attempt, from the same package and the same version as the
gate that enforces it. [Hooks](hooks.md) covers how to write one.

## Keep the tracker out of a repository the agent writes to

If the agent should not be touching `.tasks/` in the diff, promote the store out of the
project and it becomes invisible to git while every command keeps working:

```bash
taskmgr store move --central --to my-project
```

See [Where your tasks live](stores.md).
