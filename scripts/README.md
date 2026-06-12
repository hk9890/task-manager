# scripts

## `import_beads.py` — migrate a beads tracker into a `.tasks` store

One-off importer: reads a beads JSONL export and recreates every issue in an
task-manager `.tasks` store by driving `taskmgr` (the validated single writer).
Preserves title, description, type, priority, labels, assignee, `parent` /
`blocked_by` edges, comments, and closed state.

```bash
# easiest — via mise (builds taskmgr, runs in your current dir):
mise run import-beads            # prompts before overwriting an existing .tasks
mise run import-beads -- -y      # overwrite without prompting

# or directly:
mise run build                   # produces ./bin/taskmgr (used by the script)
python3 scripts/import_beads.py --prefix at
python3 scripts/import_beads.py --from beads.jsonl --dir /path/to/project
python3 scripts/import_beads.py --dry-run    # preview, write nothing
```

### Re-importing
The import is **additive** (it appends), so a clean re-import means starting from
a fresh store. The script handles that for you: if a `.tasks` store already exists
in the target it **asks whether to delete and re-import**. Pass `-y/--yes` to skip
the prompt (e.g. for the mise task or CI).

Flags: `--from FILE` (default: runs `bd export`), `--dir DIR` (target holding
`.tasks`, default cwd), `--prefix P` (ID prefix for the new store), `--yes/-y`
(overwrite without asking), `--taskmgr PATH` (default `<repo>/bin/taskmgr`),
`--map-out FILE`, `--dry-run`.

### What to expect
- **IDs are reallocated** — beads ids (`at-zib.1.1`) aren't valid task-manager ids.
  The script remaps every `parent`/`blocked_by` edge and writes a
  `beads-id → new-id` map (default `scripts/.beads-import-map.json`, gitignored).
- **Timestamps** — `created`/`updated`/`closed` are set at import time; the
  original beads timestamps are preserved in a footer appended to each issue body.
- **`related` edges** are skipped (task-manager only sets `related` at create time).
- Closed beads issues land directly in `.tasks/closed/`.
