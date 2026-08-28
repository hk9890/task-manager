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
taskmgr store list     # every registered project → store mapping
```

`where` never fails and never writes. Run it whenever you are unsure — especially before a
command that writes.

## Moving a store

```bash
taskmgr store move --central --to my-project   # promote the local store here to central
taskmgr store move --rename  --to new-name     # rename the central store here
taskmgr store move --relink  --to my-project   # this directory is where that store's project moved to
```

The store travels whole, `config.yaml` included, so the ID prefix and any hooks survive and
every existing ID stays valid.

Two things to know before promoting:

- **The local `.tasks/` is gone afterwards.** There is no confirmation prompt, and nothing
  touches git — committing the removal is yours to do.
- **Hook commands are not rewritten.** Hooks run with the project root as their working
  directory, which the move does not change, so a hook whose command points into `.tasks/`
  stops resolving. Rewrite it to an absolute path, or use `$TASKMGR_STORE`
  ([Hooks](hooks.md)).

There is no `unlink` command. The registry is one short YAML file at
`~/.taskmgr/mapping.yaml`; delete an entry by hand.

## Configuration

| What | Where |
|---|---|
| Your taskmgr home | `~/.taskmgr/`, or `$TASKMGR_HOME` if set (must be an absolute path) |
| Global config | `<home>/config.yaml` — `central_root`, a fallback `hook_timeout`, and hooks applied to every store on this machine |
| The registry | `<central_root>/mapping.yaml` |
| Central stores | `<central_root>/stores/<name>/` |
| A store's own config | `<store>/config.yaml` — the ID prefix, `hook_timeout`, and this project's hooks |
| Log level | `$TASKMGR_LOG` = `debug` / `info` / `warn` (default) / `error`, always to stderr |

Both config files are editable by hand and through `taskmgr config`:

```bash
taskmgr config keys                    # every settable key, and which file it belongs to
taskmgr config list                    # this store's config, with its path
taskmgr config list --global           # yours, machine-wide
taskmgr config set hook_timeout 5m
```

`--global` selects the per-user file and resolves no store, so it works from any directory.
Hooks are a list rather than a value and have their own verbs — see [Hooks](hooks.md).

There is no home to create up front: everything has a built-in default, and the files are
written only by a command that needs to persist central state.

`$TASKMGR_DIR` is **rejected**, not ignored. It used to point at a store directory; a
command fails outright if it is set, rather than quietly writing into whichever store the
walk-up happened to find.
