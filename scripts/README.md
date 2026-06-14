# scripts

## `import_beads.py` — migrate a beads tracker into a `.tasks` store

A beads→task-manager **adapter**: it translates a beads JSONL export into
task-manager *import envelopes* and feeds them to `taskmgr import --batch`, which
validates each record and writes it (the single writer). All beads-specific
knowledge lives in the script; taskmgr knows nothing about beads.

Each issue is written as a complete **end-state** via `taskmgr import`, so the
original timestamps, closed state, and full comment log are preserved in one pass
— no create→update→close replay.

```bash
# easiest — via mise (builds taskmgr, runs in your current dir):
mise run import-beads            # prompts before overwriting an existing .tasks
mise run import-beads -- -y      # overwrite without prompting

# or directly:
mise run build                   # produces ./bin/taskmgr (used by the script)
python3 scripts/import_beads.py --prefix at
python3 scripts/import_beads.py --from beads.jsonl --dir /path/to/project
python3 scripts/import_beads.py --dry-run    # print envelopes, write nothing
```

### Re-importing
taskmgr mints the IDs, so a clean re-import means starting from a fresh store
(re-importing into a populated one would duplicate every issue). If a `.tasks`
store already exists in the target it **asks whether to delete and re-import**;
pass `-y/--yes` to skip the prompt.

Flags: `--from FILE` (default: runs `bd export`), `--dir DIR` (target holding
`.tasks`, default cwd), `--prefix P` (ID prefix; default derived from the dir
name), `--yes/-y`, `--taskmgr PATH` (default `<repo>/bin/taskmgr`), `--map-out
FILE`, `--dry-run`.

### What it maps
- **IDs are minted by taskmgr**, not the adapter — each issue is imported with no
  `id`, so the store allocates a fresh random token (beads ids like `at-zib.1.1`
  aren't valid task-manager ids, so they can't be reused verbatim). The original
  id is preserved as a **`beads:<id>` label** (which also marks the issue as
  imported), and a `source_id → new-id` map is written to `--map-out` (default
  `scripts/.beads-import-map.json`, gitignored). Issues import one at a time in
  dependency order so each edge can reference the freshly minted id of an
  already-imported parent/blocker.
- **Timestamps and comments** (created/updated/closed; comment author + time) are
  imported verbatim.
- **Labels** are slugified to fit the label grammar (spaces → `-`); an
  unsalvageable label is dropped with a warning.
- **Statuses** outside taskmgr's set (e.g. `deferred`) map to `open`, preserved
  as an `imported-status:<s>` label.
- **Edges**: issues import in dependency order, so `parent`/`blocked_by` always
  resolve; a `related` edge whose target imports later (or forms a cycle) is
  skipped and counted.
- **Control characters** (e.g. ANSI escapes in comments) are stripped — taskmgr's
  validator rejects them, so the adapter sanitizes source data to fit the model.

The summary line reports how many issues imported and the counts dropped/skipped.
