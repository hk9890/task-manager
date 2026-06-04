# AGENTS.md — agent-tasks routing

## Repository purpose

A lean, file-based task tracker: issues, dependencies, and ready-work as Markdown
files under `.tasks/`. Ships a CLI (`atctl`) and an importable Go SDK
(`sdk/tasks`) over one storage engine. Go, two modules (cobra CLI + a
dependency-light SDK).

## Use-case routing

Depending on your goal, load the relevant document first.

### Research, planning, analysis

Load [docs/OVERVIEW.md](docs/OVERVIEW.md) for the architecture and repository
layout. For the full data model, ready/blocked semantics, locking, and
validation rules, read [SPEC.md](SPEC.md).

### Coding and file changes

Load [docs/CODING.md](docs/CODING.md) before changing code — it covers the
single-writer rule, the two-module layout, and where each kind of change goes.

### Testing and verification
>
Load [docs/TESTING.md](docs/TESTING.md) to run the suites and the pre-commit gate.

### Commit, branch, PR workflow

Run `make fmt vet test` first (see [docs/TESTING.md](docs/TESTING.md)), then use
the **commit-commands** skill (`commit`, `commit-push-pr`) for commits and PRs.
There is no repo-specific git policy beyond the green-build gate.
