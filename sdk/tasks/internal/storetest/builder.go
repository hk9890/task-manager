// Package storetest provides a declarative fixture builder for constructing
// populated tasks.Store instances in tests. It is a regular (non-_test.go)
// package so any package's test files can import it; because only test files
// import it, it never ships in a binary.
//
// Usage:
//
//	st := storetest.New(t).
//	    Issue("tst-0001", storetest.Open, storetest.Parent("tst-0007")).
//	    Closed("tst-0007").
//	    Comment("tst-0001", "hans", "first note")
//	store := st.Mem()        // L2: in-memory, instant
//	store  = st.TempDir(t)   // L3: materialized on real osFS
package storetest

import (
	"testing"
	"time"

	"github.com/hk9890/agent-tasks/sdk/tasks"
	"github.com/hk9890/agent-tasks/sdk/tasks/internal/vfs"
)

// issueSpec holds the declarative description of one issue to create.
type issueSpec struct {
	id       string
	closed   bool
	status   tasks.Status
	priority int
	issType  tasks.Type
	parent   string
	blockers []string
	labels   []string
	title    string
}

// commentSpec holds the declarative description of one comment to add.
type commentSpec struct {
	issueID string
	author  string
	body    string
}

// Builder accumulates a declarative store spec. Call Mem or TempDir to
// materialise it.
type Builder struct {
	t        *testing.T
	prefix   string
	issues   []issueSpec
	comments []commentSpec
}

// New returns a new Builder for test t. The default prefix is "tst".
func New(t *testing.T) *Builder {
	t.Helper()
	return &Builder{
		t:      t,
		prefix: "tst",
	}
}

// Opt is a functional option that modifies an issueSpec.
type Opt func(*issueSpec)

// Open sets the issue status to open (this is the default).
var Open Opt = func(s *issueSpec) { s.status = tasks.StatusOpen }

// InProgress sets the issue status to in_progress.
var InProgress Opt = func(s *issueSpec) { s.status = tasks.StatusInProgress }

// Priority sets the issue priority (0 = most urgent, 4 = least).
func Priority(p int) Opt {
	return func(s *issueSpec) { s.priority = p }
}

// Parent sets the parent issue ID.
func Parent(id string) Opt {
	return func(s *issueSpec) { s.parent = id }
}

// BlockedBy adds one or more blocker IDs to the issue.
func BlockedBy(ids ...string) Opt {
	return func(s *issueSpec) { s.blockers = append(s.blockers, ids...) }
}

// Label adds one or more labels to the issue.
func Label(ls ...string) Opt {
	return func(s *issueSpec) { s.labels = append(s.labels, ls...) }
}

// IssueType sets the issue type.
func IssueType(tp tasks.Type) Opt {
	return func(s *issueSpec) { s.issType = tp }
}

// Issue registers an open issue with the given ID. Apply functional Opt values
// to override defaults (status, priority, parent, blockers, labels, type).
func (b *Builder) Issue(id string, opts ...Opt) *Builder {
	b.t.Helper()
	spec := issueSpec{
		id:       id,
		status:   tasks.StatusOpen,
		priority: tasks.PriorityDefault,
		issType:  tasks.TypeTask,
		title:    id, // use the ID as the title for simplicity
	}
	for _, o := range opts {
		o(&spec)
	}
	b.issues = append(b.issues, spec)
	return b
}

// Closed registers a closed issue with the given ID. Opts can override other
// fields; the status is always set to closed.
func (b *Builder) Closed(id string, opts ...Opt) *Builder {
	b.t.Helper()
	spec := issueSpec{
		id:       id,
		closed:   true,
		status:   tasks.StatusClosed,
		priority: tasks.PriorityDefault,
		issType:  tasks.TypeTask,
		title:    id,
	}
	for _, o := range opts {
		o(&spec)
	}
	spec.status = tasks.StatusClosed // enforce closed regardless of opts
	b.issues = append(b.issues, spec)
	return b
}

