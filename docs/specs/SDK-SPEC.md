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
func Resolve(opts ResolveOptions) (*Store, ResolveInfo, error)   // the front-end entry point
func Open(start string, opts ...Option) (*Store, error)          // low-level: local walk-up only
func Init(root, prefix string, opts ...Option) (*Store, error)   // create a local store
func InitCentral(projectPath, name, prefix string, opts ...Option) (*Store, error) // create + register
func Stores(opts ResolveOptions) ([]StoreEntry, error)           // enumerate the central registry

type Option func(*Store)
func WithLogger(l *slog.Logger) Option   // structured observability sink (MONITORING.md)
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
- **`Stores`** reads the central registry and returns its entries (it does **not**
  resolve against a working directory — `where` uses `Resolve`; `store list` uses this).
  It reads through the same seams as `Resolve` and never writes; a missing registry
  yields an empty slice, a corrupt one an error.
- **`Option`** values configure the store. `WithLogger` supplies the `log/slog`
  logger the store writes observability records to (hook timing, writes, IO errors;
  see MONITORING.md); without it the store is silent. The SDK does not read
  *logging* configuration — a front end maps `TASKMGR_LOG` to a level and injects the
  logger. (Store **resolution** is the one place the SDK reads the environment, and it
  does so only through the `internal/env` seam — ARCHITECTURE-SPEC §5 — so it stays
  hermetically testable.)

```go
type ResolveOptions struct {
    WorkDir   string // resolution origin; "" → process working directory
    StorePath string // explicit store-path override (--store-path / TASKMGR_DIR); opens directly
    StoreName string // explicit central store-name override (--store-name); via the registry
    // StorePath and StoreName are mutually exclusive.
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
    ResolvedOverridePath                    // explicit store-path / TASKMGR_DIR
    ResolvedOverrideName                    // explicit store-name
)

type StoreEntry struct {
    Path      string // the project path the entry maps (canonicalized)
    Store     string // the registry name == subfolder under <central_root>/stores
    StorePath string // the resolved store directory, <central_root>/stores/<Store>
}
```

In production `Resolve` and `InitCentral` use the OS-backed `vfs`/`env` seams;
hermetic tests inject in-memory/fake seams through the same internal hooks the store
already uses for `vfs.Mem`, so resolution is exercised with no real `HOME` or disk.

`Store` is the single gateway to a project's files; every read and write goes
through it. It is safe for concurrent use within a process and across processes; the
write path serializes writers (§7).

```go
func (s *Store) Root() string     // project root (parent of the data dir)
func (s *Store) Dir() string      // absolute path to the data directory
func (s *Store) Prefix() string   // configured ID prefix
```

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
}
```

### Enums and bounds

```go
type Status string
const ( StatusOpen; StatusInProgress; StatusBlocked; StatusDeferred; StatusClosed )
var Statuses []Status
func (s Status) Valid() bool
func (s Status) IsClosed() bool

type Type string
const ( TypeTask; TypeBug; TypeFeature; TypeEpic; TypeChore )
var Types []Type
func (t Type) Valid() bool

const ( PriorityMin = 0; PriorityMax = 4; PriorityDefault = 2 )
```

### `Config`

```go
type Config struct { Prefix string }
```

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
`$USER` fallback is a CLI-layer convenience (`--creator`, CLI-SPEC.md §4), matching
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
valid expression usable as `Filter.Expr` or with `Store.Query`, so the CLI `search`
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
func (s *Store) Query(expr string) ([]*Issue, error)  // select by filter expression (QUERY-SPEC.md)
func (s *Store) List(f Filter) ([]*Issue, error)      // Query + scope/sort/offset/limit via Filter
func (s *Store) ListPage(f Filter) (Page, error)      // List window + total match count (paging)
func (s *Store) Find(c Criteria, opt FindOptions) ([]*Issue, error)  // Criteria.Build + List
func (s *Store) FindPage(c Criteria, opt FindOptions) (Page, error)  // Criteria.Build + ListPage
func (s *Store) Ready() ([]*Issue, error)             // open, no open blockers
func (s *Store) Blocked() ([]BlockedIssue, error)     // non-closed with an open blocker
func (s *Store) Detail(id string) (*Detail, error)    // issue + resolved + derived edges + comments
func (s *Store) Labels() ([]string, error)            // distinct labels, sorted
func (s *Store) Comments(id string) ([]Comment, error)// the issue's comment log
```

