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
    ID       int       // per-issue, monotonic from 1
    Author   string
    Created  time.Time
    Replaces int       // 0, or the ID of a comment this one supersedes
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

### Filtering

```go
type Filter struct {
    Expr          string    // filter expression — the selector (CLI spec §3.1)
    IncludeClosed bool      // scope: include closed issues
    Sort          SortField // presentation
    Reverse       bool
    Limit         int
}

type SortField string
const (
    SortWork     SortField = "" // priority then created (default)
    SortID; SortPriority; SortCreated; SortUpdated; SortClosed
)
```

---

## 4. Store methods

### Read (lock-free)

```go
func (s *Store) Get(id string) (*Issue, error)        // one issue (falls through to closed/)
func (s *Store) All() ([]*Issue, error)               // active set, sorted by ID
func (s *Store) Query(expr string) ([]*Issue, error)  // select by filter expression (CLI spec §3.1)
func (s *Store) List(f Filter) ([]*Issue, error)      // Query + scope/sort/limit via Filter
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
```

- **`Query`** / **`List`** / **`Ready`** / **`Blocked`** read the active set by
  default; passing `IncludeClosed` (or an expression that selects closed issues)
  reads the cold partition too.
- **`Detail`** resolves `ParentRef`, `BlockedBy`, `Related`, computes `Blocks` and
  `Children` by scanning, and loads `Comments` from the sidecar.

### Write (validated, under the lock)

```go
func (s *Store) Create(in CreateInput) (*Issue, error)
func (s *Store) Update(id string, in UpdateInput) (*Issue, error)
func (s *Store) Close(id, reason string) (*Issue, error)   // idempotent; moves to closed/
func (s *Store) Reopen(id string) (*Issue, error)          // moves back to active
func (s *Store) AddComment(id, author, body string) (*Issue, error)
func (s *Store) EditComment(id string, commentID int, author, body string) (*Issue, error)
func (s *Store) AddDep(dependent, blocker string) error    // idempotent; rejects self/cycle
func (s *Store) RemoveDep(dependent, blocker string) error
```

- Every write allocates/validates, then performs an atomic file write while holding
  the project lock.
- **`Create`** allocates the next ID, applies defaults (`TypeTask`,
  `PriorityDefault`, `StatusOpen`), de-duplicates labels/edges, and validates.
- **`AddComment`** appends to the sidecar (sanitizing the body); **`EditComment`**
  appends a revision with `Replaces` set.
- **`Close`** sets the closed timestamp/reason and relocates the file; **`Reopen`**
  reverses it.

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
)
```

Validation failures return a typed error carrying the offending field:

```go
type ValidationError struct { Field, Message string }
func (e *ValidationError) Error() string
```

The store rejects (before any byte is written) every invariant listed in the
storage spec: empty title, unknown enum, priority out of range, self/duplicate/
dangling references, dependency cycles, and field-constraint violations.

---

## 7. Concurrency & durability contract

- **One writer per project.** Every mutating method runs under an exclusive
  advisory lock (`flock`) on the project. Concurrent processes serialize; reads are
  lock-free.
- **Atomic writes.** Each file write is temp-file + `fsync` + `rename`, so a reader
  never observes a torn file.
- **Read consistency.** Read methods take a fresh directory snapshot per call; a
  returned `*Issue` is a copy the caller may mutate without affecting disk.

---

## 8. Stability

The exported surface in this document is the public contract. `Store` internals are
opaque. Additive changes (new methods, new optional struct fields) are
non-breaking; signature or semantic changes are breaking and are versioned at the
module level.
