# Configuration & Store Resolution Specification

Defines the **per-user configuration** and how a working directory is mapped to a
single store. It complements [TASK-STORAGE-SPEC.md](TASK-STORAGE-SPEC.md) (one store
in isolation) by specifying what sits *around* stores: a per-user home, an optional
central store root, and the resolution rule. Resolution is owned by the engine
(`sdk/tasks`), so every front end resolves identically (Go API:
[SDK-SPEC.md](SDK-SPEC.md) §1; CLI surface: [CLI-SPEC.md](CLI-SPEC.md) §1–§2).

Key rules:

- **Local always wins** — the central registry is consulted only when no local
  `.tasks` is found by walk-up.
- **One source of truth per store** — a local store is identified by its location, a
  central store by its registry entry; never both.
- **No guessing** — wherever a store is named, the caller states explicitly whether it
  is a path or a registry name.
- **Friendly to write, canonical to compare** — registry paths may use `~`/relative
  form; equality is decided only after canonicalization (§4).

---

## 1. The taskmgr home

Per-user state lives under the **home**: `~/.taskmgr/` by default, or `$TASKMGR_HOME`
(an absolute path) if set. It holds `config.yaml` (§2) and — when `central_root` is the
home — `mapping.yaml` and the `stores/` directory of central stores (§3).

The engine ships **built-in defaults** for everything in §2, so a missing home or
`config.yaml` is not an error — the defaults apply. The read path never writes: the
home and `config.yaml` are created/written only by a command that must persist central
state (e.g. `init --central`). This keeps reads side-effect-free (ARCHITECTURE-SPEC §6,
TASK-STORAGE-SPEC §7) and avoids two first-runs racing to create the home.

---

## 2. Global config — `config.yaml`

One YAML document at `<home>/config.yaml`:

```yaml
version: 1
central_root: ~/.taskmgr   # registry + central stores live here; ~ expands

hook_timeout: 2s           # fallback limit for a store that sets none
hooks:                     # lifecycle gates for every store on this machine
  - id: doc-needs-path
    event: pre-create
    when: 'type == "doc" && !(label ~ "path:")'
    run: ["/home/me/.taskmgr/hooks/doc-path.sh"]
```

| Field | Required | Notes |
|---|---|---|
| `version` | no | Schema version; defaults to `1`. |
| `central_root` | no | Directory holding the registry and central stores. `~` expands; a relative value resolves against the home. Defaults to the home. |
| `hook_timeout` | no | Fallback per-hook wall-clock limit for a store that sets none ([HOOK-SPEC](HOOK-SPEC.md) §3.1). A store's own value wins. |
| `hooks` | no | Lifecycle gates applied to **every** store on this machine, running before the store's own. Same schema as a store's hooks block; semantics in [HOOK-SPEC](HOOK-SPEC.md) §3.5. |

`config.yaml` always lives in the home, even when `central_root` points elsewhere.
Unknown keys are ignored; a corrupt (unparseable) file is a hard error.

`hook_timeout` and `hooks` are validated **lazily, on the first write to any
store** — never on a read — exactly as a store's own hooks block is
([HOOK-SPEC](HOOK-SPEC.md) §3.4). The blast radius is wider than a store's:
a malformed block here fails mutations in *every* store on the machine while
leaving every query working. `taskmgr config` validates before it writes, so the
error normally surfaces at the command that caused it.

**Machine-local, by construction.** A store travels in git; this file does not.
A gate configured here therefore applies to you and not to a colleague or to CI,
which makes it the right home for personal ergonomics — a reminder, a
notification, a local lint — and the wrong home for any rule the data's integrity
depends on. Those belong in the store's own `config.yaml`
([TASK-STORAGE-SPEC](TASK-STORAGE-SPEC.md) §4.2), which is committed with the
repository.

---

## 3. Central root & registry — `mapping.yaml`

The **central root** (default = the home) is a plain directory — **not a store**. It
holds the registry, an advisory lock (below), and a `stores/` directory with one
subfolder per central store; each subfolder is a complete, ordinary store per
TASK-STORAGE-SPEC (own `config.yaml`, prefix, hot files, `comments/`, `closed/`). The
dedicated `stores/` directory keeps store names in their own namespace, so a store can
never collide with the central root's own files (`config.yaml`, `mapping.yaml`,
`.lock`). Because a central store is an ordinary store, relocating one is a plain folder
move plus a registry edit (§5).

