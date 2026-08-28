# SDK Specification — `sdk/tasks`

This document specifies the public Go API of the storage engine, the package every
consumer imports to read and write a `.tasks` store. It is the single owner of file
access; the on-disk format it produces is defined in
[TASK-STORAGE-SPEC.md](TASK-STORAGE-SPEC.md).

```go
import "github.com/hk9890/task-manager/sdk/tasks"
```

The package is its own Go module (minimal dependencies) so consumers can import it
without pulling in any CLI dependencies.

---

## 1. Opening a store

```go
func Resolve(opts ResolveOptions, sopts ...Option) (*Store, ResolveInfo, error) // the front-end entry point
func Open(start string, opts ...Option) (*Store, error)          // low-level: local walk-up only
func Init(root, prefix string, opts ...Option) (*Store, error)   // create a local store
func InitCentral(projectPath, name, prefix string, opts ...Option) (*Store, error) // create + register
func MoveToCentral(projectPath, name string, opts ...Option) (*Store, error) // promote a local store
func RenameCentral(oldName, newName string) (string, error)      // rename folder + entry
func RelinkCentral(name, projectPath string) (string, error)     // re-point an entry at a moved project
func Stores() ([]StoreEntry, error)                              // enumerate the central registry
func DerivePrefix(path string) string                            // project path → default ID prefix

type Option func(*Store)
func WithLogger(l *slog.Logger) Option   // structured observability sink (MONITORING.md)
```

Package-level constants naming the on-disk layout (TASK-STORAGE-SPEC §2), so a
consumer can locate a store's files without hard-coding the names:

```go
const DataDirName    = ".tasks"      // the data directory under a project root
const ConfigFileName = "config.yaml" // the store config inside it
const FileExt        = ".md"         // the per-issue file extension
```

- **`Resolve`** is the single function a front end calls to get "the store for here".
  It runs the full resolution algorithm of [CONFIG-SPEC.md](CONFIG-SPEC.md) §4 —
  explicit override → local walk-up → central-registry fallback — reading the global
  config, the registry, and the environment through the engine's seams (using built-in
  config defaults when none is on disk; `Resolve` is a read and never writes), and
  returns the opened store plus a `ResolveInfo` saying how it was chosen. Returns `ErrNoStore`
  when nothing matches. This is what makes every front end (CLI, `taskmgr-ui`, …)
  resolve identically; the CLI is just a thin caller.
- **`Open`** is the low-level local discovery used as step 2 of `Resolve`: it walks
  up from `start` (or the current working directory if `start == ""`) for a `.tasks/`
  and loads its config. It consults **no** central registry or global config. Returns
  `ErrNoStore` if none is found. Consumers that want central fallback call `Resolve`.
- **`Init`** creates a new **local** project store under `root` with the given ID
  `prefix` and returns it open. Fails if a store already exists (`ErrStoreExists`) or
  the prefix is invalid.
- **`InitCentral`** creates a **central** store at `<central_root>/stores/<name>` (an
  ordinary store) **and** writes its registry entry `{path: projectPath, store: name}`
  in one operation (CONFIG-SPEC §5). `name` must match the store-name grammar
  (CONFIG-SPEC §3). An empty `prefix` is derived from the project directory name (else
  `task`), exactly as for `Init` — prefixes are per-project, with no global default.
  Fails if the subfolder or a registry entry for that path already exists.
- **`MoveToCentral`** promotes an existing local store: it registers `name` for
  `projectPath` and moves `<projectPath>/.tasks` to `<central_root>/stores/<name>`,
  returning the store open at its new location (CONFIG-SPEC §5). The store's
  `config.yaml` moves verbatim, so the prefix and hooks survive. Returns `ErrNoStore`
  when there is no local store to promote, and `ErrStoreExists` when the name or the
  subfolder is taken. The registry entry is written **before** the files move, so an
  interrupted promote leaves the local store working, and it is **rolled back** when the
  move returns an error, so the call can simply be retried (CONFIG-SPEC §5).
- **`RenameCentral`** renames the central store `oldName` to `newName` — the subfolder
  under `stores/` and the registry entry both — and returns the new store directory.
  The folder moves first and is **moved back** if the registry write fails. Returns
  `ErrStoreNotRegistered` for an unknown `oldName` and `ErrStoreExists` when `newName`
  is taken.
- **`RelinkCentral`** re-points the registry entry `name` at `projectPath`, for a
  project that moved on disk, and returns the canonical path recorded. It touches no
  files. `projectPath` must be **absolute** — a relative path is resolved against the
  central root, not the caller's working directory. Returns `ErrStoreNotRegistered` for
  an unknown name, `ErrNoStore` when the store subfolder is missing (it will not write
  an entry resolution would skip), and an error when `projectPath` does not exist
  (canonicalization falls back to the lexical form, so an unchecked typo would silently
  point a live entry at nothing).
- **`Stores`** reads the central registry and returns its entries (it does **not**
  resolve against a working directory — `where` uses `Resolve`; `store list` uses this).
  It reads through the same seams as `Resolve` and never writes; a missing registry
  yields an empty slice, a corrupt one an error. It takes **no arguments**: the
  registry is global, so there is nothing a working directory or a store-name
  override could select. Each entry carries its `Health`, classified by the same
  test resolution applies, so enumerating the registry costs one `stat` per entry
  and never reports a dangling or broken entry as usable.
- **`DerivePrefix`** turns a project path into the default ID prefix for a store
  tracking it (CONFIG-SPEC §5): base name lowercased, non-alphanumerics stripped,
  leading digits removed, truncated to 8, falling back to `task`. `Init` and
  `InitCentral` apply it themselves when `prefix` is empty; it is exported so a
  front end can *show* the default before creating anything, and so no front end
  has to re-implement it. That matters because the prefix is stamped into every
  ID the store ever allocates and cannot be changed retroactively — two callers
  deriving it differently is unrecoverable.
