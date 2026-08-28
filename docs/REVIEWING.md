# Reviewing

**Local delta —** run the `code-review` skill for the correctness pass; this file is what a
task-manager review must cover on top of it. Each rule below names the document that owns
it: read that, and flag what the change does not respect. Where the two disagree, this
file wins.

## Blocking

- **The normative spec for the touched area was updated in the same change.**
  [CODING.md § Keep specs in sync](CODING.md#keep-specs-in-sync) maps the area to its spec.
  The `spec_*_conformance_test.go` suites cover the sections named in their own headers and
  nothing more, so an un-updated spec is a review finding, not a test failure.
- **A file added to the `imperativeShell` map in `sdk/tasks/importboundary_test.go`.**
  The guard passes either way — that is the point of the map — so this is where the
  pure-core boundary erodes. A file belongs on the list only when it genuinely cannot do
  its job over plain values;
  [ARCHITECTURE-SPEC §5](specs/ARCHITECTURE-SPEC.md#5-the-engine) is the standard.
  Adding one to make a build pass is a blocking finding.
- **A disk, process, or environment call outside its seam** — an inline `os.Getenv` or
  `os/exec` in `sdk/tasks`, blocking even where the guard test does not reach it
  ([CODING.md § Single-writer rule](CODING.md#single-writer-rule) names the three seams).
- **A mutation that lengthens the in-lock path.** Pre-hooks and the write already run
  under the store `flock` ([HOOK-SPEC §8](specs/HOOK-SPEC.md)); work added there serializes
  every other writer, in every process.

## Also check

- **Test layer.** Pure logic tests at L1 — which means it declares no `*Store` method, or
  it cannot be tested there at all. Fixtures come from `sdk/tasks/internal/storetest`;
  a hand-rolled `.tasks/` tree in a test is a finding. Time is `Store.SetNow`, never the
  wall clock ([TESTING.md § Conventions](TESTING.md#conventions)).
- **User-facing docs, in the same PR.** A new or changed command, flag, or JSON field
  reaches [user-guide/](user-guide/); a new package reaches
  [OVERVIEW.md § Repository layout](OVERVIEW.md#repository-layout). `check:docs` only
  proves a cited path still exists — it can never prove a new one is cited.
- **Cross-module compatibility.** A change to a `sdk/tasks` exported symbol that `cmd/`
  now depends on has to survive `mise run verify:pin` at release time
  ([RELEASING.md](RELEASING.md)); flag one that will not.

## Not a finding

- Anything `mise run quality:full` already rejects — formatting, `go vet`,
  golangci-lint, a failing layer. Report only what a green gate would still ship broken.
- Naming and style the linter accepts. The enabled set is deliberately small
  (`.golangci.yml`); a preference it does not encode is a suggestion, not a blocker.
- A missing `write` log record for a comment, dependency or related-link edit. Those are
  not lifecycle transitions and log nothing by design
  ([MONITORING.md § What `write` covers](MONITORING.md#what-write-covers)).
