# Change Workflow

All feature work happens on a branch in a git worktree and lands through a pull
request. **No direct commits to `main`.**

## Branch + worktree per feature

Use an isolated worktree so `main` stays clean and parallel work can coexist:

```bash
git worktree add ../agent-tasks-<feature> -b <feature>
cd ../agent-tasks-<feature>
```

When the change has landed, remove it:

```bash
git worktree remove ../agent-tasks-<feature>
```

## Change loop

1. Track the work in the repo's own `.tasks/` store with `atctl` (`atctl create`
   to open an issue, `atctl close` when it lands).
2. Make the change on the feature branch — never on `main`.
3. Verify to the depth of the change:
   - **Docs-only** → check touched paths, links, and routes.
   - **Code** → run the green gate `make fmt vet test` (see [TESTING.md](TESTING.md)).
   - **Behaviour change** → update the matching spec in `docs/specs/` in the same
     change (see [CODING.md](CODING.md)).
4. Commit on the branch.

## Land via pull request

```bash
git push -u origin <feature>
gh pr create --fill
```

- The branch must be green (`make fmt vet test`) before review.
- Merge the PR into `main`; do not push to `main` directly.
- Update tracker state with `atctl`: close finished issues and file follow-ups.

## Session completion

Work is not done until the branch is pushed, the PR is open or merged, and the
tracker state is synced. For version tags and release artifacts, see
[RELEASING.md](RELEASING.md).
