# scripts

## `import_beads.py` — migrate a beads tracker into a `.tasks` store

One-off importer: reads a [beads](https://github.com/) JSONL export and recreates
every issue in an agent-tasks `.tasks` store by driving `atctl` (the validated
single writer). Preserves title, description, type, priority, labels, assignee,
`parent` / `blocked_by` edges, comments, and closed state.

```bash
mise run build                              # produces ./bin/atctl (used by the script)

# import into ./.tasks, creating the store:
python3 scripts/import_beads.py --init --prefix at

# or from a saved export into another project:
bd export -o beads.jsonl
python3 scripts/import_beads.py --from beads.jsonl --dir /path/to/project

python3 scripts/import_beads.py --dry-run    # preview, write nothing
```

Flags: `--from FILE` (default: runs `bd export`), `--dir DIR` (target holding
`.tasks`, default cwd), `--atctl PATH` (default `<repo>/bin/atctl`), `--init
[--prefix P]`, `--map-out FILE`, `--dry-run`.

### What to expect
- **IDs are reallocated** — beads ids (`at-zib.1.1`) aren't valid agent-tasks ids.
  The script remaps every `parent`/`blocked_by` edge and writes a
  `beads-id → new-id` map (default `scripts/.beads-import-map.json`).
- **Timestamps** — `created`/`updated`/`closed` are set at import time; the
  original beads timestamps are preserved in a footer appended to each issue body.
- **`related` edges** are skipped (agent-tasks only sets `related` at create time;
  beads exports rarely carry them). A warning is printed if any are found.
- Closed beads issues land directly in `.tasks/closed/`.
