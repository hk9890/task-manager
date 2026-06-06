# SDK Specification — `sdk/tasks`

This document specifies the public Go API of the storage engine, the package every
consumer imports to read and write a `.tasks` store. It is the single owner of file
access; the on-disk format it produces is defined in
[TASK-STORAGE-SPEC.md](TASK-STORAGE-SPEC.md).

```go
import "github.com/hk9890/agent-tasks/sdk/tasks"
```

The package is its own Go module (minimal dependencies) so consumers can import it
without pulling in any CLI dependencies.

---

## 1. Opening a store

```go
func Init(root, prefix string) (*Store, error)
func Open(start string) (*Store, error)
```

- **`Init`** creates a new project store under `root` with the given ID `prefix`
  and returns it open. Fails if a store already exists (`ErrStoreExists`) or the
  prefix is invalid.
- **`Open`** locates a store by walking up from `start` (or the current working
  directory if `start == ""`) and loads its config. Returns `ErrNoStore` if none
  is found.

`Store` is the single gateway to a project's files; every read and write goes
through it. It is safe to use from one process; cross-process safety is provided by
the write lock (§7).

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
}

func (i *Issue) IsClosed() bool
```

Only the outgoing edges (`Parent`, `BlockedBy`, `Related`) are stored. Inverse
edges are derived (see `Detail`). Comments are **not** carried on `Issue`; they
live in the sidecar and are loaded on demand (§4, `Detail` / `Comments`).

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
    ParentRef *Ref   // resolved parent
    BlockedBy []Ref  // resolved blockers
    Related   []Ref  // resolved related
    Blocks    []Ref  // derived: issues blocked by this one
    Children  []Ref  // derived: issues whose parent is this one
    Comments  []Comment
}
```

### Enums and bounds

```go
type Status string
const ( StatusOpen; StatusInProgress; StatusBlocked; StatusClosed )
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
```

`UpdateInput` uses pointers so the zero value means "leave unchanged"; only set
fields are applied.

`Creator` is intentionally absent from `UpdateInput`: it is provenance — set once
at creation and never edited afterward.

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
- `Expr` references closed work (e.g. `status == "closed"` or a `closed` date comparison):
  cold partition is auto-included; `IncludeClosed` need not be set explicitly.
- Callers must never rely on the cold partition being scanned silently — they must
  explicitly opt in. `All()`, `Ready()`, `Blocked()`, and `Labels()` are always hot-only.

### Structured criteria

`Criteria` is a typed, composable description of a selection. It **compiles** to a
canonical filter expression (QUERY-SPEC.md) that is fed to the existing engine — it
is a convenience for structured callers, **not** a second selection engine.

```go
type Criteria struct {
    Text        string   // text ~ "..."
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

type WorkState int
const (
    WorkAny WorkState = iota // no ready/blocked constraint
    WorkReady                // -> bare `ready`
    WorkBlocked              // -> bare `blocked`
)

// Build compiles the criteria to a canonical filter expression. It is the single
// owner of value quoting/escaping and precedence. Pure; no filesystem access. The
// zero value compiles to "" (the always-true predicate). For well-formed input the
// result always parses (it never yields a *ParseError); an unknown Status or Type
// is reported as an error.
func (c Criteria) Build() (string, error)
```

