# Testing

`mise` is the canonical command surface — it is the only one that runs the
linter and the integration layer. `make` is a fallback for the fast inner loop
and covers strictly less; see [Command surfaces](#command-surfaces).

For the L1–L4 layer model, the `vfs.Mem`-vs-real-disk proof boundary, and the
fixture builder, read
[implementation/TESTING-STRATEGY.md](implementation/TESTING-STRATEGY.md).

## Running tests

```bash
mise run test              # L1 pure + L2 store-on-Mem (fast, both modules)
mise run test:integration  # L3 real temp dir + L4 CLI (requires -tags=integration)
mise run test:all          # every layer, both modules
```

L3 and L4 are behind the `integration` build tag, so a plain `go test ./...`
silently skips them — including the whole CLI suite. The raw equivalent is
`go test -race -tags=integration ./...`, run once in the root module and once in
`sdk/`.

`sdk/` is its own module, so the suites always run separately.

## Gates

```bash
mise run quality       # vet + lint + L1/L2 + build/vet all modules + fuzz seeds
mise run quality:full  # the above plus L3/L4 — required before handoff
```

`quality` is the pre-commit gate; `quality:full` is the pre-handoff gate. A
change is not green until `quality:full` passes.

CI (`.github/workflows/ci.yml`) runs **more** than `quality:full`, not the same
set: `-race` on every test step, an `sdk`-wide `gofmt -l .` (broader than the
`cmd sdk/tasks` paths the local fmt tasks cover), a bench build and vet, fuzz
seed corpora, and a separate `lint` job on the pinned golangci-lint. Passing the
local gate makes a red PR unlikely, not impossible.

## Command surfaces

| | `mise` | `make` |
|---|---|---|
| fmt / vet | `mise run fmt`, `mise run vet` | `make fmt`, `make fmt-check`, `make vet` |
| L1 + L2 | `mise run test` | `make test` |
| L3 + L4 | `mise run test:integration` | *not covered* |
| lint | `mise run lint` | *not covered* |
| gates | `mise run quality`, `quality:full` | *not covered* |

`make fmt-check` fails instead of rewriting. There is deliberately no
`make lint`: golangci-lint is pinned in `.mise.toml`, and a recipe-less `lint`
target used to exit 0 without linting anything.

## Conventions

- Tests sit next to the code (`sdk/tasks/*_test.go`).
- Build fixtures with `sdk/tasks/internal/storetest` — `storetest.New(t).Issue(…)`
  then `.Mem()` for an L2 in-memory store or `.TempDir(t)` for an L3 real one.
  Never hand-roll a real `.tasks/`. `cmd/` tests cannot import the internal
  package; they drive the public `Store` API (or the built binary, at L4).
- **Deterministic time:** never assert the wall clock. Inside package `tasks`,
  override the unexported `Store.now`; from `cmd/` and external consumers, call
  `Store.SetNow`.
- Assert errors with `errors.Is` against the sentinels; validation failures are
  `*ValidationError` with a `Field`.

## Spec conformance

The CLI and SDK must match the specs in `docs/specs/`
([CLI-SPEC](specs/CLI-SPEC.md), [SDK-SPEC](specs/SDK-SPEC.md),
[TASK-STORAGE-SPEC](specs/TASK-STORAGE-SPEC.md)). A behaviour change updates the
spec in the same change; a mismatch is a bug.