- **`Option`** values configure the store. `WithLogger` supplies the `log/slog`
  logger the store writes observability records to (hook timing, writes, IO errors;
  see [MONITORING.md](../MONITORING.md)); without it the store is silent. The SDK does not read
  *logging* configuration — a front end maps `TASKMGR_LOG` to a level and injects the
  logger. (Store **resolution** is the one place the SDK reads the environment, and it
  does so only through the `internal/env` seam — [ARCHITECTURE-SPEC](ARCHITECTURE-SPEC.md) §5 — so it stays
  hermetically testable.)

```go
type ResolveOptions struct {
    WorkDir   string // resolution origin; "" → process working directory
    StoreName string // explicit central store-name override (--store-name); via the registry
}

type ResolveInfo struct {
    Kind        ResolveKind // how the store was chosen
    StorePath   string      // the resolved store directory
    ProjectPath string      // the project the store tracks (the store's parent for a local store)
}

type ResolveKind int
const (
    ResolvedLocal        ResolveKind = iota // local .tasks found by walk-up
    ResolvedCentral                         // central registry match
    ResolvedOverrideName                    // explicit store-name
)

type StoreEntry struct {
    Path      string      // the project path the entry maps (canonicalized)
    Store     string      // the registry name == subfolder under <central_root>/stores
    StorePath string      // the resolved store directory, <central_root>/stores/<Store>
    Health    StoreHealth // whether that directory is a usable store
}

type StoreHealth int
const (
    StoreOK       StoreHealth = iota // a finished store
    StoreDangling                    // no subfolder at all — resolution skips the entry
    StoreBroken                      // a subfolder holding no config.yaml — resolution reports it
)
```

`StoreHealth` carries the three cases resolution already acts on (CONFIG-SPEC §3),
so a listing and a resolution can never disagree about an entry: a client that
enumerates the registry sees the dangling and broken entries **before** opening
them, rather than discovering them as an error per row. `String()` returns the
stable tokens `ok` / `dangling` / `broken`, which are the CLI's JSON contract
(CLI-SPEC §6).

In production `Resolve` and `InitCentral` use the OS-backed `vfs`/`env` seams;
hermetic tests inject in-memory/fake seams through the same internal hooks the store
already uses for `vfs.Mem`, so resolution is exercised with no real `HOME` or disk.

One further symbol is exported but **not part of the consumer API**, because its
argument type lives under `internal/` and no outside package can name it:

```go
func InitWithVFS(root, prefix string, fs vfs.FS, opts ...Option) (*Store, error)
```

`InitWithVFS` is the in-memory creation seam used by `internal/storetest` and the
engine's own tests. It routes through the same creation path as `Init`, so it
applies the identical `ErrStoreExists` guard, prefix validation and `Option`
values — a fixture must not be able to construct a store production could not.

A clock is supplied at construction, through `WithClock`, and never afterwards:

```go
func WithClock(now func() time.Time) Option
```

This replaces a former `Store.SetNow` method. `SetNow` wrote the `now` field
after construction, with no synchronization, on a type this section documents as
safe for concurrent use — so the supported way to get deterministic timestamps
was itself a data race. Construction-time injection also reaches the stores a
caller never builds directly: `Resolve` and `Open` pass their options down
(CONFIG-SPEC §4), where a setter could only reach a store already handed back. A
caller that must move time mid-run gives the closure its own state.

`Store` is the single gateway to a project's files; every read and write goes
through it. It is safe for concurrent use within a process and across processes; the
write path serializes writers (§7).

```go
func (s *Store) Root() string     // project root (parent of the data dir)
func (s *Store) Dir() string      // absolute path to the data directory
func (s *Store) Name() string     // central-registry name, "" for a local store
func (s *Store) Prefix() string   // configured ID prefix

func (s *Store) Config() Config                              // a copy, the use: list included
func (s *Store) UpdateConfig(mutate func(*Config) error) error // read-modify-write under the lock
func (s *Store) SetConfig(cfg Config) error                  // replace the file wholesale

func (s *Store) Packages() ([]PackageInfo, error) // every use: entry that gates this store
func (s *Store) HookChain() ([]HookInfo, error)   // the effective hook chain, in run order
```

`Name` reports the registry name the store was opened under — set by `Resolve` for
a central or `--store-name` match, and by `InitCentral` / `MoveToCentral` for the
store they create. It is empty for a local store, which has no registry entry to
take a name from. It is what identifies a store across projects; an issue ID does
not, because a prefix is derived from the project directory name, so two projects
whose directories share a name share a prefix (CONFIG-SPEC §5).

`Config` returns a copy, its `Use` slice included, so a caller editing the result cannot
reach the configuration the store is already running with.

`Packages` reports the per-user config's `use:` entries and then the store's, each with
the directory it resolves to and a status of `ok`, `missing` or `broken` — the vocabulary
the registry uses for its own entries (CONFIG-SPEC §3). It never fails on an entry that
will not load; reporting those is what it is for. `HookChain` is the same two lists
resolved into the hooks they contribute, in the order they run ([HOOK-SPEC](HOOK-SPEC.md)
§3.5), and it does fail when a package will not load, because a chain missing a gate is
not a chain.

`UpdateConfig` is how a caller changes one key. The read, the mutation and the write
all happen inside one hold of the store lock, so two processes editing different keys
keep both edits; `mutate` receives the configuration as it is on disk, not the snapshot
the `Store` was opened with. `SetConfig` replaces the file wholesale and is therefore
last-writer-wins: `cfg` was built from a read that happened outside the lock.