Compilation: every non-empty group is AND-ed at the top level, and each multi-value
group is wrapped in parentheses to protect precedence under the surrounding `&&`.
All user-supplied string, enum, and date values are emitted **quoted** (with
`"`→`\"` and `\`→`\\` escaping per QUERY-SPEC.md §3); the numeric `priority` is
emitted bare. This puts the bareword/quoting rule in exactly one audited place.
`LabelMatch` defaults to `LabelMatchAll` (the issue must carry every listed label).

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
    Issues []*Issue // the window: matches[Offset : Offset+Limit]
    Total  int      // total matches in scope, before Offset/Limit
}

// FindOptions is the presentation subset of Filter (scope/sort/paging) used with a
// Criteria. The selection comes from the Criteria, not from an Expr.
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
  `IncludeClosed:true` **or** using an expression that references closed work
  (e.g. `status == "closed"` or a `closed` date comparison) includes the cold
  partition. See Filter scope semantics in §3.
- **`ListPage`** runs the same selection/sort/paging as `List` and additionally
  returns `Total` — the count of all matches in scope **before** `Offset`/`Limit`
  are applied. The window and the total come from one directory snapshot, so a
  paging viewer never races a separate count against the page. Paging is stable
  across windows because every sort has an `id` tie-break.
- **`Find`** / **`FindPage`** are `Criteria.Build` + `List` / `ListPage`:
  `Find(c, opt) ≡ List(Filter{Expr: c.Build(), …})`. The closed partition is
  auto-derived from the criteria (a `Closed` status, or any `Closed*` bound) exactly
  as it is for the equivalent `Expr`; `FindOptions.IncludeClosed` is the explicit
  override.
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
func (s *Store) Create(in CreateInput) (*Issue, error)
func (s *Store) Update(id string, in UpdateInput) (*Issue, error)
func (s *Store) Close(id, reason string) (*Issue, error)   // idempotent; moves to closed/
func (s *Store) Reopen(id string) (*Issue, error)          // moves back to active
func (s *Store) AddComment(id, author, body string) (*Comment, error)         // returns the new comment (with its id)
func (s *Store) EditComment(id, commentID, author, body string) (*Comment, error) // appends a revision; returns the new effective comment
func (s *Store) DeleteComment(id, commentID, author string) error             // appends a tombstone
func (s *Store) AddDep(dependent, blocker string) error    // idempotent; rejects self/cycle
func (s *Store) RemoveDep(dependent, blocker string) error
```

- Every write allocates/validates, then performs an atomic file write while holding
  the project lock.
- **`Create`** allocates the next ID, applies defaults (`TypeTask`,
  `PriorityDefault`, `StatusOpen`), de-duplicates labels/edges, and validates.
- **`Update`** applies the partial field changes and routes lifecycle transitions
  through the same path the CLI uses: setting `Status` to `closed` routes through the
  close flow (stamps the close time, moves the file to `closed/`); setting a
  non-closed `Status` on a closed issue routes through reopen (moves the file back,
  clears the close fields), then applies the remaining field edits. A plain field
  edit with no status change on a closed issue returns `ErrImmutable`. Callers drive
  a status change entirely through `Update`'s `Status` field — they never dispatch
  `Close` / `Reopen` themselves for it.
- **`AddComment`** appends a new comment and returns it, including its freshly
  allocated random `ID` (the handle a caller needs for a later edit/delete);
  **`EditComment`** appends a revision with `Replaces` set and returns the new
  effective comment; **`DeleteComment`** appends a tombstone (`Replaces` set,
  `Deleted: true`, empty body). All three sanitize the body and run under the store
  lock; none rewrites the task file (the sidecar is append-only, storage §4.4).
- **`Close`** sets the closed timestamp/reason and relocates the file to `closed/`;
  **`Reopen`** moves it back to the hot set, clears the close fields, and sets
  `StatusOpen` (the pre-close status is not persisted).

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
    ErrNotFound      // issue not found
    ErrAlreadyExists // issue already exists
    ErrNoStore       // no .tasks directory found
    ErrStoreExists   // .tasks directory already exists
    ErrImmutable     // attempted in-place write to a closed issue (closed/ partition)
)
```

`ErrImmutable` is returned by `Update` (ordinary field edits), `AddDep`, and
`RemoveDep` when the target issue lives in `closed/` (closed issues are immutable
in place — see storage spec §5). `Close` is idempotent: calling it on an already-closed
issue returns the existing issue and `nil` (no-op; no in-place write to `closed/`
is attempted). Use `Reopen` to restore mutability; `AddComment` / `EditComment` /
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

---

## 7. Concurrency & durability contract

- **One writer per store.** Every mutating method runs under an exclusive advisory
  lock (`flock` on `.tasks/.lock`). Concurrent processes serialize; reads are
  lock-free.
- **Atomic writes.** Each file write is temp-file + `fsync` + `rename`, so a reader
  never observes a torn file — except the append-only comment sidecar (§4), which is
  grown with `O_APPEND` + `fsync` rather than rewritten.
- **Read consistency.** Read methods take a fresh directory snapshot per call; a
  returned `*Issue` is a copy the caller may mutate without affecting disk.

---

## 8. Stability

The exported surface in this document is the public contract. `Store` internals are
opaque. Additive changes (new methods, new optional struct fields) are
non-breaking; signature or semantic changes are breaking and are versioned at the
module level.
