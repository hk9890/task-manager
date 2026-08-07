# Contributing to task-manager

Thanks for your interest in improving task-manager! This guide is the
human-readable on-ramp; the deeper engineering rules live in [`docs/`](docs/).

## Project layout

Two Go modules over one storage engine (see [docs/OVERVIEW.md](docs/OVERVIEW.md)):

- root (`.`) — the `taskmgr` CLI (cobra).
- `sdk/` — the dependency-light storage engine and SDK (`sdk/tasks`).

Local development uses a committed `go.work` workspace, so the CLI builds against
the in-tree SDK without a `replace` directive. You do not need to do anything to
enable it.

## Prerequisites

- **Go 1.26+** (pinned in `go.mod`).
- Optionally [`mise`](https://mise.jdx.dev): `mise install` provisions the toolchain
  CI uses, including golangci-lint 2.1.6. Without it you cannot reproduce the lint
  job locally.

## Build & test (the green gate)

```bash
mise run build         # -> ./bin/taskmgr  (or: make build)
make install           # build and put `taskmgr` on your $PATH
mise run quality:full  # fmt + vet + lint + every test layer + build/vet all modules
```

Run `quality:full` before pushing — it covers what CI checks.
Details: [docs/TESTING.md](docs/TESTING.md), which owns the gate.

## Where changes go

The single most important rule: **the SDK (`sdk/tasks`) is the only component
that writes the store.** The CLI is a thin layer over it. Before touching code,
read [docs/CODING.md](docs/CODING.md) — it covers the single-writer rule, the
three-module layout, and which kind of change belongs where.

## Keep the specs in sync

The behaviour of the CLI, SDK, storage format, query language, hooks, and config
is specified under [docs/specs/](docs/specs/). If you change behaviour, update the
relevant spec in the same change — the conformance tests check the code against
the specs.

## Pull requests

- **No direct commits to `main`.** Branch, push, and open a PR
  (see [docs/CHANGE-WORKFLOW.md](docs/CHANGE-WORKFLOW.md)).
- Keep PRs focused; write a clear description of the what and why.
- Make sure CI is green before requesting review.

## Reporting bugs & requesting features

Use the GitHub issue templates. For security issues, **do not** open a public
issue — follow [SECURITY.md](SECURITY.md) instead.

## License of contributions

This project is licensed under the Apache License 2.0. By submitting a
contribution, you agree that it is licensed under the same terms (Apache-2.0
§5 — inbound = outbound). No separate CLA is required.
