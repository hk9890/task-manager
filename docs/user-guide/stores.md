# Where your tasks live

By default a store sits in the project it tracks. It does not have to. This page covers
both arrangements, how `taskmgr` decides which one answers, and how to move between them.

## The default: a store in the project

`taskmgr init` creates `.tasks/` in the current directory and you commit it. Every command
walks **up** from where you are standing until it finds a `.tasks/`, so any directory in
the project works.

This is the arrangement to want. The tracker branches with the code, a pull request shows
the task changes beside the diff, and a clone brings the backlog with it.

## The alternative: a central store

Sometimes the tracker should not be in the repository — you are tracking work on a
repository you do not own, or on something that is not a repository at all, or you simply
do not want `.tasks/` in the diff.

```bash
taskmgr init --central --store-name my-project
```

That creates the store under `~/.taskmgr/stores/my-project/` and records, in a registry,
that this project directory is tracked by it. Afterwards `taskmgr` works exactly as before
from inside the project — the store is just somewhere else.

A central store is byte-for-byte an ordinary store. Nothing about the files or the commands
changes; only where the directory sits.

## How a directory picks its store

In order:

1. `--store-name <name>` — an explicit override, naming a registered central store.
2. **A local `.tasks/` found by walking up.** A local store always wins.
3. **The registry** — the entry whose project path is the closest ancestor of where you
   are standing.
4. Nothing, and the command says so.

```bash
taskmgr where          # kind (local / central / override_name / none), store path, project path
taskmgr where --json
taskmgr store list     # every registered project → store mapping, and its health
```

`where` never fails and never writes. Run it whenever you are unsure — especially before a
command that writes.

Each row of `store list` carries a health:

| | Means |
|---|---|
| `ok` | a usable store |
| `dangling` | the entry is registered, its store directory is gone — commands resolve past it |
| `broken` | the directory is there without its `config.yaml` — commands stop and say so |

A `dangling` or `broken` row is an entry to repair or delete by hand, not a store to
select. Both are what a half-finished move leaves behind.

A health is read from the store directory, so if `store list` cannot read one it says so
and exits instead of printing a row: a directory it was refused is not a directory that
is gone, and only one of the two is repaired by deleting the entry.

## Working on another project's store

`--store-name` is not only the tie-breaker above. It works on every command, read and write
alike, so any registered store answers from anywhere.

```bash
taskmgr store list                        # the names to choose from
taskmgr --store-name reports ready        # read another project's work
taskmgr --store-name reports create --title "Export drops the BOM" --type bug
```

Prefer it to `-C <path>`: it takes a name, not the other project's location. This is what
lets an agent file a bug against a project it is not working in.

Two things change once the store is not the one you are standing in:

- **IDs belong to one store.** The prefix comes from the project directory name, so two
  projects with the same directory name share one. `--json` output carries the store name
  on every issue — keep the two together, and pass them together.
- **Hooks are the target project's.** A write runs the hooks configured in the store you
  named, with that project as the working directory, so a `create` can trigger checks in a
  repository you are not standing in ([Hooks](hooks.md)).

Only central stores are reachable by name. A project tracked by a local `.tasks/` is
reachable with `-C <path>`, or promote it — below.

## Moving a store

```bash
taskmgr store move --central --to my-project   # promote the local store here to central
taskmgr store move --rename  --to new-name     # rename the central store here
taskmgr store move --relink  --to my-project   # this directory is where that store's project moved to
```

The store travels whole, `config.yaml` included, so the ID prefix and the list of hook
packages survive and every existing ID stays valid.

Two things to know before promoting:

- **The local `.tasks/` is gone afterwards.** There is no confirmation prompt, and nothing
  touches git — committing the removal is yours to do.
- **A package kept inside the store moves with it.** A hook that runs a script from its own
  package keeps working, because the script is found relative to the package
  ([Hooks](hooks.md)). A package named by name lives under your taskmgr home instead, so it
  is per machine and unaffected either way.

There is no `unlink` command. The registry is one short YAML file at
`~/.taskmgr/mapping.yaml`; delete an entry by hand.

## Configuration

| What | Where |
|---|---|
| Your taskmgr home | `~/.taskmgr/`, or `$TASKMGR_HOME` if set (must be an absolute path) |
| Global config | `<home>/config.yaml` — `central_root`, a fallback `hook_timeout`, and the hook packages applied to every store on this machine |
| The registry | `<central_root>/mapping.yaml` |
| Central stores | `<central_root>/stores/<name>/` |
| A store's own config | `<store>/config.yaml` — the ID prefix, `hook_timeout`, and this project's hook packages |
| Log level | `$TASKMGR_LOG` = `debug` / `info` / `warn` (default) / `error`, always to stderr |

Both config files are editable by hand and through `taskmgr config`:

```bash
taskmgr config keys                    # every settable key, and which file it belongs to
taskmgr config list                    # this store's config, with its path
taskmgr config list --global           # yours, machine-wide
taskmgr config set hook_timeout 5m
```

`--global` selects the per-user file and resolves no store, so it works from any directory.
The list of hook packages is a list rather than a value and has its own verbs,
`taskmgr package` — see [Hooks](hooks.md).

There is no home to create up front: everything has a built-in default, and the files are
written only by a command that needs to persist central state.

`$TASKMGR_DIR` is **rejected**, not ignored. It used to point at a store directory; a
command fails outright if it is set, rather than quietly writing into whichever store the
walk-up happened to find. Unset it, and name the store with `--store-name` or run from
inside the project.