```
~/.taskmgr/
├── config.yaml          # §2
├── mapping.yaml         # the registry (below)
├── .lock                # advisory flock for registry writes (empty; only its lock state matters)
└── stores/              # all central stores live here
    └── my-project/      # a central store — a complete, ordinary store
        ├── config.yaml
        ├── myp-3k9f2x.md
        ├── comments/
        └── closed/
```

The registry is one YAML document at `<central_root>/mapping.yaml`:

```yaml
version: 1
stores:
  - path: ~/dev/my-project   # project this store tracks (friendly form allowed)
    store: my-project        # the store's subfolder name under stores/
```

- Every entry **maps to a path**; `path` and `store` are both required (there is no
  project-less entry). The store lives at `<central_root>/stores/<store>`.
- **`store` name grammar.** A single path segment matching `^[A-Za-z0-9][A-Za-z0-9._-]*$`,
  1–64 characters. The leading-alphanumeric rule excludes path separators, `.`, `..`,
  and hidden names; the length cap keeps it a sane directory name on every filesystem.
- `path` may use `~`/relative form; it is canonicalized only at compare time (§4).
- Both keys are **unique** across entries: a duplicate canonical `path` is an error (a
  path can map to only one store), and a duplicate `store` name is an error (so
  `--store-name` selects exactly one entry).
- A **missing** `mapping.yaml` means "no central stores" (not an error); a **corrupt**
  one is a hard error.

**Dangling vs. broken entries.** The two are handled differently and must not be
conflated:

- **Dangling** — the `store` subfolder does not exist at all. **Ignored** by resolution
  (§4) rather than failing the command, so a hard-killed promote leaves the project able
  to resolve elsewhere instead of wedged.
- **Broken** — the subfolder exists but holds no `config.yaml`, so it is not a finished
  store. Resolution **reports** it, naming the folder and what is missing. Skipping it
  would treat a store whose config went missing as if the project had no store at all,
  while every issue file is still sitting there: the command fails with "no store", and
  the advice that comes with that — run `taskmgr init` — creates a second, empty store
  beside the real one and splits the project's issues across the two.

A missing project `path` is neither: an entry whose project directory was deleted or
moved still matches and opens its central store. A subfolder with no registry entry is
simply unreachable until an entry is added (§5).

`--store-name` reports a broken store the same way, since the caller named something
that does exist in the registry.

**Enumeration** classifies without opening: `Stores` labels every entry `ok`,
`dangling` or `broken` from a `stat` (SDK-SPEC §1, surfaced by `store list`,
CLI-SPEC §2.1). The vocabulary is the one above, so a listing and a resolution never
disagree about an entry — a caller building a store switcher sees the entries that
will not open before it offers them.

Note that a *published* half-built store is not a state the tooling produces: a promote
assembles the tree under a staging name and publishes it with one atomic rename (§5), so
a broken folder means external interference or a store edited by hand.

**Registry lock.** Writes to `mapping.yaml` are serialized by an advisory `flock` on
`<central_root>/.lock` — an empty file whose only role is its lock state, mirroring a
store's `.tasks/.lock` (TASK-STORAGE-SPEC §4.5). The central root is not itself a store,
so it carries this separate lock for registry mutations.

---

## 4. Store resolution

Map a working directory `W` (plus optional override) to one store, in order. `W` is the
caller-provided resolution origin — for the CLI, `--dir`/`-C` if given, else the cwd
(SDK: `ResolveOptions.WorkDir`) — and the **same** `W` drives every step below:

0. **Withdrawn overrides** — `TASKMGR_DIR` is **rejected**: if it is set to anything
   non-empty, resolution fails naming it. It once pointed resolution at a store
   directory outright; the override is gone along with the `--store-path` flag that
   mirrored it. The flag now fails as an unknown flag, but an environment variable has
   no such backstop — left merely unread, a CI job or direnv profile that exports it to
   pin a store keeps exiting 0 while every issue lands in whatever store the walk-up
   happens to find.
1. **Explicit override** — `--store-name` opens `<central_root>/stores/<name>` via the
   registry (error if it has no entry). There is no path-based override: every store is
   reached through walk-up or the registry, so the project a store tracks is always
   known.
2. **Local walk-up** (unchanged) — from `W` upward, the first `.tasks/` found wins and
   resolution stops. This is why a local store always beats a central one.
3. **Central fallback** — no local store: canonicalize `W` and pick the registry entry
   whose canonical `path` is the **longest** ancestor-of-or-equal-to `W`; open its
   store. Dangling entries (§3) are skipped before the match. The winning entry is then
   required to be a finished store: a broken one is reported, not skipped past to a
   shorter ancestor — the entry that owns the directory is the one that answers for it.
