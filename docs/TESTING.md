# Testing

## Running tests

```bash
make test       # both modules: test-sdk then test-cli
make test-sdk   # cd sdk && go test ./...
make test-cli   # go test ./...   (root module)
```

Both modules are tested separately because `sdk/` is its own Go module.

## Pre-commit gate

Before committing, run:

```bash
make fmt vet test
```

- `make fmt` — `gofmt -w` on `cmd`, `sdk/tasks`, `main.go`. Use `make fmt-check`
  to fail (rather than rewrite) on unformatted files.
- `make vet` — `go vet ./...` in both modules.
- `make test` — both suites.

There is no CI configured; this gate is manual.

## Test conventions

- Tests live next to the code as `sdk/tasks/*_test.go` (`store_test.go`,
  `ready_test.go`, `frontmatter_test.go`).
- Use a temp store: `newTestStore(t)` calls `Init(t.TempDir(), "agt")`, so tests
  never touch a real `.tasks/`.
- **Deterministic time.** Tests override `Store.now` with a fixed,
  monotonically-ticking clock so timestamps are reproducible — never assert
  against the wall clock.
- Use the `mustCreate(t, s, in)` helper for fixtures.
- Assert store errors with `errors.Is` against the exported sentinels
  (`ErrNotFound`, `ErrStoreExists`, `ErrNoStore`, …).
- Validation failures are `*ValidationError` carrying a `Field`; assert on that
  field when testing rejection paths.
