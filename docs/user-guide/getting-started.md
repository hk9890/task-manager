# Getting started

Install `taskmgr`, create a store in your project, and run one task from filed to closed.

## Install

```bash
go install github.com/hk9890/task-manager/cmd/taskmgr@latest   # needs Go 1.26+
taskmgr version
```

Or download an archive for your OS and architecture from the
[releases page](https://github.com/hk9890/task-manager/releases) and put the binary on your
`PATH`. Each release ships Linux, macOS and Windows builds for amd64 and arm64, a
`checksums.txt`, an SBOM per archive, and a cosign signature over the checksums.

## Create a store

```bash
cd your-project
taskmgr init --prefix proj
```

That creates `.tasks/` in the current directory and gives every issue in it the ID prefix
`proj`. Without `--prefix`, the prefix is derived from the directory name.

**Commit `.tasks/`** — it is your tracker, and keeping it in the repository is the whole
point: tasks branch and merge with the code. If you would rather keep it out of the
repository, see [Where your tasks live](stores.md).

Every command from here on finds that store by walking up from the directory you are
standing in, so you can run `taskmgr` from anywhere inside the project. `taskmgr where`
tells you which store answered, and why.

## File a task

```bash
taskmgr create --title "Add CSV export" --type feature --priority 1
```

Only `--title` is required. The command prints the new issue's ID — something like
`proj-3k9f2x`. IDs are **random, not sequential**: never guess or construct one. When you
are scripting, capture it:

```bash
id=$(taskmgr create --title "Define the export schema" --json | jq -r .id)
```

## Link one task to another

```bash
taskmgr create --title "Wire the export button" --blocked-by "$id"
```

The second task cannot be worked on until the first is closed. That is what makes the next
command useful.

## Find what to work on

```bash
taskmgr ready     # open issues with no open blockers — what you can start now
taskmgr blocked   # what is waiting, and on what
taskmgr list      # every active issue
```

```
ID           P   TYPE     STATUS  TITLE
proj-3k9f2x  P1  feature  open    Add CSV export
proj-8mq04b  P2  task     open    Define the export schema
```

## Work it, and close it

```bash
taskmgr show "$id"
taskmgr update "$id" --status in_progress
taskmgr comment add "$id" "Chose RFC 4180 quoting to match the reports module."
taskmgr close "$id" --reason "shipped in a1b2c3d"

taskmgr ready     # the task it was blocking is now ready
```

Closing moves the file into `.tasks/closed/`, which keeps everyday commands fast as
history grows. Nothing is deleted, and `taskmgr reopen <id>` brings it back.

## Where to go next

- `taskmgr guide` — the same workflow as a printable brief, straight from the binary.
- `taskmgr <command> --help` — one command's flags, with an example.
- [Concepts](concepts.md) — what types, priorities and the three kinds of link actually
  mean.
- [Filtering and search](queries.md) — once `list` returns more than a screen.
