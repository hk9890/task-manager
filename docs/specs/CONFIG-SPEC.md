# Configuration & Store Resolution Specification

This document specifies the **per-user configuration** and the **store-resolution
algorithm**: the global config file, the central store root and its registry, and
the exact rule by which `taskmgr` (or any other front end) decides *which* store a
command operates on.

It complements the on-disk **store** format ([TASK-STORAGE-SPEC.md](TASK-STORAGE-SPEC.md)),
which defines a single `.tasks/` store in isolation. This spec defines what sits
*around* stores: how a user keeps stores outside a project tree and how a working
directory is mapped to one.

Resolution is owned by the engine (`sdk/tasks`), not the CLI, so every front end —
the CLI, a future `taskmgr-ui`, an HTTP server — resolves identically. The Go API
is in [SDK-SPEC.md](SDK-SPEC.md) §1; the CLI surface is in [CLI-SPEC.md](CLI-SPEC.md)
§1–§2.

---

## 1. Principles

1. **One function answers "which store?"** A front end calls a single resolver and
   gets back the store for the current working directory (or an explicit override).
   That is the entire contract a consumer needs — see SDK-SPEC §1.
2. **Local first, central as fallback.** Today's behaviour is preserved exactly: a
   `.tasks/` found by walking up from the working directory always wins. The central
   registry is consulted **only** when no local store is found.
