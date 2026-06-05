# Testing Strategy

Four layers, split by *what they touch*. The seam that makes the split possible is
`internal/vfs.FS` (see [PACKAGE-OVERVIEW.md](PACKAGE-OVERVIEW.md)): pure logic needs
no disk, the shell is tested on an in-memory FS, and a real temp dir is the source
of truth for durability.

## Layers

| Layer | Touches | Covers | Run |
|---|---|---|---|
| **L1 pure unit** | nothing (no `os`, no `vfs`) | query lex/parse/eval (fake `Row`s), frontmatter byte round-trips, validate tables, ready/blocked graph, comment `resolve` | `mise run test` |
| **L2 store on Mem** | `vfs.Mem` | `Store` CRUD orchestration, `nextID` across partitions, close/reopen, **fault injection** (forced rename/append failure → no torn state) | `mise run test` |
| **L3 integration** | real `t.TempDir()` (`osFS`) | real `fsync`/`flock`/`rename`; full lifecycle; **reload via a fresh `Open()` and re-assert** | `mise run test:integration` |
| **L4 CLI** | temp store + cobra | command → JSON DTO golden | `mise run test:integration` |

L1/L2 are the default build (fast, no tag); L3/L4 are gated behind the
`integration` build tag.

## Boundaries

- `vfs.Mem` proves **logic and error handling**, not durability — `fsync`/`flock`
  are no-ops in memory. **L3 is the only layer that proves atomic writes and
  cross-process locking.** Don't assert durability against the mock.
- Run L1/L2 on every change; run L3/L4 before handoff.

## Fixtures — one builder, two backends

`internal/storetest` builds a populated store from a spec, materialized into
`vfs.Mem` (L2, instant) or a real `t.TempDir()` (L3). Same fixture, both layers:

```go
st := storetest.New(t).
    Issue("agt-0001", storetest.Open, storetest.Parent("agt-0007")).
    Closed("agt-0007").
    Comment("agt-0001", "hans", "first note")
store := st.Mem()        // L2: in-memory, instant
store := st.TempDir(t)   // L3: materialized on real osFS
```

## Conventions

- Tests sit next to the code (`*_test.go`). Never hand-roll a real `.tasks/`.
- Deterministic time only — override `Store.now`; never assert the wall clock.
- Assert sentinels with `errors.Is`; field failures are `*ValidationError` (`Field`);
  query parse failures are `*ParseError` (`Pos`).
- **TDD:** with the harness in place, write the layer-appropriate failing test
  first, then implement.

## Commands

`mise run test` (L1+L2) · `mise run test:integration` (L3+L4) ·
`mise run test:all` · `mise run lint` · `mise run quality` (vet+lint+test) ·
`mise run quality:full` (+ integration).
