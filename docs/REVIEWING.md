# Reviewing

**Local delta —** run the `code-review` skill for the correctness pass; this file is what a
task-manager review must cover on top of it. A rule below states its condition in full, or
names the document that owns it: read that document, and flag what the change does not
respect. Where the skill and this file disagree, this file wins.

## Blocking

- **The normative spec for the touched area was updated in the same change.** Flag a
  behaviour or structural change whose spec the diff leaves unchanged; a mismatch between
  code and spec is a bug. The touched area names the spec:
  - a CLI command or flag, or a public `sdk/tasks` function, type or semantics → the
    matching one of [CLI](specs/CLI-SPEC.md), [SDK](specs/SDK-SPEC.md),
    [STORAGE](specs/TASK-STORAGE-SPEC.md), [QUERY](specs/QUERY-SPEC.md);
  - hook events, hook config or payloads → [HOOK](specs/HOOK-SPEC.md);
  - the per-user config, the central registry, or store resolution →
    [CONFIG](specs/CONFIG-SPEC.md); the project `config.yaml` →
    [STORAGE](specs/TASK-STORAGE-SPEC.md) §4.2;
  - a structural change (packages, a seam) →
    [ARCHITECTURE](specs/ARCHITECTURE-SPEC.md) §5.

  The `spec_*_conformance_test.go` suites cover the sections named in their own headers and
  nothing more, so an un-updated spec is a review finding, not a test failure.
- **A file added to `mayDeclareStoreMethods`, `mayImportVFS`, `mayImportEnv` or
  `mayImportExec` in `sdk/tasks/importboundary_test.go`.** The guard passes either way — that is the point of
  the maps — so this is where the pure-core boundary erodes. A file belongs on a list only
  when it genuinely cannot do its job over plain values, and being on one list is not a
  reason to add it to the other;
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
  a hand-rolled `.tasks/` tree in a test is a finding. Time is `tasks.WithClock` at
  construction, never the wall clock and never a setter afterwards
  ([TESTING.md § Conventions](TESTING.md#conventions)). A test that touches a real
  disk without the `integration` tag is a finding: it puts L3 in the fast suite.
- **User-facing docs, in the same PR.** A change a user would notice — a new or changed
  command, flag, JSON field or error message — updates the page covering it in
  [user-guide/](user-guide/); a new package reaches
  [OVERVIEW.md § Repository layout](OVERVIEW.md#repository-layout). Flag a diff that has
  the change and not the page. `check:docs` only proves a cited path still exists — it can
  never prove a new one is cited.
- **Cross-module compatibility.** A change to a `sdk/tasks` exported symbol that `cmd/`
  now depends on has to survive `mise run verify:pin` at release time
  ([RELEASING.md](RELEASING.md)); flag one that will not.

## Quality rules

How finished code must look. Each rule is a condition to check on the diff, with the
correct form.

- **Flag structs**: a command in `cmd/` keeps its flag values in its own struct, named
  `<command>Flags` — `closeFlags`, `commentAddFlags`. Flag a struct that two sibling
  subcommands read; give each its own. The persistent-flag struct of a group, read by
  each subcommand of that group, is no finding
  ([CODING.md § Where changes go](CODING.md#where-changes-go) owns that mechanism), and
  neither is a struct type that two commands each hold in a variable of their own
  (`filterFlags` behind `listFlags` and `searchFlags`).
- **Package-level flag variables**: the only ones in `cmd/` are the `<command>Flags`
  structs, the persistent-flag struct of a group (`configFlags`), and the root persistent
  flags `flagJSON`/`flagDir`/`flagStoreName`. Flag any other; move it into the struct of
  its command. A variable that holds flag names and no flag value (`createFromSetFlags`)
  is no flag variable.

## Not a finding

- Anything `mise run quality:full` already rejects — formatting, `go vet`,
  golangci-lint, a failing layer. Report only what a green gate would still ship broken.
- Naming and style the linter accepts and no rule above states. The enabled set is
  deliberately small (`.golangci.yml`); a preference neither encodes is a suggestion, not
  a blocker.
- A missing `write` log record for a comment, dependency or related-link edit. Those are
  not lifecycle transitions and log nothing by design
  ([MONITORING.md § What `write` covers](MONITORING.md#what-write-covers)).