Both reject a `Prefix` that is empty or differs from the store's — the prefix is part of
every filename and every stored reference, so it is immutable — and both compile the
hooks a write **introduces** before writing, so a block that would fail every later
mutation ([HOOK-SPEC](HOOK-SPEC.md) §3.4) is refused here instead. A malformed hook
already on disk is not re-validated: it would otherwise refuse the write that removes
it. On success the store's compiled hook set is invalidated, so a long-lived handle runs
the hooks it just wrote rather than the ones it opened with.

---

## 2. Core types

### `Issue`

The complete in-memory model of one task file.

```go
type Issue struct {
    ID       string
    Title    string
    Status   Status
    Type     Type
    Priority int
    Assignee string
    Creator  string   // who filed the issue; set at creation, never edited
    Labels   []string

    Parent    string   // grouping/epic issue ID
    BlockedBy []string // IDs that must close before this is ready
    Related   []string // non-blocking references

    Created     time.Time
    Updated     time.Time
    Closed      time.Time // zero value == not closed
    CloseReason string

    Description string   // the markdown body, stored verbatim after the frontmatter
}

func (i *Issue) IsClosed() bool
```

Only the outgoing edges (`Parent`, `BlockedBy`, `Related`) are stored. Inverse
edges are derived (see `Detail`). `Description` is the issue's markdown body
(everything after the frontmatter); it round-trips through `Marshal` / `Unmarshal`
and is the text the `text` query field searches (QUERY-SPEC.md §2). Comments are
**not** carried on `Issue`; they live in the sidecar and are loaded on demand
(§4, `Detail` / `Comments`).

**`Description` is not populated by every read.** A body over `MaxInlineBody`
lives in a content sidecar (TASK-STORAGE-SPEC §4.6). `Get` and `Detail` resolve
it; the bulk read paths deliberately do not, and return an empty `Description`
for such an issue — see §4. Where the body is stored is otherwise not exposed on
`Issue`: the flag is an internal storage detail, so a caller can never construct
an `Issue` whose flag disagrees with its `Description`, and any issue handed back
by `Get`, `Detail` or `ResolveBody` is safe to pass to `Marshal`.

### `Comment`

```go
type Comment struct {
    ID       string    // opaque random token, ^[0-9a-z]{8}$ (self-assigned on append)
    Author   string
    Created  time.Time
    Replaces string    // "", or the id of a comment this one supersedes
    Deleted  bool      // true → tombstone retracting Replaces (Body is empty)
    Body     string
}
```

### `Ref` and `Detail`

```go
type Ref struct { ID, Title string; Type Type; Status Status; Priority int }

type Detail struct {
    Issue
    ParentRef     *Ref   // resolved parent   (vs the embedded Issue.Parent ID)
    BlockedByRefs []Ref  // resolved blockers (vs the embedded Issue.BlockedBy IDs)
    RelatedRefs   []Ref  // symmetric related: forward (Issue.Related) ∪ inverse, deduped
    Blocks        []Ref  // derived: issues blocked by this one
    Children      []Ref  // derived: issues whose parent is this one
    Comments      []Comment
    BodyExternal  bool   // the body came from the content sidecar, not the .md
}
```

`Detail` always carries a fully resolved `Description`. `BodyExternal` only says
where the bytes were stored (TASK-STORAGE-SPEC §4.6) — it is the one public place
that reports this, and is informational: a viewer can use it to say where the
content lives, or to warn before rendering a very large body. It reports the
stored layout regardless of the issue's status: a closed issue reaches `Detail`
by a different path than a hot one, and both must answer the same.

### Enums and bounds

```go
type Status string
const ( StatusOpen; StatusInProgress; StatusBlocked; StatusDeferred; StatusClosed )
var Statuses []Status
func (s Status) Valid() bool
func (s Status) IsClosed() bool

type Type string
const ( TypeTask; TypeBug; TypeFeature; TypeEpic; TypeChore; TypeDoc )
var Types []Type
func (t Type) Valid() bool
func (t Type) IsWork() bool   // false only for TypeDoc

const ( PriorityMin = 0; PriorityMax = 4; PriorityDefault = 2 )

// Body storage bounds (TASK-STORAGE-SPEC §4.6).
const (
    MaxInlineBody  = 65536 // above this, an issue body moves to the content sidecar
    MaxCommentBody = 65536 // hard cap: comments are rejected above this, never overflowed
)
```

`TypeDoc` is an ordinary issue type in every respect except that `IsWork` reports
false, which is what keeps documents out of `Ready` and `Blocked` (§4).

### `Config`

```go
type Config struct {
    Prefix      string       `yaml:"prefix"`                 // prepended to allocated issue IDs
    HookTimeout string       `yaml:"hook_timeout,omitempty"` // per-hook wall-clock limit ("2s"); "" = 2s default, "0" = disabled
    Use         []PackageRef `yaml:"use,omitempty"`          // the hook packages this store uses
}

// Hook is one entry of a package manifest, never of a config file.
type Hook struct {
    ID    string   `yaml:"id,omitempty"`   // required; effective id is "pkg:<package>:<id>"
    Event string   `yaml:"event"`          // the lifecycle event that fires it
    When  string   `yaml:"when,omitempty"` // optional QUERY-SPEC filter over the new issue
    Run   []string `yaml:"run"`            // argv, executed directly (no shell); required

}

// PackageInfo is one use: entry and what it resolves to on this machine.
type PackageInfo struct {
    Name, Path, Scope string // the package, its directory, and "global" | "store"
    Status, Detail    string // "ok" | "missing" | "broken", and why when it is not ok
    Hooks             int    // how many hooks it contributes
    Shadowed          bool   // an earlier entry already took this name
}

// HookInfo is one hook of the effective chain.
type HookInfo struct {
    ID, Event, When  string   // ID is the effective "pkg:<package>:<hook>"
    Run              []string // argv, with argv[0] already resolved inside the package
    Package, Scope   string   // which package supplied it, and which file named that package
}

const PackageManifestName = "taskmgr-package.yaml"
```

