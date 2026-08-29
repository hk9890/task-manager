# Testing

`mise` is the canonical surface; `make` covers only fmt/vet and L1/L2. Layer model
and fixtures: [implementation/TESTING-STRATEGY.md](implementation/TESTING-STRATEGY.md).

```bash
mise run test              # L1 pure + L2 store-on-Mem (fast, both modules)
mise run test:integration  # L3 real temp dir + L4 CLI
mise run test:all          # every layer
mise run check:docs        # the doc gate (docs/DOCUMENTING.md)
mise run quality           # vet + lint + docs + L1/L2 + build/vet all modules + fuzz  (pre-commit)
mise run quality:full      # + L3/L4                                                  (pre-handoff)
```

`make` fallback: `make test` (both modules), `make test-sdk` / `make test-cli` for
one, `make vet`, `make fmt`, `make fmt-check` (fails instead of rewriting).

L3 and the subprocess half of L4 sit behind the `integration` build tag, so a
plain `go test ./...` skips them. Raw equivalent:
`go test -race -tags=integration ./...`, once per module. The CLI's in-process
tests — anything asserting on what a command printed — run untagged through
`cmd.Run`; see [implementation/TESTING-STRATEGY.md](implementation/TESTING-STRATEGY.md).

The tag also carries the few L2 tests whose **cost** puts them out of the fast
loop — `sdk/tasks/edge_bounds_slow_test.go`, which fills a relation list to its
256-item bound twice. Tag an L2 test for cost only when it dominates the default
suite, name the reason in its file comment, and keep it in `quality:full`.

A change is not green until `quality:full` passes; it covers everything
`.github/workflows/ci.yml` checks **except** the vulnerability scan, which runs
only in CI because it reaches the network: run `mise run vuln` by hand to check it
early. There is no `make lint` — linting is `mise run lint`.

Two coverage tasks are **manual** and gate nothing. `mise run test:coverage` runs
every layer against a threshold (`COVERAGE_THRESHOLD`, default 75) — a policy
nobody has adopted, so it stays available without failing anyone's build.
`mise run test:coverage:summary` ranks both modules per-function, least-covered
first, and is the one to reach for: it names the lines no test executes, which is
the gap a kill-rate cannot show.

Read either as a pointer, never as a verdict. A covered line is one that **ran**,
not one whose behaviour anything asserted — this repository has already shipped
three tests named for a directory fsync that asserted only that the call returned
nil. Coverage says where to look; the assertion is still the work.

## Conventions

- Tests sit next to the code (`sdk/tasks/*_test.go`).
- Fixtures: `sdk/tasks/internal/storetest` — `.Mem()` for L2, `.TempDir(t)` for L3,
  `.NewRawFixture(t, root)` to write raw bytes into a `.tasks/` tree (malformed
  frontmatter, a hand-seeded `closed/` entry). Never hand-roll a real `.tasks/`:
  the raw fixture is what you reach for instead. `cmd/` tests can't import it;
  they use the public `Store` API, or the built binary at L4.
- **Deterministic time:** `Store.now` inside package `tasks`, `tasks.WithClock`
  at construction from `cmd/` and external consumers. Never assert the wall clock,
  and never set a clock after construction — a test that must move time gives its
  `WithClock` closure state the test still owns.
- **Naming:** a test function is `TestSubject_Behaviour`
  (`TestRemoveDep_Absent_WritesNothing`), and a test file separates words with
  underscores (`store_move_test.go`), except where it mirrors a source file of the
  same name. Both were the tree's habit before they were written down here, which
  is why a reviewer could not apply either.
- A test that touches a real disk is L3 and belongs in an `_l3_test.go` file
  behind `//go:build integration`, whatever the subject is. The default
  `mise run test` must stay disk-free.
- Assert errors with `errors.Is` against the sentinels; validation failures are
  `*ValidationError` with a `Field`.
- **Durability:** assert the parent-directory fsync through the `fsyncDirFn` seam
  in `sdk/tasks/internal/vfs`, from an internal test file in package `vfs`. A test
  that only checks the call returned nil passes with the fsync deleted, because a
  lost directory entry shows up on the next crash and never in the return value.

## Spec conformance

The CLI and SDK must match the specs in `docs/specs/`
([CLI-SPEC](specs/CLI-SPEC.md), [SDK-SPEC](specs/SDK-SPEC.md),
[TASK-STORAGE-SPEC](specs/TASK-STORAGE-SPEC.md)). A behaviour change updates the
spec in the same change; a mismatch is a bug.