```go
type BlockedIssue struct {
    Issue     *Issue
    BlockedBy []Ref
}

// Page is a windowed List/Find result plus the total number of matches in scope
// (before Offset/Limit) — the value a viewer needs to size a scrollbar.
type Page struct {
    Issues []*Issue // the window: matches[Offset : Offset+Limit] (matches[Offset:] when Limit == 0)
    Total  int      // total matches in scope, before Offset/Limit
}

// FindOptions is the presentation subset of Filter (scope/sort/paging) used with a
// Criteria. The selection comes from the Criteria, not from an Expr. Offset/Limit
// behave exactly as on Filter (§3): negatives clamp to 0, Limit 0 = no limit.
type FindOptions struct {
    IncludeClosed bool
    Sort          SortField
    Reverse       bool
    Offset        int
    Limit         int
}
```

- **`All`** returns only the hot (active) set — it never descends into `closed/` or
  `comments/`. It is the cheapest scan: O(open issues), regardless of how many
  closed issues exist. Use `List(Filter{IncludeClosed:true})` to read history.
- **`Query`** / **`List`** parse and evaluate the **filter-expression language**;
  the engine is its sole implementation (the CLI just forwards a string). The
  grammar, fields, operators, and error semantics are defined in
  [QUERY-SPEC.md](QUERY-SPEC.md); a malformed expression returns a `*ParseError`
  (§6), not a match.
- **`Query`** / **`List`** read the active set by default; passing
  `IncludeClosed:true` **or** an expression that satisfies the cold-scope predicate
  (QUERY-SPEC.md §5) includes the cold partition. See Filter scope semantics in §3.
- **`ListPage`** runs the same selection/sort/paging as `List` and additionally
  returns `Total` — the count of all matches in scope **before** `Offset`/`Limit`
  are applied. The window and the total come from one directory snapshot, so a
  paging viewer never races a separate count against the page. Every sort has an `id`
  tie-break, so ordering is **deterministic for a given store state**; but each call
  is its own snapshot, so paging is not isolated across calls — a store mutated
  between page fetches can skip or repeat an item at a window boundary.
- **`Find`** / **`FindPage`** are `Criteria.Build` + `List` / `ListPage`:
  `Find(c, opt) ≡ List(Filter{Expr: c.Build(), …})`. Cold scope is derived by
  applying the cold-scope predicate (QUERY-SPEC.md §5) to the **built expression** —
  the same detector `List` uses — so a `Criteria` and its hand-written `Expr` always
  scope identically; `FindOptions.IncludeClosed` is the explicit override. If
  `Criteria.Build` fails (the `*ValidationError` cases above — unknown `Status` /
  `Type`, or a negative priority bound), that error is returned and no scan runs.
- **`Ready`** / **`Blocked`** / **`Labels`** are always hot-only. They are O(open)
  and never read `closed/`. Use `List(Filter{IncludeClosed:true})` for history.
- **`Detail`** resolves `ParentRef`, `BlockedBy`, `Related`, computes `Blocks` and
  `Children` by scanning, and loads `Comments` from the sidecar.
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

---

## 5. Serialization

```go
func Marshal(iss *Issue) ([]byte, error)   // Issue → on-disk file bytes
func Unmarshal(data []byte) (*Issue, error) // file bytes → Issue
```

Exposed for tools that need to read or render a single file without a `Store`.
Output conforms to the storage spec (frontmatter + verbatim markdown body).

---

## 6. Errors & validation

Sentinel errors, testable with `errors.Is`:

```go
var (
    ErrNotFound           // issue not found
    ErrNoStore            // no store found (no local .tasks and no registry match)
    ErrStoreExists        // a store already exists at the create target
    ErrImmutable          // attempted in-place write to a closed issue (closed/ partition)
    ErrStoreNotRegistered // --store-name names a store with no registry entry (CONFIG-SPEC §4)
    ErrAmbiguousOverride  // both a store-path and a store-name override were supplied
)
```

`Resolve` returns `ErrNoStore` when neither a local store nor a registry match is
found, `ErrStoreNotRegistered` when an explicit `StoreName` has no entry, and
`ErrAmbiguousOverride` when both `StorePath` and `StoreName` are set. A corrupt
`config.yaml` or `mapping.yaml`, or a registry with a duplicate canonical `path`,
is reported as a (non-sentinel) configuration error (CONFIG-SPEC §2–§3).

`ErrImmutable` is returned by `Update` (ordinary field edits), `AddDep`, and
`RemoveDep` when the target issue lives in `closed/` (closed issues are immutable
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