4. **None** → `ErrNoStore` (the CLI renders this actionably; `taskmgr where` shows the
   outcome).

**Canonicalization** (step 3): expand a leading `~`, make absolute (registry paths
against `central_root`, `W` against the working directory), resolve symlinks where the
path exists, then clean. Matching is ancestor/longest-prefix on **segment** boundaries
(so `/a/projectX` is not a child of `/a/project`), mirroring local walk-up.

---

## 5. Creation & relocation

- **Create local** (unchanged) — a `.tasks/` store in the project tree; no registry.
- **Create central** — create `<central_root>/stores/<store>` as an ordinary store
  **and** add its registry entry in one step (the `init --central` command, CLI-SPEC §2).
- **Promote local → central** — move an existing `<project>/.tasks` to
  `<central_root>/stores/<store>` and register it (`store move --central`, CLI-SPEC §2.1).
  The store moves whole, `config.yaml` included, so its prefix and hooks block survive
  and existing IDs stay valid. Hook **argv** is not rewritten: hooks run with the
  project root as their working directory (HOOK-SPEC §3.2), which the promote does not
  change, so a hook whose argv points into `.tasks` must be rewritten by hand.
  The **registry entry is written before the files move**, so a promote that dies in
  between leaves a dangling entry (ignored by resolution, §3) plus the local store,
  which still exists and still wins step 2 — the project keeps working. The reverse
  order would leave a store nothing points at. If the move *returns an error* the entry
  is rolled back, so the command can simply be run again; only a hard kill leaves the
  entry behind.
  The **files are published by one atomic rename**. The tree is moved into a staging
  directory beside the destination (a name `validStoreName` rejects, so it can never be
  resolved as a store) and renamed into place only once it is whole. Copying straight to
  the destination publishes it a piece at a time on the cross-filesystem path: the entry
  is already live, so the moment `config.yaml` lands another process resolves the
  half-copied folder as a finished store and writes into it — under that folder's own
  `.lock`, which is a different file from the one the promote holds on the source, so
  locking the source serializes nothing. A failed attempt therefore leaves its partial
  copy at the staging path rather than at the destination, where it would block every
  retry (the name is taken, and so is the project path under any other name); the next
  attempt clears it.
- **Rename a central store** — rename the subfolder under `stores/` and update the
  entry's `store` field (`store move --rename`). Always within one filesystem, so the
  folder move is a plain rename. The folder moves first; if the registry write fails
  the folder is moved back, leaving the untouched entry still pointing at it.
- **Re-link a moved project** — update an entry's `path` to the project's new location
  (`store move --relink`). A pure registry edit, held to the same checks as the writers
  that create entries. It refuses when the store subfolder is not a **finished** store —
  the same test resolution applies (§3), not merely "is a directory", or relink would
  report success on an entry the very next command skips — when the new project path
  does not exist (canonicalization (§4) falls back to the lexical form for a path that
  is not there, so an unchecked typo would point a live entry at nothing), and when
  another entry already claims the project. That last check matches on **both** the raw
  and the symlink-resolved path, exactly as entry creation does: entries are only ever
  compared lexically, so matching the resolved form alone would let a project registered
  before its path became a symlink be claimed a second time, and the duplicate would be
  invisible to the registry's own lexical dedup.
- **Unlink** — dropping an entry has no dedicated verb; the registry is one short YAML
  file the user can hand-edit.

**Cross-filesystem moves.** A store folder is moved by rename where possible and by
recursive copy plus removal of the source when the project and the central root sit on
different filesystems. The copy path is **not** atomic and is not rolled back: a failure
leaves the partial copy at the staging path for inspection, with the source untouched.
The copy is strict — an entry that is neither a directory nor a regular file aborts it —
so the source is only ever removed after a complete copy. Because the copy targets the
staging name and only the final rename publishes it, the destination store name is never
occupied by a partial result.

**Prefix.** A store's ID prefix is `--prefix` if given, else derived from the project
directory name (lowercased, non-alphanumerics stripped, leading digits removed,
truncated to 8 characters), else `task`. Prefixes are **per project** — there is deliberately no global
default prefix, so two projects never share a prefix by accident.

Registry writes (today, `init --central`) obey the store durability discipline
(TASK-STORAGE-SPEC §7): serialized under the `<central_root>/.lock` advisory `flock` (§3)
and written atomically, so concurrent writers never corrupt `mapping.yaml`.