`HookTimeout` and `Use` are read lazily on the first write, not on read, so an unusable
package never breaks queries. Semantics: [HOOK-SPEC](HOOK-SPEC.md) §3, package format
§3.6.

### `GlobalConfig`

The per-user configuration ([CONFIG-SPEC](CONFIG-SPEC.md) §2) — one file for the
whole machine, not part of any store.

```go
type GlobalConfig struct {
    Version     int    `yaml:"version"`
    CentralRoot string `yaml:"central_root,omitempty"` // registry + central stores; "" = the taskmgr home
    HookTimeout string `yaml:"hook_timeout,omitempty"` // fallback limit for a store that sets none
    Use         []PackageRef `yaml:"use,omitempty"`    // hook packages applied to every store on this machine
}

// PackageRef is one entry of a use: list. Exactly one field is set.
type PackageRef struct {
    Name string `yaml:"name,omitempty"` // <taskmgr home>/packages/<name>
    Path string `yaml:"path,omitempty"` // relative to the directory holding the config file
}

func LoadGlobalConfig() (GlobalConfig, error)  // built-in defaults when the file is absent
func UpdateGlobalConfig(mutate func(*GlobalConfig) error) error // read-modify-write under the home lock
func SaveGlobalConfig(cfg GlobalConfig) error  // replace the file wholesale
func GlobalConfigPath() (string, error)        // absolute path, whether or not it exists
func GlobalPackages() ([]PackageInfo, error)   // the per-user use: list and what it resolves to
```

The packages named here contribute hooks **before** a store's own on every mutation, and
every hook's effective id is `pkg:<package>:<hook>` ([HOOK-SPEC](HOOK-SPEC.md) §3.5), so
the package that supplied a gate is readable from the id alone.

`UpdateGlobalConfig` and `SaveGlobalConfig` are the store pair's counterparts, over the
home's own lock: the first is a read-modify-write inside it, the second replaces the file
from a snapshot read outside it. Both check a `use:` entry the write introduces — one
that could never resolve would fail mutations in every store on the machine, so it is
refused at the call that caused it — and neither re-checks one already on disk, which is
what leaves a bad entry removable.

---

## 3. Inputs

```go
type CreateInput struct {
    ID          string    // optional explicit ID (import/migration); empty → store allocates
    Title       string
    Description string
    Type        Type
    Priority    *int      // nil → PriorityDefault
    Assignee    string
    Creator     string    // who filed the issue (caller-resolved identity); recorded once
    Labels      []string
    Parent      string
    BlockedBy   []string
    Related     []string
}

type UpdateInput struct {
    Title        *string   // nil pointer → field unchanged
    Description  *string
    Status       *Status
    Type         *Type
    Priority     *int
    Assignee     *string
    Parent       *string
    SetLabels    []string  // replace the whole set
    AddLabels    []string
    RemoveLabels []string
    ClearLabels  bool
}

type ImportInput struct {
    ID          string    // optional explicit ID; empty → store allocates
    Title       string
    Description string
    Type        Type
    Priority    *int
    Status      Status    // final status (incl. closed); empty → StatusOpen
    Assignee    string
    Creator     string
    Labels      []string
    Parent      string    // edges: taskmgr IDs that must already exist
    BlockedBy   []string
    Related     []string
    Created     time.Time // zero → now()
    Updated     time.Time // zero → Created
    Closed      time.Time // required when Status == closed; zero → Updated
    CloseReason string
    RunHooks    bool      // run lifecycle hooks on this import; default false (bulk import omits hooks)
    Comments    []ImportComment
}

type ImportComment struct {
    Author  string
    Created time.Time // zero → the issue's Created
    Body    string
}
```

`UpdateInput` uses pointers so the zero value means "leave unchanged"; only set
fields are applied.

`Creator` is intentionally absent from `UpdateInput`: it is provenance — set once
at creation and never edited afterward. The SDK applies no identity default: an
empty `CreateInput.Creator` is stored as empty (provenance simply unknown). The
`$USER` fallback is a CLI-layer convenience (`--creator`, [CLI-SPEC.md](CLI-SPEC.md) §4), matching
`--assignee` and comment `--author`; an embedder that wants attribution sets
`Creator` itself.

### Filtering

```go
type Filter struct {
    Expr          string    // filter expression (QUERY-SPEC.md); closed-scope auto-detected
    IncludeClosed bool      // scope: include closed issues (reads closed/ partition)
    Sort          SortField // presentation order
    Reverse       bool      // reverse the sort order
    Offset        int       // matches to skip after sort/reverse, before Limit (0 = none)
    Limit         int       // 0 = no limit
}

type SortField string
const (
    SortWork     SortField = "" // priority then created (default)
    SortID; SortPriority; SortCreated; SortUpdated; SortClosed
)
// Every sort is total — it ends with an `id` tie-break — so the order is
// deterministic for a given store state. This is what makes Offset/Limit paging
// stable across windows (see ListPage, §4).
```

All filtering (by status, type, priority, assignee, creator, label, text, ready/blocked, dates)
is expressed via the `Expr` field using the filter-expression language
(see [QUERY-SPEC.md](QUERY-SPEC.md)). The empty expression is the always-true predicate.

`Offset` and `Limit` are presentation paging applied after sort/reverse: skip
`Offset` matches, then return at most `Limit` (0 = all remaining). Negative values
are clamped to 0. To page a large result set (e.g. virtual scrolling) use
`ListPage` / `FindPage` (§4), which return the window together with the total match
count from a single snapshot.