3. **A store is described in exactly one place.** A local store is identified by its
   filesystem location; a central store is identified by its registry entry. No
   store is described twice, so the two can never contradict each other (this is why
   there is no embedded "project path" inside a store's own `config.yaml`).
4. **No guessing.** Wherever a store is named, the caller states *explicitly*
   whether the name is a filesystem path or a registry store name. Path-vs-name is
   never inferred.
5. **Friendly to write, canonical to compare.** The registry may hold human-written
   paths (`~`, relative); equality is decided only after canonicalization (§5).
6. **Self-creating, never surprising.** The global config is created with defaults
   on first run. It carries only what configuration genuinely requires; unknown keys
   are ignored, never rejected (forward-compatible, matching the store config policy).

---

## 2. The taskmgr home

Per-user state lives under the **taskmgr home**:

| | |
|---|---|
| **Default** | `~/.taskmgr/` |
| **Override** | the `TASKMGR_HOME` environment variable (an absolute path) |
| **Contents** | `config.yaml` (the global config, §3) and — when `central_root` is the home — `mapping.yaml` plus the central store subfolders (§4) |

The home is resolved once per process. The user's home directory is obtained from
the OS (`$HOME` on Unix); `TASKMGR_HOME`, if set and non-empty, replaces the whole
`~/.taskmgr` path.

**Eager creation.** On the first run of *any* command, if the home or its
`config.yaml` is missing, the engine creates the home directory and writes a
`config.yaml` populated with defaults (§3). Creation is idempotent and cheap; it is
not deferred to the first central-store operation. A read-only command therefore may
create the home on its first ever run — this is expected and harmless.

---

## 3. Global config — `config.yaml`

| | |
|---|---|
| **Path** | `<taskmgr-home>/config.yaml` |
| **Role** | Per-user configuration. Exactly one. |
| **Format** | A single YAML document. |

```yaml
# ~/.taskmgr/config.yaml  (auto-created on first run)
version: 1
central_root: ~/.taskmgr     # where central stores + mapping.yaml live; ~ expands
default_prefix: tsk          # ID prefix used when creating a central store without one
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `version` | int | no | Schema version for forward-compatibility. Defaults to `1`. |
| `central_root` | path | no | Directory holding the registry and the central store subfolders. `~` expands; a relative value is resolved against the home. Defaults to the taskmgr home itself. |
| `default_prefix` | string | no | `^[a-z][a-z0-9]*$`, max 32. Fallback ID prefix at store creation when none is given and none can be derived from the project name; it replaces the built-in `task` fallback. When unset, creation falls back to `task`. |

Notes:

- The config file always lives in the **home**, even when `central_root` points
  elsewhere. Only the registry and the store subfolders follow `central_root`.
- Unknown keys are ignored on read, never rejected. A **corrupt** `config.yaml`
  (unparseable YAML) is a hard error — it is the user's configuration and must not be
  silently discarded.

---

## 4. Central root & registry — `mapping.yaml`

The **central root** (`central_root` from §3, default = the home) is a plain
directory. It is **not itself a store**. It holds the registry and one subfolder per
central store; each subfolder is a complete, ordinary store per TASK-STORAGE-SPEC
(its own `config.yaml`, prefix, hot files, `comments/`, `closed/`). Because a central
store is an ordinary store, relocating one is just moving its folder (§6).

```
~/.taskmgr/                       # central root (default = the home)
├── config.yaml                   # global config (§3)
├── mapping.yaml                  # the registry (this section)
└── my-project/                   # a central store — a complete, ordinary store
    ├── config.yaml               #   its own prefix, hooks, …
    ├── myp-3k9f2x.md
    ├── comments/
    └── closed/
```

### 4.1 Registry schema

| | |
|---|---|
| **Path** | `<central_root>/mapping.yaml` |
| **Role** | Maps a project path to the central store that tracks it. Exactly one. |
| **Format** | A single YAML document. |

```yaml
# ~/.taskmgr/mapping.yaml
version: 1
stores:
  - path: ~/dev/my-project   # the project this store tracks (friendly form allowed)
    store: my-project        # the store's subfolder name under central_root
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `version` | int | no | Schema version; defaults to `1`. |
| `stores` | list | no | Registry entries. Absent or empty means "no central stores yet". |
| `stores[].path` | path | yes | The project path this store tracks. May be written in friendly form (`~`, relative); canonicalized only at compare time (§5). |
| `stores[].store` | string | yes | The store's subfolder name under `central_root`. A single path segment (no separator), conventionally the project's basename. |

Rules:

- **Every entry maps to a path.** There is no project-less / "inbox" entry; a store
  not associated with any project is out of scope for this spec.
- **`store` is the subfolder name**, so the store lives at `<central_root>/<store>`.
- A **missing** `mapping.yaml` means there are no central stores — equivalent to an
  empty `stores` list (no error). A **corrupt** `mapping.yaml` is a hard error.
- **Duplicate canonical `path`** across two entries is invalid (a path cannot map to
  two stores) and is reported as an error when the registry is loaded for resolution.

### 4.2 Integrity

The registry and the subfolders can drift through manual edits. Resolution is
defensive; the `store list` command (CLI-SPEC §2) surfaces problems explicitly:

- **Dangling entry** — an entry whose `store` subfolder does not exist, or whose
  `path` no longer exists on disk. Resolution **ignores** a dangling entry rather
  than failing; `store list` flags it so the user can repair it.
- **Orphan subfolder** — a store subfolder under `central_root` with no registry
  entry. It is unreachable by path-based resolution (it can still be opened by an
  explicit `--store-name`); `store list` flags it.

---

## 5. Store resolution

Resolution maps a working directory `W` (and optional explicit override) to a single
store. The algorithm, in order:

1. **Explicit override** — if the caller supplied one, use it and stop:
   - an explicit **store path** (`--store-path` / `TASKMGR_DIR`) opens the store at
     that path directly (no walk-up);
   - an explicit **store name** (`--store-name`) opens `<central_root>/<name>` via
     the registry — it is an error if no entry names that store.

   A path override and a name override are **mutually exclusive**. A flag overrides
   the corresponding environment variable.

2. **Local walk-up** *(unchanged from today)* — starting at `W`, look for a
   `.tasks/` directory; if absent, ascend to the parent and repeat to the filesystem
   root. **If a `.tasks/` is found, that store is used and resolution stops.** This is
   why a local store always wins over a central one — the registry is never consulted
   when a local store exists.

3. **Central fallback** — no local store was found. Canonicalize `W` (§5.1) and
   match it against the registry: among entries whose canonical `path` is an
   **ancestor of, or equal to**, canonical `W`, choose the entry with the **longest**
   matching path (the most specific project). Open `<central_root>/<store>` for that
   entry. Dangling entries (§4.2) are skipped.

4. **No store** — neither a local store nor a matching registry entry exists: return
   the "no store" condition (`ErrNoStore`; SDK-SPEC §6). The CLI renders this as
   actionable guidance (CLI-SPEC §1).

The resolver also reports *how* the store was chosen (local / central / override) and
the paths involved, so a front end can show the user which store is in effect (SDK
`ResolveInfo`, SDK-SPEC §1; `taskmgr where`, CLI-SPEC §2).

### 5.1 Path canonicalization

Path comparison in steps 3 is performed on **canonical** paths, never on the raw
strings. To canonicalize a path:

1. expand a leading `~` to the user's home directory;
2. make it absolute (a relative registry `path` is resolved against `central_root`;
   a relative `W` against the process working directory);
3. resolve symlinks (`EvalSymlinks`) where the path exists;
4. clean it (`filepath.Clean`: collapse `.`/`..`, drop trailing separators).

Matching in step 3 is **ancestor / longest-prefix** on the canonical forms, on path
**segment boundaries** (so `/a/projectX` is not treated as a child of `/a/project`).
This mirrors local walk-up: a working directory deeper than a registered project
path still resolves to that project's store.

---

## 6. Store creation & relocation

Creating and moving stores is the only way the registry changes through normal use;
hand-editing `mapping.yaml` is supported but not required.

- **Create local** *(unchanged)* — create a `.tasks/` store in a project tree. No
  registry involvement.
- **Create central** — create `<central_root>/<store>` as an ordinary store **and**
  write its registry entry (`path` = the project path, `store` = the subfolder name).
  The prefix follows the same rules as a local store, falling back to `default_prefix`
  (§3) when none is supplied.
- **Relocate (move)** — move a store between local and central (or change its central
  location) by relocating its folder and updating the registry in one step:
  - local → central: move `<project>/.tasks` to `<central_root>/<store>`, add the
    entry;
  - central → local: move `<central_root>/<store>` to `<project>/.tasks`, remove the
    entry;
  - central → central: move the subfolder, update the entry's `store`.

  Because a store is self-contained, moving the folder carries all of its data; only
  the registry entry, never the store's own files, is rewritten.

Registry writes obey the same durability discipline as store writes (TASK-STORAGE-SPEC
§7): serialized under an advisory `flock` on the central root, written atomically
(temp file + `fsync` + `rename`). Concurrent `store` operations therefore never
corrupt or interleave `mapping.yaml`.

The CLI verbs for these operations (`init --central`, `store move`, `store link` /
`store unlink`, `store list`) are specified in [CLI-SPEC.md](CLI-SPEC.md) §2.

---

## 7. Overrides & precedence summary

From highest precedence to lowest, the inputs that determine the store:

| Precedence | Input | Effect |
|---|---|---|
| 1 | `--store-path <p>` (flag) | Open the store at path `p`. |
| 1 | `--store-name <n>` (flag) | Open central store `n` via the registry. (Mutually exclusive with `--store-path`.) |
| 2 | `TASKMGR_DIR` (env) | Same as `--store-path`; a flag overrides it. |
| 3 | local `.tasks/` (walk-up from cwd or `-C/--dir`) | Today's discovery. Wins over any central mapping. |
| 4 | central registry match (canonical longest-prefix) | Fallback when no local store exists. |
| — | none of the above | `ErrNoStore`. |

`TASKMGR_HOME` (§2) is orthogonal: it selects *where the config and registry live*,
not which store resolves. `central_root` (§3) likewise relocates the registry and
subfolders, not the resolution order.

---

## 8. Relationship to other specs

- [TASK-STORAGE-SPEC.md](TASK-STORAGE-SPEC.md) — the on-disk format of a single
  store. A central store is byte-for-byte an ordinary store; this spec only adds
  *where* it may live and *how* it is found.
- [SDK-SPEC.md](SDK-SPEC.md) §1 — the Go resolver API (`Resolve`, `ResolveOptions`,
  `ResolveInfo`) and store creation with a local/central target.
- [CLI-SPEC.md](CLI-SPEC.md) §1–§2 — the `--store-path` / `--store-name` flags, the
  `TASKMGR_DIR` / `TASKMGR_HOME` environment variables, and the `store` command
  group.
- [ARCHITECTURE-SPEC.md](ARCHITECTURE-SPEC.md) §5 — the `internal/env` seam that lets
  the engine read the home/environment while staying hermetically testable.
