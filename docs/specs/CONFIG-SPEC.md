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
home — `mapping.yaml` and the central store subfolders (§3).

On the first run of **any** command the engine creates the home and a default
`config.yaml` if missing (eager, idempotent). A read-only command may therefore create
it on first run — expected and harmless.

---

## 2. Global config — `config.yaml`

One YAML document at `<home>/config.yaml`:

```yaml
version: 1
central_root: ~/.taskmgr   # registry + central stores live here; ~ expands
```

| Field | Required | Notes |
|---|---|---|
| `version` | no | Schema version; defaults to `1`. |
| `central_root` | no | Directory holding the registry and central stores. `~` expands; a relative value resolves against the home. Defaults to the home. |

`config.yaml` always lives in the home, even when `central_root` points elsewhere.
Unknown keys are ignored; a corrupt (unparseable) file is a hard error.

---

## 3. Central root & registry — `mapping.yaml`

The **central root** (default = the home) is a plain directory — **not a store**. It
holds the registry and one subfolder per central store; each subfolder is a complete,
ordinary store per TASK-STORAGE-SPEC (own `config.yaml`, prefix, hot files, `comments/`,
`closed/`). Because a central store is an ordinary store, relocating one is a folder
move (§5).

```
~/.taskmgr/
├── config.yaml          # §2
├── mapping.yaml         # the registry (below)
└── my-project/          # a central store — a complete, ordinary store
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
    store: my-project        # the store's subfolder name under central_root
```

- Every entry **maps to a path**; `path` and `store` are both required (there is no
  project-less entry). `store` is a single path segment; the store lives at
  `<central_root>/<store>`.
- `path` may use `~`/relative form; it is canonicalized only at compare time (§4).
- A **missing** `mapping.yaml` means "no central stores" (not an error); a **corrupt**
  one is a hard error; a **duplicate** canonical `path` across entries is an error.

**Integrity** (surfaced by `taskmgr store list`, CLI-SPEC §2): a **dangling** entry
(missing subfolder, or a project `path` that no longer exists) is ignored by resolution;
an **orphan** subfolder (no entry) is unreachable by path but still openable via
`--store-name`.

---

## 4. Store resolution

Map a working directory `W` (plus optional override) to one store, in order:

1. **Explicit override** — `--store-path` / `TASKMGR_DIR` opens that path directly;
   `--store-name` opens `<central_root>/<name>` via the registry (error if it has no
   entry). Path and name are mutually exclusive; a flag beats the environment variable.
2. **Local walk-up** (unchanged) — from `W` upward, the first `.tasks/` found wins and
   resolution stops. This is why a local store always beats a central one.
3. **Central fallback** — no local store: canonicalize `W` and pick the registry entry
   whose canonical `path` is the **longest** ancestor-of-or-equal-to `W`; open its
   store. Dangling entries are skipped.
4. **None** → `ErrNoStore` (the CLI renders this actionably; `taskmgr where` shows the
   outcome).

**Canonicalization** (step 3): expand a leading `~`, make absolute (registry paths
against `central_root`, `W` against the working directory), resolve symlinks where the
path exists, then clean. Matching is ancestor/longest-prefix on **segment** boundaries
(so `/a/projectX` is not a child of `/a/project`), mirroring local walk-up.

---

## 5. Creation & relocation

- **Create local** (unchanged) — a `.tasks/` store in the project tree; no registry.
- **Create central** — create `<central_root>/<store>` as an ordinary store **and** add
  its registry entry in one step.
- **Move** — relocate a store and update the registry together (local↔central or
  central→central): the folder carries all its data; only the registry entry is
  rewritten.

**Prefix.** A store's ID prefix is `--prefix` if given, else derived from the project
directory name (lowercased, non-alphanumerics stripped, leading digits removed,
truncated), else `task`. Prefixes are **per project** — there is deliberately no global
default prefix, so two projects never share a prefix by accident.

Registry writes obey the store durability discipline (TASK-STORAGE-SPEC §7): serialized
under an advisory `flock` on the central root and written atomically, so concurrent
`store` operations never corrupt `mapping.yaml`.
