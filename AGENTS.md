# AGENTS.md — task-manager routing

## Repository purpose

A lean, file-based task tracker: issues, dependencies, and ready-work as Markdown files
under `.tasks/`. Two Go modules — the cobra CLI (`taskmgr`) and a dependency-light SDK
(`sdk/tasks`) — over one storage engine, which is the only code that touches the store.

## Use-case routing

Every route below is **mandatory, not advisory**. Load the document BEFORE the first
action of that kind — loading it afterwards does not count, and no route becomes
skippable because the change looks small.

### Coding and file changes

**MUST read [docs/CODING.md](docs/CODING.md) before creating or editing ANY file under
`cmd/`, `sdk/`, or `scripts/`.** Where a change goes is not guessable from the tree: the
storage engine is the only writer, and most changes owe an update to a spec.

### Research, planning, architecture — and finding anything at all

**MUST read [docs/OVERVIEW.md](docs/OVERVIEW.md) before your first `rg`, `grep`, `Glob`
or `ls` of the source tree, and before writing any plan or design for a change.** It is
the map, and it carries the search expressions that land on the right file first time.

### Writing project docs

**MUST read [docs/DOCUMENTING.md](docs/DOCUMENTING.md) AND invoke the
`instruction-writing:writing-project-docs` skill before creating or editing ANY Markdown
file under `docs/`, or `AGENTS.md`, `CLAUDE.md`, `README.md`, or `CONTRIBUTING.md`.**
A doc change is gated here, and some gaps in the set are deliberate.

### Testing and verification

**MUST read [docs/TESTING.md](docs/TESTING.md) before writing a test, before your first
`mise run test*` or `go test`, and before reporting a change as green.** Which layer a
test belongs in, and which gate makes a change green, are both fixed there.

### Running `taskmgr` by hand, or reproducing a reported bug

**MUST read [docs/RUNNING.md](docs/RUNNING.md) before running a `taskmgr` binary against
any store, and before reproducing a reported bug by hand.** A command run from this
repository writes to a real store.

### Reviewing a PR or a diff

**MUST read [docs/REVIEWING.md](docs/REVIEWING.md) before your first `git diff` or
`gh pr diff` run to judge a change, and whenever a review is requested.** It carries what
a review must cover on top of the `code-review` skill, and what is not a finding here.

### Commit, branch, worktree, PR, merge

**MUST read [docs/CHANGE-WORKFLOW.md](docs/CHANGE-WORKFLOW.md) before any git command
that writes** — commit, branch, worktree, push — **before opening a PR, and before
merging one.** There are no direct commits to `main`.

### Release

**MUST read [docs/RELEASING.md](docs/RELEASING.md) before cutting a release**, and before
editing `.goreleaser.yaml`, `.github/workflows/release.yml`, or the `sdk` version pinned
in `go.mod`.

### Diagnosing a failure, or inspecting what a past run did

**MUST read [docs/MONITORING.md](docs/MONITORING.md) before reading a captured
`TASKMGR_LOG` run, and before your first edit made in response to a failed `taskmgr`
command.** Most of what looks like a bug is visible in one run's records first.
