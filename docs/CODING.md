# Coding

Repository-specific implementation constraints. Read [OVERVIEW.md](OVERVIEW.md)
for the layout and [SPEC.md](../SPEC.md) for the model first.

## Build and run

```bash
make build      # -> ./bin/atctl  (version stamped via -ldflags)
make install    # build atctl onto $PATH ($GOBIN, else $GOPATH/bin)
make fmt vet    # gofmt -w + go vet, both modules
```

Toolchain: Go 1.26 (see `go.mod`).

## The single-writer rule

`sdk/tasks` is the **only** package that may read or write files under `.tasks/`.
`cmd/` and any external viewer go through the `Store` API. Do not open, stat, or
write issue files from `cmd/` or anywhere else — that bypasses validation,
locking, and the atomic-write path.

## Module layout

- The root module `github.com/hk9890/agent-tasks` (the CLI) depends on the SDK
  module via a `replace ... => ./sdk` directive in `go.mod`.
- `sdk/` is a separate module with minimal dependencies (only `yaml.v3`). Do not
  add CLI-only or heavy dependencies to `sdk/go.mod`.
- `bench/` is a third, standalone module (`replace`d onto `../sdk`) holding the
  scaling harness. Keep it outside the CLI's dependency graph: it is excluded
  from `go build ./...` and `make test`, and `make tidy` does not touch it.
- After changing imports in the root or `sdk` module, run `make tidy` (runs
  `go mod tidy` in both).

## Where changes go

- **New CLI command** → add to `cmd/`, grouped by kind (`create.go`, `query.go`,
  `mutate.go`, `show.go`, `init.go`) and wired in `root.go`. Commands call the
  `Store`, never the filesystem.
- **New stored field or behavior** → `sdk/tasks` (`model.go` for the type,
  `frontmatter.go` for serialization, `validate.go`/`store.go` for the rules),
  then expose it through the CLI DTOs in `cmd/render.go`.
- **Output** → human rendering and the snake_case JSON DTOs both live in
  `cmd/render.go`. Every command supports `--json`; keep the JSON shapes stable
  for agent consumers.

## Storage invariants to preserve

- **Validation is layered.** `validate.go` checks self-contained invariants
  (non-empty title, known enums, priority range, no self/duplicate edges).
  Referential checks (referenced IDs exist, no dependency cycles) need the whole
  graph and live in `store.go`. Put new rules on the correct side.
- **Never persist derived edges.** Only `parent`, `blocked_by`, and `related`
  are written; children and "blocks" are derived.
- **Atomic writes only.** Mutations run through the store's lock + temp-file +
  fsync + rename path (`withLock`). Don't write issue files directly.
- **Timestamps** are UTC truncated to whole seconds (`defaultNow`) for readable,
  minimal git diffs.