**Scope semantics (TASK-STORAGE-SPEC §5, QUERY-SPEC.md §5):**
- By default (`IncludeClosed:false`, no closed-referencing `Expr`)
  only the hot (active) set is scanned. The `closed/` partition is never opened.
- `IncludeClosed:true`: reads both hot and cold partitions.
- `Expr` satisfies the cold-scope predicate (QUERY-SPEC.md §5 — a `status == "closed"`
  atom or any `closed`-field comparison; `status != "closed"` does **not** count):
  cold partition is auto-included; `IncludeClosed` need not be set explicitly.
- Callers must never rely on the cold partition being scanned silently — they must
  explicitly opt in. `All()`, `Ready()`, `Blocked()`, and `Labels()` are always hot-only.

### Structured criteria

`Criteria` is a typed, composable description of a selection. It **compiles** to a
canonical filter expression (QUERY-SPEC.md) that is fed to the existing engine — it
is a convenience for structured callers, **not** a second selection engine.

```go
type Criteria struct {
    Text        string   // matched against id+title+description (per TextMatch)
    TextMatch   TextMatch
    Statuses    []Status // OR within the group
    Types       []Type   // OR within the group
    Labels      []string // combined per LabelMatch
    LabelMatch  LabelMatch
    Assignee    string   // assignee == "..."
    Creator     string   // creator == "..."
    Parent      *string  // parent == "id"; a non-nil "" means "no parent"
    Work        WorkState
    PriorityMin *int     // priority >= n
    PriorityMax *int     // priority <= n
    CreatedFrom, CreatedTo *time.Time
    UpdatedFrom, UpdatedTo *time.Time
    ClosedFrom,  ClosedTo  *time.Time
}

type LabelMatch int
const (
    LabelMatchAll LabelMatch = iota // every listed label present (default)
    LabelMatchAny                   // at least one listed label present
)

type TextMatch int
const (
    TextPhrase   TextMatch = iota // Text is one contiguous substring (default)
    TextAllWords                  // each whitespace-separated word AND-ed (order-independent)
)

type WorkState int
const (
    WorkAny WorkState = iota // no ready/blocked constraint
    WorkReady                // -> bare `ready`
    WorkBlocked              // -> bare `blocked`
)

// Build compiles the criteria to a canonical filter expression. It is the single
// owner of value quoting/escaping and precedence. Pure; no filesystem access. The
// zero value compiles to "" (the always-true predicate). For well-formed input the
// result always parses (it never yields a *ParseError); invalid input — an unknown
// Status or Type, or a negative priority bound — is reported as a *ValidationError
// (§6), naming the offending field.
func (c Criteria) Build() (string, error)
```