// Comment registers a comment to be added to the issue with issueID.
func (b *Builder) Comment(issueID, author, body string) *Builder {
	b.t.Helper()
	b.comments = append(b.comments, commentSpec{issueID: issueID, author: author, body: body})
	return b
}

// fixedClock returns a deterministic clock starting at a fixed point in time,
// advancing one second per call.
func fixedClock() func() time.Time {
	tick := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}
}

// materialize populates store s with all registered issues and comments.
// It uses a two-pass strategy so that issue registration order does not
// matter: in the first pass every issue is created without parent/blocker
// references, and in the second pass the references and status are applied.
func (b *Builder) materialize(s *tasks.Store) {
	b.t.Helper()

	// Pass 1: create every issue with title, type, priority and labels only.
	// No parent or blocker refs yet — those may reference issues that do not
	// exist on disk at this point.
	for _, spec := range b.issues {
		p := spec.priority
		in := tasks.CreateInput{
			Title:    spec.title,
			Type:     spec.issType,
			Priority: &p,
			Labels:   spec.labels,
		}
		iss, err := s.Create(in)
		if err != nil {
			b.t.Fatalf("storetest: Create(%q): %v", spec.id, err)
		}
		if iss.ID != spec.id {
			b.t.Fatalf("storetest: Issue(%q): got ID %q — register issues in sequential order matching prefix %q", spec.id, iss.ID, b.prefix)
		}
	}

	// Pass 2: apply parent, blockers, and status for each issue.
	for _, spec := range b.issues {
		var uin tasks.UpdateInput

		needsUpdate := false
		if spec.parent != "" {
			p := spec.parent
			uin.Parent = &p
			needsUpdate = true
		}
		if len(spec.blockers) > 0 {
			for _, blk := range spec.blockers {
				if err := s.AddDep(spec.id, blk); err != nil {
					b.t.Fatalf("storetest: AddDep(%q, %q): %v", spec.id, blk, err)
				}
			}
		}
		if needsUpdate {
			if _, err := s.Update(spec.id, uin); err != nil {
				b.t.Fatalf("storetest: Update(%q, refs): %v", spec.id, err)
			}
		}

		// Apply non-open status last.
		if spec.closed {
			if _, err := s.Close(spec.id, ""); err != nil {
				b.t.Fatalf("storetest: Close(%q): %v", spec.id, err)
			}
		} else if spec.status != tasks.StatusOpen {
			st := spec.status
			if _, err := s.Update(spec.id, tasks.UpdateInput{Status: &st}); err != nil {
				b.t.Fatalf("storetest: Update(%q, status=%q): %v", spec.id, spec.status, err)
			}
		}
	}

	// Pass 3: add comments.
	for _, cs := range b.comments {
		if _, err := s.AddComment(cs.issueID, cs.author, cs.body); err != nil {
			b.t.Fatalf("storetest: AddComment(%q): %v", cs.issueID, err)
		}
	}
}

// Mem materialises the fixture into a vfs.Mem-backed store (L2: fast, no disk
// I/O). The returned store has a deterministic clock.
func (b *Builder) Mem() *tasks.Store {
	b.t.Helper()
	m := vfs.NewMem()
	s, err := tasks.InitWithVFS("/", b.prefix, m)
	if err != nil {
		b.t.Fatalf("storetest: InitWithVFS: %v", err)
	}
	s.SetNow(fixedClock())
	b.materialize(s)
	return s
}

// TempDir materialises the fixture into a real temp-dir-backed store (L3:
// proves durability, real fsync/flock/rename). The returned store has a
// deterministic clock.
func (b *Builder) TempDir(t *testing.T) *tasks.Store {
	t.Helper()
	root := t.TempDir()
	s, err := tasks.Init(root, b.prefix)
	if err != nil {
		t.Fatalf("storetest: Init: %v", err)
	}
	s.SetNow(fixedClock())
	b.materialize(s)
	return s
}
