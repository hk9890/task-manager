# agent-tasks

A lean, file-based task tracker — the things we actually use [beads] for (issues,
dependencies, ready-work), and nothing else. No database: every task is a
Markdown file under a `.tasks/` directory in your repo.

It ships two front-ends over one engine:

- **`atctl`** — the command-line tool (for agents and humans).
- **`sdk/tasks`** — an importable Go SDK, so a viewer (e.g. [beads-workbench])
  can read and write the same store directly.

The SDK is the **only** code that touches the files. It validates every change
and serializes writers, so nothing malformed lands on disk.

## Quick start

```bash
# build
make build            # -> ./bin/atctl

cd your-project
atctl init --prefix proj                 # create .tasks/
atctl create --title "First task" --type task --priority 1
atctl create --title "Depends on it" --blocked-by proj-0001
atctl ready                              # what can I work on now?
atctl show proj-0001
atctl update proj-0001 --status in_progress
atctl close proj-0001 --reason "done"
atctl ready                              # the dependent is now ready
```

Add `--json` to any command for machine-readable output.

## On-disk format

One Markdown file per issue, YAML frontmatter + markdown body:

```markdown
---
id: proj-0002
title: Depends on it
status: open
type: task
priority: 2
blocked_by: [proj-0001]
created: 2026-06-04T15:00:00Z
updated: 2026-06-04T15:00:00Z
---

## Description
...
```

Only `parent`, `blocked_by`, and `related` are stored; the inverse edges
(children, "blocks") are derived, never written. See [SPEC.md](SPEC.md) for the
full model, ready/blocked semantics, locking, and validation rules.

## Layout

```
.                      root module: the atctl CLI (cobra)
├── cmd/               command implementations
└── sdk/               separate module: the importable engine
    └── tasks/         package tasks — model, store, ready, validation
```

## Development

```bash
make build      # build ./bin/atctl
make test       # run CLI + SDK tests
make fmt vet    # format and vet both modules
```

## Using the SDK

```go
import "github.com/hk9890/agent-tasks/sdk/tasks"

store, err := tasks.Open("")            // find .tasks upward from cwd
iss, err := store.Create(tasks.CreateInput{Title: "Fix nav", Type: tasks.TypeBug})
ready, err := store.Ready()
```

[beads]: https://github.com/steveyegge/beads
[beads-workbench]: https://github.com/hk9890/beads-workbench
