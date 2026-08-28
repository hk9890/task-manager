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

A change is not green until `quality:full` passes; it covers everything
`.github/workflows/ci.yml` checks, so a green gate should mean a green PR. There is
no `make lint` — linting is `mise run lint`.

## Conventions

- Tests sit next to the code (`sdk/tasks/*_test.go`).
- Fixtures: `sdk/tasks/internal/storetest` — `.Mem()` for L2, `.TempDir(t)` for L3.
  Never hand-roll a real `.tasks/`. `cmd/` tests can't import it; they use the
  public `Store` API, or the built binary at L4.
- **Deterministic time:** `Store.now` inside package `tasks`, `Store.SetNow` from
  `cmd/` and external consumers. Never assert the wall clock.
- Assert errors with `errors.Is` against the sentinels; validation failures are
  `*ValidationError` with a `Field`.

## Spec conformance

The CLI and SDK must match the specs in `docs/specs/`
([CLI-SPEC](specs/CLI-SPEC.md), [SDK-SPEC](specs/SDK-SPEC.md),
[TASK-STORAGE-SPEC](specs/TASK-STORAGE-SPEC.md)). A behaviour change updates the
spec in the same change; a mismatch is a bug.
