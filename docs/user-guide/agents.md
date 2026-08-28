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

## Capture IDs, never construct them

IDs are random tokens (`proj-3k9f2x`), not counters. There is no "next" ID and no way to
guess one:

```bash
id=$(taskmgr create --title "Add CSV export" --type feature --json | jq -r .id)
taskmgr create --title "Wire the button" --blocked-by "$id"
```

## Write bodies through a file, not a flag

`--description "a\nb"` stores the backslash-n literally. For anything multi-line, read
standard input:

```bash
taskmgr update "$id" --description-file - <<'EOF'
## Acceptance criteria
- [ ] handles an empty result set
EOF

echo "scaffold pushed" | taskmgr comment add "$id" --file -
```

`update --description` replaces the body rather than appending. To amend, read it back with
`taskmgr show <id> --json` and resubmit the whole thing.

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

## Keep the tracker out of a repository the agent writes to

If the agent should not be touching `.tasks/` in the diff, promote the store out of the
project and it becomes invisible to git while every command keeps working:

```bash
taskmgr store move --central --to my-project
```

See [Where your tasks live](stores.md).