Compilation: every non-empty group is AND-ed at the top level, and each multi-value
group is wrapped in parentheses to protect precedence under the surrounding `&&`.
All user-supplied string, enum, and date values are emitted **quoted** (with
`"`→`\"` and `\`→`\\` escaping per QUERY-SPEC.md §3); the numeric `priority` is
emitted bare. This puts the bareword/quoting rule in exactly one audited place.
`LabelMatch` defaults to `LabelMatchAll` (the issue must carry every listed label).
`TextMatch` defaults to `TextPhrase`: `Text` compiles to a single `text ~ "..."`.
Under `TextAllWords` the `Text` value is split on whitespace and each word emits its
own `text ~ "word"` clause AND-ed together (order-independent); empty or
whitespace-only `Text` adds no clause.
Date bounds are **half-open on the instant**: `*From` emits `field >= From`
(inclusive), `*To` emits `field < To` (exclusive); to cover a whole day `D` pass `To`
as the start of the next day. `PriorityMin` / `PriorityMax` are emitted bare as
`priority >= n` / `priority <= n`. A `priority` literal is a **non-negative** integer
(QUERY-SPEC.md §3, grammar `number = digit {digit}`); a bound *above* the storable
range (`>= 5`) is emitted as-is and harmless — it matches all or none rather than
erroring. A **negative** bound is rejected with a `*ValidationError` naming the field:
the grammar cannot express it, and clamping it to `0` would silently change its
meaning (`priority <= -1` selects nothing, but `priority <= 0` selects the criticals).
Rejecting rather than clamping keeps `Build` total over the storable range and free of
`*ParseError`s.

### Free-text search

```go
func SearchExpr(query string) string
```

`SearchExpr` is the single shared entry point for user-facing text search. It turns
a free-text query into the canonical filter expression using the AND-of-words
semantic (`Criteria{Text: query, TextMatch: TextAllWords}.Build`): the query is split
on whitespace and every word must appear in the issue's id/title/description
(order-independent). Matching is per-word **substring** (inherited from `~`), so
`cat dog` also matches "category dogma" — not whole-word/token matching. An empty or
whitespace-only query yields `""` (the always-true predicate). The result is always a
valid expression usable as `Filter.Expr`, so the CLI `search`
command and any UI share one definition of search. `SearchExpr` is **total** — it
never returns an error (a search box must not reject input), which is why it returns a
bare `string` rather than mirroring `Build`'s `(string, error)`. To combine search
with structured facets, build a `Criteria` with `TextAllWords` and call `Build` rather
than concatenating expression strings.

---

## 4. Store methods

### Read (lock-free)

```go
func (s *Store) Get(id string) (*Issue, error)        // one issue (falls through to closed/)
func (s *Store) All() ([]*Issue, error)               // hot (active) set only, sorted by ID
func (s *Store) List(f Filter) ([]*Issue, error)      // select by expression + scope/sort/offset/limit
func (s *Store) ListPage(f Filter) (Page, error)      // List window + total match count (paging)
func (s *Store) Ready() ([]*Issue, error)             // open work, no open blockers (never docs)
func (s *Store) Blocked() ([]BlockedIssue, error)     // non-closed work with an open blocker (never docs)
func (s *Store) Detail(id string) (*Detail, error)    // issue + resolved + derived edges + comments
func (s *Store) Labels() ([]string, error)            // distinct labels, sorted
func (s *Store) Comments(id string) ([]Comment, error)// the issue's comment log
func (s *Store) ResolveBody(iss *Issue) error         // fill in an overflowed body; no-op otherwise
```

#### Body resolution

Reads split into two classes, and the difference is deliberate:

| Method | `Description` for an overflowed issue |
|---|---|
| `Get`, `Detail` | **resolved** — the full body, read from the content sidecar |
| `All`, `Query`, `List`, `ListPage`, `Find`, `FindPage`, `Ready`, `Blocked` | **empty** |

A bulk path never reads a content sidecar, so listing a thousand issues can never
materialize gigabytes no matter what they contain. `ResolveBody` is how a caller
fills in one of them; it is safe on any issue and a no-op when the body is
already inline. After it returns, the issue is safe to hand to `Marshal`.

> **Compatibility.** Before body overflow, every read populated `Description`.
> Code that reads a body out of a bulk result must now call `ResolveBody` (or
> `Get`) for issues large enough to have overflowed. This is a deliberate
> breaking change, not a deprecation: there is no flag to restore the old
> behaviour.

The one exception to "bulk paths do not read sidecars" is invisible from the
outside: an expression that uses the `text` field is evaluated against a resolved
copy, because `text` spans the body (QUERY-SPEC.md §2). The issues *returned* are
still unresolved, so the table above holds unconditionally.

```go
type BlockedIssue struct {
    Issue     *Issue
    BlockedBy []Ref
}

// Page is a windowed List result plus the total number of matches in scope
// (before Offset/Limit) — the value a viewer needs to size a scrollbar.
type Page struct {
    Issues []*Issue // the window: matches[Offset : Offset+Limit] (matches[Offset:] when Limit == 0)
    Total  int      // total matches in scope, before Offset/Limit
}
```

- **`All`** returns only the hot (active) set — it never descends into `closed/` or
  `comments/`. It is the cheapest scan: O(open issues), regardless of how many
  closed issues exist. Use `List(Filter{IncludeClosed:true})` to read history.
- **`List`** parses and evaluates the **filter-expression language**;
  the engine is its sole implementation (the CLI just forwards a string). The
  grammar, fields, operators, and error semantics are defined in
  [QUERY-SPEC.md](QUERY-SPEC.md); a malformed expression returns a `*ParseError`
  (§6), not a match.
- **`List`** reads the active set by default; passing
  `IncludeClosed:true` **or** an expression that satisfies the cold-scope predicate
  (QUERY-SPEC.md §5) includes the cold partition. See Filter scope semantics in §3.
- **`ListPage`** runs the same selection/sort/paging as `List` and additionally
  returns `Total` — the count of all matches in scope **before** `Offset`/`Limit`
  are applied. The window and the total come from one directory snapshot, so a
  paging viewer never races a separate count against the page. Every sort has an `id`
  tie-break, so ordering is **deterministic for a given store state**; but each call
  is its own snapshot, so paging is not isolated across calls — a store mutated
  between page fetches can skip or repeat an item at a window boundary.
- A structured caller reaches the same place through `Criteria.Build`:
  `List(Filter{Expr: c.Build(), …})`. Cold scope is derived by applying the
  cold-scope predicate (QUERY-SPEC.md §5) to the **built expression** — the same
  detector any expression goes through — so a `Criteria` and a hand-written `Expr`
  always scope identically, and `Filter.IncludeClosed` is the explicit override.
  If `Criteria.Build` fails (the `*ValidationError` cases above — unknown `Status`
  / `Type`, or a negative priority bound), that error is returned and no scan runs.

  > **Removed.** `Store.Query`, `Store.Find`, `Store.FindPage` and `FindOptions`
  > were part of this surface until v0.9.0. Each forwarded to `List` or
  > `ListPage` and none had a caller outside the engine's own tests, while
  > `FindOptions` restated five of `Filter`'s six fields — so every new `Filter`
  > field cost three declarations and two copy blocks, and a field left out of one
  > of them was silently ignored rather than rejected. `Query(expr)` becomes
  > `List(Filter{Expr: expr})`; `Find(c, opt)` becomes the `Criteria.Build` call
  > above. `Criteria` and `Build` are unchanged.
- **`Ready`** / **`Blocked`** / **`Labels`** are always hot-only. They are O(open)
  and never read `closed/`. Use `List(Filter{IncludeClosed:true})` for history.
- **`Ready`** / **`Blocked`** additionally exclude `TypeDoc`: a document is not
  work (TASK-STORAGE-SPEC §9). The bare `ready` / `blocked` query predicates apply
  the same exclusion, so the two surfaces cannot disagree.
- **`Detail`** resolves `ParentRef`, `BlockedBy`, `Related`, computes `Blocks` and
  `Children` by scanning, loads `Comments` from the sidecar, and resolves an
  overflowed body (reporting its origin in `BodyExternal`).
- **`Comments`** / **`Detail.Comments`** return the **resolved effective log**: each
  `replaces`-chain collapsed to its newest document, tombstoned comments omitted
  (storage spec §4.4). The on-disk stream keeps full history; the API returns the
  current view.

### Write (validated, under the lock)

```go
func (s *Store) Create(in CreateInput) (*MutationResult, error)
func (s *Store) Import(in ImportInput) (*MutationResult, error)   // direct write of a complete end-state
func (s *Store) Update(id string, in UpdateInput) (*MutationResult, error)
func (s *Store) Close(id, reason string) (*MutationResult, error)   // idempotent; moves to closed/
func (s *Store) Reopen(id string) (*MutationResult, error)          // moves back to active
func (s *Store) AddComment(id, author, body string) (*Comment, error)         // returns the new comment (with its id)
func (s *Store) EditComment(id, commentID, author, body string) (*Comment, error) // appends a revision; returns the new effective comment
func (s *Store) DeleteComment(id, commentID, author string) error             // appends a tombstone
func (s *Store) AddDep(dependent, blocker string) error    // idempotent; rejects self/cycle
func (s *Store) RemoveDep(dependent, blocker string) error
func (s *Store) AddRelated(a, b string) error              // idempotent; rejects self/dangling (no cycle check)
func (s *Store) RemoveRelated(a, b string) error           // severs both sides
```

- Every write allocates/validates, then performs an atomic file write while holding
  the project lock. Configured lifecycle hooks ([HOOK-SPEC.md](HOOK-SPEC.md)) run on the
  transition: pre-hooks gate the write under the lock, post-hooks notify after it. A
  pre-hook denial returns `*HookDeniedError` (§6) and writes nothing.
- **All five writes (`Create`/`Update`/`Close`/`Reopen`/`Import`) return a `*MutationResult`** —
  the resulting `Issue` plus the advisory hook output (HOOK-SPEC §6.2): `Hints`, aggregated
  from every pre- and post-hook that allowed, and `Warnings`, the post-hook failures (which
  never fail the write). Both are nil when no hooks ran or none had anything to say. A no-op
  mutation (nothing changes on disk) returns the unchanged issue with no hints/warnings
  and fires no hooks (HOOK-SPEC §2.1).

  ```go
  type MutationResult struct {
      Issue    *Issue
      Hints    []string
      Warnings []string
  }
  ```
- **`Create`** allocates a fresh collision-resistant ID (random base36 token; see
  TASK-STORAGE-SPEC §3), applies defaults (`TypeTask`, `PriorityDefault`,
  `StatusOpen`), de-duplicates labels/edges, and validates. A non-empty
  `CreateInput.ID` is honoured verbatim instead (import/migration) when it is
  well-formed, carries the store prefix, and is not already in use.
- **`Import`** is a direct write of a complete issue **end-state** from an external
  system — not a `Create`→`Update`→`Close` replay. Unlike `Create` it takes the
  final `Status` (including `closed`) and the original `Created`/`Updated`/`Closed`
  timestamps and the full comment log, validates the whole record (fields,
  references, and every comment) **before** any write, and materializes it in one
  locked operation in the correct partition (a closed issue lands directly in
  `closed/` via the same git-rename anchor as `Close`). Edge targets must already
  exist (same referential/acyclicity checks as `Create`), so an importing caller
  works in dependency order and translates foreign IDs to taskmgr IDs. It writes the
  comment sidecar with the supplied authors and timestamps. Because validation is
  up-front, a rejected record (e.g. a control character in a comment body) leaves
  **nothing** behind. By default `Import` runs with hooks **omitted** — bulk loading should
  not fire a gate per issue; set `ImportInput.RunHooks` to gate/notify each imported
  transition like an ordinary write.
- **`Update`** applies the partial field changes and routes lifecycle transitions
  through the same path the CLI uses: setting `Status` to `closed` routes through the
  close flow (stamps the close time, moves the file to `closed/`); setting a
  non-closed `Status` on a closed issue reopens it (moves the file back, clears the
  close fields) and the issue ends in **the requested status** — `in_progress` stays
  `in_progress`, not forced to `open` — then the remaining field edits apply. A plain
  field edit with no status change on a closed issue returns `ErrImmutable` (and
  re-setting `Status: closed` on a closed issue is a status no-op, so any accompanying
  field edit still returns `ErrImmutable`). Callers drive a status change entirely
  through `Update`'s `Status` field — they never dispatch `Close` / `Reopen`
  themselves for it.
- **`AddComment`** appends a new comment and returns it, including its freshly
  allocated random `ID` (the handle a caller needs for a later edit/delete);
  **`EditComment`** appends a revision with `Replaces` set and returns the new
  effective comment; **`DeleteComment`** appends a tombstone (`Replaces` set,
  `Deleted: true`, empty body). All three sanitize the body and run under the store
  lock; none rewrites the task file (the sidecar is append-only, storage §4.4).
- **`Close`** sets the closed timestamp/reason and relocates the file to `closed/`;
  **`Reopen`** moves it back to the hot set, clears the close fields, and sets
  `StatusOpen` (the pre-close status is not persisted). `Reopen` always lands on
  `open`; to reopen directly into another active status use `Update` with that
  `Status`. On an already-active issue `Reopen` is a no-op, returning it unchanged.
- **`AddRelated` / `RemoveRelated`** manage the non-blocking `related` link, the
  peer to `AddDep`/`RemoveDep` for `blocked_by`. The relationship is **symmetric**:
  `AddRelated(a, b)` stores the edge on `a` (idempotent; rejects a self-link and a
  dangling ref; **no** cycle check, since related is non-blocking) and the inverse
  is derived on read — `Detail.RelatedRefs` is the deduped union of forward and
  inverse edges, so the link shows from both issues. `RemoveRelated(a, b)` severs
  the edge from **both** stored sides; `a` must be writable (`ErrImmutable` if
  closed), and the inverse side is best-effort (skipped if `b` is closed/absent).
- **The four edge methods share one no-op contract.** Adding an edge that is
  already present, and removing one that is not, both succeed and **write
  nothing**: no file is rewritten, `updated` is not bumped, and no log record is
  emitted. Only a call that actually changes the stored list writes.

  This is stated once, for all four, because they diverged on it: `RemoveDep`
  rewrote the file and bumped `updated` unconditionally, while its peer
  `RemoveRelated` detected the no-op. A caller could not reason about the family
  — `dep rm` on an absent edge churned the `.md`, reordered `--sort updated`
  results and produced a git diff for a command that changed nothing, while the
  identical `rel rm` did not.

---

## 5. Serialization

```go
func Marshal(iss *Issue) ([]byte, error)   // Issue → on-disk file bytes
func Unmarshal(data []byte) (*Issue, error) // file bytes → Issue
```

Exposed for tools that need to read or render a single file without a `Store`.
Output conforms to the storage spec (frontmatter + verbatim markdown body).

`Marshal` is unchanged by body overflow: it always writes the `Description` it is
given, inline, and never errors on size. Splitting a large body across the `.md`
and the content sidecar is the **store's** job (TASK-STORAGE-SPEC §4.6), not the
serializer's — which keeps this function pure and preserves the round-trip
property that anything `Unmarshal` accepts, `Marshal` re-emits. The consequence
is that a caller who marshals an issue and writes the bytes into the hot
directory themselves can produce an oversized file; going through `Store` is what
maintains the invariant.

---

## 6. Errors & validation

Sentinel errors, testable with `errors.Is`:

```go
var (
    ErrNotFound           // issue not found
    ErrAlreadyExists      // an explicit CreateInput.ID / ImportInput.ID is already taken
    ErrNoStore            // no store found (no local .tasks and no registry match)
    ErrStoreExists        // a store already exists at the create/move target
    ErrImmutable          // attempted in-place write to a closed issue (closed/ partition)
    ErrStoreNotRegistered // a store name has no registry entry (CONFIG-SPEC §4)
)
```

`Resolve` returns `ErrNoStore` when neither a local store nor a registry match is
found, and `ErrStoreNotRegistered` when an explicit `StoreName` has no entry.
`RenameCentral` and `RelinkCentral` return `ErrStoreNotRegistered` for the store they
were asked to edit, and `ErrNoStore` when its subfolder is missing; `MoveToCentral`
returns `ErrNoStore` when there is no local store to promote. A corrupt
`config.yaml` or `mapping.yaml`, or a registry with a duplicate canonical `path`,
is reported as a (non-sentinel) configuration error (CONFIG-SPEC §2–§3).

`ErrAlreadyExists` is returned by `Create` and `Import` when the caller supplies an
explicit `ID` the store already holds; allocated IDs retry against the existing set
and cannot hit it.

`ErrImmutable` is returned by `Update` (ordinary field edits), `AddDep`,
`RemoveDep`, `AddRelated`, and `RemoveRelated` when the target issue lives in
`closed/` (closed issues are immutable
in place — see storage spec §5). `Close` is idempotent: calling it on an already-closed
issue returns the existing issue and `nil` (no-op; no in-place write to `closed/`
is attempted); `Reopen` is symmetric — on an already-active issue it is a no-op
returning the issue unchanged. Use `Reopen` to restore mutability; `AddComment` / `EditComment` /
`DeleteComment` are still allowed on closed issues (sidecar append is the one
exception — see storage spec §5).

Validation failures return a typed error carrying the offending field:

```go
type ValidationError struct { Field, Message string }
func (e *ValidationError) Error() string
```

The store rejects (before any byte is written) every invariant listed in the
storage spec: empty title, unknown enum, priority out of range, self/duplicate/
dangling references, dependency cycles, and field-constraint violations.

A malformed filter expression (`Query` / `List`) returns a typed parse error
locating the failure; it is not a validation error and never reaches disk:

```go
type ParseError struct { Pos int; Message string } // Pos = byte offset in the expression
func (e *ParseError) Error() string
```

The grammar it enforces is defined in [QUERY-SPEC.md](QUERY-SPEC.md) §1.

A **pre-hook denial** returns a typed error naming the gate that refused — the event, hook
id, issue, exit code, and reason, plus any hints gathered from hooks that allowed before it
([HOOK-SPEC.md](HOOK-SPEC.md) §6.2). It fails the mutation with nothing written:

```go
type HookDeniedError struct {
    Event   string   // the gated event, e.g. "pre-close"
    Hook    string   // the denying hook's id
    IssueID string   // the issue the transition targeted
    Exit    int      // the hook's exit code (-1 when it never exited: spawn error/timeout)
    Reason  string   // the denial reason
    Hints   []string // hints from hooks that allowed before the denial
}
func (e *HookDeniedError) Error() string
```

---

## 7. Concurrency & durability contract

- **One writer per store.** Every mutating method serializes against all others,
  whether separate processes or goroutines in one process: an in-process mutex
  serializes goroutines, and an exclusive advisory `flock` on `.tasks/.lock`
  serializes processes. Reads are lock-free.
- **Atomic writes.** Each file write is temp-file + `fsync` + `rename`, so a reader
  never observes a torn file — except the append-only comment sidecar (§4), which is
  grown with `O_APPEND` + `fsync` rather than rewritten.
- **Read consistency.** A read takes a fresh snapshot per call and returns an
  `*Issue` the caller may mutate without affecting disk. A scan spanning both
  partitions (`IncludeClosed`) reads two directories, so it is **not** a single
  atomic snapshot: a concurrent close/reopen (a cross-partition move) can briefly
  make an issue visible in both or neither. Reads dedup by ID, so an issue never
  appears twice; a transient omission, if any, clears on the next call.

---

## 8. Stability

The exported surface in this document is the public contract. `Store` internals are
opaque. Additive changes (new methods, new optional struct fields) are
non-breaking; signature or semantic changes are breaking and are versioned at the
module level.
