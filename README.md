# task-manager

[![CI](https://github.com/hk9890/task-manager/actions/workflows/ci.yml/badge.svg)](https://github.com/hk9890/task-manager/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/hk9890/task-manager/sdk/tasks.svg)](https://pkg.go.dev/github.com/hk9890/task-manager/sdk/tasks)
[![Latest release](https://img.shields.io/github/v/release/hk9890/task-manager?sort=semver)](https://github.com/hk9890/task-manager/releases)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

A lean, file-based task tracker — issues, dependencies, and ready-work, and
nothing else. No database, no server: every task is a Markdown file with YAML
frontmatter under a `.tasks/` directory, versioned alongside your code. You work
with it through **`taskmgr`**, the command-line tool, or embed the storage engine
directly via the **`sdk/tasks`** Go module.

- **Plain files** — every issue is one Markdown file you can read, grep, and diff.
- **Dependencies & ready-work** — model `blocked_by` / `parent` / `related` edges
  and ask "what can I work on now?" with `taskmgr ready`.
- **Single writer** — `taskmgr` (and the SDK) is the only thing that writes the
  store; it validates every field and serializes concurrent writers with an
  advisory lock, so the on-disk state can never go malformed.
- **Machine-readable** — add `--json` to any command for scripting and agents.

## Install

```bash
# 1. Go toolchain (Go 1.26+) — installs the latest tagged release onto your $PATH
go install github.com/hk9890/task-manager@latest

# 2. Prebuilt binary — download an archive for your OS/arch from the Releases page:
#    https://github.com/hk9890/task-manager/releases
#    (each release ships linux/macOS/Windows × amd64/arm64 + a checksums.txt)

# 3. From a checkout (Go 1.26+)
make install      # builds `taskmgr` and puts it on your $PATH
```

## Usage

```bash
cd your-project
taskmgr init --prefix proj                 # create .tasks/
taskmgr create --title "First task" --type task --priority 1
taskmgr create --title "Depends on it" --blocked-by proj-0001
taskmgr ready                              # what can I work on now?
taskmgr show proj-0001
taskmgr update proj-0001 --status in_progress
taskmgr close proj-0001 --reason "done"
taskmgr ready                              # the dependent is now ready
```

Add `--json` to any command for machine-readable output. Run `taskmgr guide` for
a built-in how-to and `taskmgr commands` for the full machine-readable catalog.

## Use as a Go library

The storage engine is an importable, dependency-light module
(`github.com/hk9890/task-manager/sdk/tasks` — only depends on `gopkg.in/yaml.v3`):

```bash
go get github.com/hk9890/task-manager/sdk@latest
```

```go
package main

import (
	"fmt"
	"log"

	"github.com/hk9890/task-manager/sdk/tasks"
)

func main() {
	store, err := tasks.Open("") // discover .tasks upward from the cwd
	if err != nil {
		log.Fatal(err)
	}

	res, err := store.Create(tasks.CreateInput{Title: "Fix nav", Type: tasks.TypeBug})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("created", res.Issue.ID)

	ready, err := store.Ready() // issues with no open blockers
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("ready:", len(ready))
}
```

See the [package reference on pkg.go.dev](https://pkg.go.dev/github.com/hk9890/task-manager/sdk/tasks).

## Documentation

- [docs/OVERVIEW.md](docs/OVERVIEW.md) — architecture and repository layout.
- [docs/specs/](docs/specs/) — the authoritative specs (CLI, SDK, storage format,
  query language, hooks, config, architecture).
- [CONTRIBUTING.md](CONTRIBUTING.md) — how to build, test, and submit changes.
- [docs/RELEASING.md](docs/RELEASING.md) — how releases are cut.
- [SECURITY.md](SECURITY.md) — how to report a vulnerability.

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE).
