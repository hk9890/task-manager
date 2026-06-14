# bench — scaling harness for the `.tasks` store

A standalone, reproducible benchmark that measures how the file-based store
scales and quantifies the payoff of two proposed changes:

- **Comments → append-only sidecars** (epic `at-zib.1`)
- **Cold-partition closed issues into `closed/`** (epic `at-zib.2`)

It is its **own Go module** (`replace` onto `../sdk`), so it never enters the
CLI module's dependency graph, `go build ./...`, or `make test`.

## Run

```bash
cd bench

go run .                  # synthetic corpus (default -n 419, ~90% closed) — portable, no data needed
go run . -n 2000          # larger synthetic corpus
go run . -jsonl PATH      # import a real issues.jsonl export instead of synthetic
go run . -mode scaling    # only current-design scaling phases
go run . -mode redesign   # only the sidecar + closed/ payoff phases
go run . -mode yaml       # only the YAML-escaping probe
```

The synthetic generator (`-jsonl` empty) mirrors a mature store: ~90% closed,
multi-paragraph bodies, and comments whose bodies carry trailing whitespace (to
exercise the YAML escaping path). It is deterministic.

## What it measures

| Phase | Question |
|---|---|
| SCALING / import | on-disk size vs source; how many comment bodies YAML mangles into escaped one-liners |
| SCALING / All() | cost of the full-store scan that runs **inside the write lock** on every Create/Update/AddDep |
| SCALING / op latency | `AddComment` (O(1)) vs `Update` (O(N) via `checkRefs`) |
| SCALING / concurrency | throughput at 1/4/8/16 concurrent writers (flock serialization) |
| SCALING / slope | `Create` latency as the store grows (campaign O(N) cost) |
| REDESIGN A/B/C | hot-set size + `All()` scan for current vs sidecar vs sidecar+`closed/` |
| REDESIGN / nextID | demonstrates the ID-reuse risk once `closed/` exists (bug `at-zib.2.1`) |
| YAML | which comment-body shapes force escaped single-line output |

## Headline results — real corpus (dtctl-test, 419 issues, 378 closed)

```
Layout                                   hot files   hot bytes   All() scan
A) current (inline comments, flat)             419     2.03 MB     14.0 ms
B) sidecar comments                            419     1.35 MB      7.1 ms
C) sidecar + closed/ (hot = open only)          41    148.0 KB      0.54 ms   (27x)
```

Other findings on the same corpus:

- **Write path is fully serialized at ~200 ops/s** (flock + in-lock `fsync`).
  16 concurrent writers get the same aggregate throughput as 1.
- **`Update` is ~4x `AddComment`** because it re-scans every file via `checkRefs`
  even when no refs changed.
- **`All()` already skips subdirectories**, so a `closed/` subdir drops out of the
  hot scan with no change to `All()` — but **`nextID` only reads the hot dir**, so
  it will re-allocate an in-use ID once closed issues move out (bug `at-zib.2.1`).
- **YAML escaping**: a comment body with **trailing whitespace, a whitespace-only
  line, or CRLF** forces `yaml.v3` off the readable block scalar and into a
  single-line double-quoted string with literal `\n`. Tabs and leading-space
  indentation are fine. 8/419 real files already degraded this way.

Numbers are machine-dependent (disk `fsync` latency dominates the write path);
re-run locally for absolute figures. The *ratios* are stable.
