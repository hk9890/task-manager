# AGENTS.md — agent-tasks routing

## Repository purpose

A lean, file-based task tracker: issues, dependencies, and ready-work as Markdown
files under `.tasks/`. Ships a CLI (`atctl`) and an importable Go SDK
(`sdk/tasks`) over one storage engine. Three Go modules: the cobra CLI, a
dependency-light SDK, and a standalone `bench` harness.

## Use-case routing

Depending on your goal, load the relevant document first.

### Research, planning, analysis

Load [docs/OVERVIEW.md](docs/OVERVIEW.md) for the architecture, repository layout,
and links to the specs.

### Coding and file changes

**You MUST load [docs/CODING.md](docs/CODING.md) before you touch any code** — it
covers the single-writer rule, the two-module layout, where each kind of change
goes, and keeping the specs in sync.

### Testing and verification

**You MUST load [docs/TESTING.md](docs/TESTING.md), run the suites, and confirm the
project is green** before handoff.

### Change, commit & release workflow

Follow [docs/CHANGE-WORKFLOW.md](docs/CHANGE-WORKFLOW.md) for branching, worktrees,
and pull requests — **no direct commits to `main`**. For version tags and release
artifacts, see [docs/RELEASING.md](docs/RELEASING.md).
