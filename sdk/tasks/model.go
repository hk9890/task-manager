package tasks

import "time"

// Status is the lifecycle state of an issue. The set is fixed and small by
// design: there are no custom or configurable statuses.
type Status string

const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusBlocked    Status = "blocked"
	StatusClosed     Status = "closed"
)

// Statuses lists every valid status in display order.
var Statuses = []Status{StatusOpen, StatusInProgress, StatusBlocked, StatusClosed}

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	switch s {
	case StatusOpen, StatusInProgress, StatusBlocked, StatusClosed:
		return true
	default:
		return false
	}
}

// IsClosed reports whether s represents a completed issue. A blocker counts as
// resolved only when it is closed.
func (s Status) IsClosed() bool { return s == StatusClosed }

// Type is the kind of work an issue represents. The set is fixed and small by
// design: there are no custom or configurable types.
type Type string

const (
	TypeTask    Type = "task"
	TypeBug     Type = "bug"
	TypeFeature Type = "feature"
	TypeEpic    Type = "epic"
	TypeChore   Type = "chore"
)

// Types lists every valid type in display order.
var Types = []Type{TypeTask, TypeBug, TypeFeature, TypeEpic, TypeChore}

// Valid reports whether t is a known type.
func (t Type) Valid() bool {
	switch t {
	case TypeTask, TypeBug, TypeFeature, TypeEpic, TypeChore:
		return true
	default:
		return false
	}
}

// Priority bounds. Lower is more urgent, matching the beads convention
// (0 = critical .. 4 = trivial).
const (
	PriorityMin     = 0
	PriorityMax     = 4
	PriorityDefault = 2
)

// Comment is an immutable, append-only note on an issue. It is the durable
// record of decisions and progress. Each comment is stored as one YAML document
// in the issue's sidecar file (.tasks/comments/<id>.yml).
type Comment struct {
	ID       string    `yaml:"id,omitempty"`       // opaque random token, ^[0-9a-z]{8}$
	Author   string    `yaml:"author,omitempty"`
	Created  time.Time `yaml:"created"`
	Replaces string    `yaml:"replaces,omitempty"` // ID of an earlier comment this supersedes
	Deleted  bool      `yaml:"deleted,omitempty"`  // true → tombstone (no Body)
	Body     string    `yaml:"body,omitempty"`
}

// Issue is the complete in-memory model of a single task file. It is the unit
// of storage: exactly one issue per file on disk.
//
// Only one direction of each relationship is stored: Parent, BlockedBy and
// Related live on the dependent issue. The inverse edges (children, "blocks")
// are always derived by scanning, never written, so the on-disk graph cannot
// disagree with itself.
type Issue struct {
	ID       string
	Title    string
	Status   Status
	Type     Type
	Priority int
	Assignee string
	Labels   []string

	Parent    string   // ID of the grouping/epic issue, if any
	BlockedBy []string // IDs that must close before this is ready
	Related   []string // non-blocking references

	Created     time.Time
	Updated     time.Time
	Closed      time.Time // zero value means not closed
	CloseReason string

	Description string // free-form markdown body of the file
}

// IsClosed reports whether the issue is in the closed state.
func (i *Issue) IsClosed() bool { return i.Status.IsClosed() }

// Ref is a lightweight reference to an issue, used when presenting
// relationships without loading full bodies.
type Ref struct {
	ID       string
	Title    string
	Type     Type
	Status   Status
	Priority int
}

// Detail is an issue enriched with its derived inverse relationships and
// resolved reference metadata. It is what a viewer (e.g. beads-workbench)
// renders for a single issue.
type Detail struct {
	Issue

	ParentRef *Ref      // resolved Parent, if set and found
	BlockedBy []Ref     // resolved blockers (overrides Issue.BlockedBy IDs for display)
	Related   []Ref     // resolved related issues
	Blocks    []Ref     // derived: issues that list this one in their BlockedBy
	Children  []Ref     // derived: issues whose Parent is this one
	Comments  []Comment // resolved effective comment log (edits applied, tombstones omitted)
}

// ref builds a Ref from an issue.
func ref(i *Issue) Ref {
	return Ref{ID: i.ID, Title: i.Title, Type: i.Type, Status: i.Status, Priority: i.Priority}
}
