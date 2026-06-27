# Testing

## Running tests

```bash
make test       # both modules: test-sdk then test-cli
make test-sdk   # cd sdk && go test ./...
make test-cli   # go test ./...   (root module)
```

`sdk/` is its own module, so the suites run separately.

## Pre-commit gate

```bash
make fmt vet test
```

Green before every commit (`make fmt-check` fails instead of rewriting). CI
(`.github/workflows/ci.yml`) re-runs the gate on every push and pull request —
fmt-check, vet, test, and integration tests for both modules, plus a build — so
the same checks are enforced automatically.

## Conventions

- Tests sit next to the code (`sdk/tasks/*_test.go`); use a temp store
  (`Init(t.TempDir(), …)`), never a real `.tasks/`.
- **Deterministic time:** override `Store.now`; never assert the wall clock.
- Assert errors with `errors.Is` against the sentinels; validation failures are
  `*ValidationError` with a `Field`.

## Spec conformance

The CLI and SDK must match the specs in `docs/specs/`
([CLI-SPEC](specs/CLI-SPEC.md), [SDK-SPEC](specs/SDK-SPEC.md),
[TASK-STORAGE-SPEC](specs/TASK-STORAGE-SPEC.md)). A behaviour change updates the
spec in the same change; a mismatch is a bug.
