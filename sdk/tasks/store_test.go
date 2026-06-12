package tasks

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestStore creates an initialized store in a temp dir with a fixed clock.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	s, err := Init(root, "agt")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}
	return s
}

func mustCreate(t *testing.T, s *Store, in CreateInput) *Issue {
	t.Helper()
	iss, err := s.Create(in)
	if err != nil {
		t.Fatalf("Create(%q): %v", in.Title, err)
	}
	return iss
}

func TestInitRejectsDuplicate(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, "agt"); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(root, "agt"); !errors.Is(err, ErrStoreExists) {
		t.Errorf("expected ErrStoreExists, got %v", err)
	}
}

func TestInitRejectsBadPrefix(t *testing.T) {
	for _, p := range []string{"", "A", "1x", "has-dash", "has space"} {
		if _, err := Init(t.TempDir(), p); err == nil {
			t.Errorf("prefix %q: expected error", p)
		}
	}
}

func TestOpenWalksUp(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, "agt"); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(nested)
	if err != nil {
		t.Fatalf("Open from nested: %v", err)
	}
	if s.Prefix() != "agt" {
		t.Errorf("prefix = %q", s.Prefix())
	}
}

func TestOpenNoStore(t *testing.T) {
	if _, err := Open(t.TempDir()); !errors.Is(err, ErrNoStore) {
		t.Errorf("expected ErrNoStore, got %v", err)
	}
}

func TestCreateAllocatesSequentialIDs(t *testing.T) {
	s := newTestStore(t)
	a := mustCreate(t, s, CreateInput{Title: "first"})
	b := mustCreate(t, s, CreateInput{Title: "second"})
	if a.ID != "agt-0001" || b.ID != "agt-0002" {
		t.Fatalf("ids = %q, %q", a.ID, b.ID)
	}
	// Defaults applied.
	if a.Type != TypeTask || a.Priority != PriorityDefault || a.Status != StatusOpen {
		t.Errorf("defaults wrong: %+v", a)
	}
}

func TestCreateValidates(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(CreateInput{Title: "  "}); err == nil {
		t.Error("empty title should fail")
	}
	p := 9
	if _, err := s.Create(CreateInput{Title: "x", Priority: &p}); err == nil {
		t.Error("out-of-range priority should fail")
	}
	if _, err := s.Create(CreateInput{Title: "x", Type: Type("nonsense")}); err == nil {
		t.Error("unknown type should fail")
	}
}

func TestCreateRejectsMissingRefs(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(CreateInput{Title: "x", BlockedBy: []string{"agt-9999"}}); err == nil {
		t.Error("missing blocker should fail")
	}
	if _, err := s.Create(CreateInput{Title: "x", Parent: "agt-9999"}); err == nil {
		t.Error("missing parent should fail")
	}
}

func TestGetNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Get("agt-0001"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdatePartial(t *testing.T) {
	s := newTestStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "orig"})

	newTitle := "changed"
	pr := 0
	st := StatusInProgress
	out, err := s.Update(iss.ID, UpdateInput{Title: &newTitle, Priority: &pr, Status: &st})
	if err != nil {
		t.Fatal(err)
	}
	if out.Title != "changed" || out.Priority != 0 || out.Status != StatusInProgress {
		t.Errorf("update not applied: %+v", out)
	}
	if !out.Updated.After(iss.Updated) {
		t.Errorf("Updated should advance: %v -> %v", iss.Updated, out.Updated)
	}
	// Untouched fields remain.
	reloaded, _ := s.Get(iss.ID)
	if reloaded.Type != TypeTask {
		t.Errorf("type should be unchanged, got %v", reloaded.Type)
	}
}

func TestUpdateLabels(t *testing.T) {
	s := newTestStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "x", Labels: []string{"a", "b"}})

	out, _ := s.Update(iss.ID, UpdateInput{AddLabels: []string{"c"}, RemoveLabels: []string{"a"}})
	if got := labelSet(out.Labels); !got["b"] || !got["c"] || got["a"] {
		t.Errorf("add/remove labels wrong: %v", out.Labels)
	}

	out, _ = s.Update(iss.ID, UpdateInput{SetLabels: []string{"x", "y"}})
	if len(out.Labels) != 2 || out.Labels[0] != "x" {
		t.Errorf("set labels wrong: %v", out.Labels)
	}

	out, _ = s.Update(iss.ID, UpdateInput{ClearLabels: true})
	if len(out.Labels) != 0 {
		t.Errorf("clear labels wrong: %v", out.Labels)
	}
}

func TestStatusClosedStampsAndClears(t *testing.T) {
	s := newTestStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "x"})

	closedStatus := StatusClosed
	out, _ := s.Update(iss.ID, UpdateInput{Status: &closedStatus})
	if out.Closed.IsZero() {
		t.Error("closing via update should stamp Closed")
	}
	openStatus := StatusOpen
	out, _ = s.Update(iss.ID, UpdateInput{Status: &openStatus})
	if !out.Closed.IsZero() || out.CloseReason != "" {
		t.Errorf("reopening should clear Closed/reason: %v / %q", out.Closed, out.CloseReason)
	}
}

func TestCloseIdempotent(t *testing.T) {
	s := newTestStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "x"})

	first, err := s.Close(iss.ID, "done")
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != StatusClosed || first.CloseReason != "done" {
		t.Errorf("close wrong: %+v", first)
	}
	// CLI-SPEC §"atctl close" says Close is idempotent. Re-closing an
	// already-closed issue must succeed (nil error) and return the existing
	// closed issue unchanged. The new reason is ignored; original is preserved.
	second, err := s.Close(iss.ID, "again")
	if err != nil {
		t.Errorf("re-close must succeed (idempotent), got: %v", err)
	}
	if second == nil || second.Status != StatusClosed {
		t.Errorf("re-close must return closed issue, got: %+v", second)
	}
	if second.CloseReason != "done" {
		t.Errorf("re-close must preserve original close_reason %q, got %q", "done", second.CloseReason)
	}
}

func TestAddComment(t *testing.T) {
	s := newTestStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "x"})
	// sanitizeCommentBody strips trailing whitespace per line, not leading.
	// "a note\n" is a clean body; use it directly.
	c, err := s.AddComment(iss.ID, "hans", "a note\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Body != "a note\n" || c.Author != "hans" {
		t.Errorf("comment wrong: %+v", c)
	}
	if len(c.ID) != 8 {
		t.Errorf("comment ID length = %d, want 8", len(c.ID))
	}
	// Verify via Comments() that it's stored in the sidecar.
	comments, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(comments) != 1 || comments[0].ID != c.ID {
		t.Errorf("Comments() = %+v, want 1 comment with id %q", comments, c.ID)
	}
}

func TestAddDepAndCycle(t *testing.T) {
	s := newTestStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	b := mustCreate(t, s, CreateInput{Title: "b"})

	if err := s.AddDep(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	// Idempotent.
	if err := s.AddDep(a.ID, b.ID); err != nil {
		t.Fatalf("re-add dep: %v", err)
	}
	reloaded, _ := s.Get(a.ID)
	if len(reloaded.BlockedBy) != 1 {
		t.Errorf("expected one blocker, got %v", reloaded.BlockedBy)
	}
	// Adding the reverse edge would close a cycle and must fail.
	if err := s.AddDep(b.ID, a.ID); err == nil {
		t.Error("expected cycle rejection")
	}
	if err := s.AddDep(a.ID, a.ID); err == nil {
		t.Error("self-dependency should fail")
	}
}

// TestAddDep_TransitiveCycle covers the multi-hop cycle that the direct 2-node
// case in TestAddDepAndCycle never reaches: a -> b -> c -> a. Closing the loop
// must exercise findCycle's gray back-edge / stack-slicing branch.
func TestAddDep_TransitiveCycle(t *testing.T) {
	s := newTestStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	b := mustCreate(t, s, CreateInput{Title: "b"})
	c := mustCreate(t, s, CreateInput{Title: "c"})

	// A valid deep chain a -> b -> c (a blocked by b, b blocked by c) is fine.
	if err := s.AddDep(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDep(b.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	// Closing the loop transitively (c blocked by a) forms a 3-node cycle and
	// must be rejected — this is the boundary the direct 2-node case misses.
	if err := s.AddDep(c.ID, a.ID); err == nil {
		t.Error("expected transitive cycle a -> b -> c -> a to be rejected")
	}
	// The rejected edge must not have been persisted.
	if reloaded, _ := s.Get(c.ID); len(reloaded.BlockedBy) != 0 {
		t.Errorf("rejected cycle edge leaked: c.BlockedBy = %v, want empty", reloaded.BlockedBy)
	}
}

func TestRemoveDep(t *testing.T) {
	s := newTestStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	b := mustCreate(t, s, CreateInput{Title: "b"})
	if err := s.AddDep(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveDep(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := s.Get(a.ID)
	if len(reloaded.BlockedBy) != 0 {
		t.Errorf("blocker not removed: %v", reloaded.BlockedBy)
	}
}

func TestAtomicWriteLeavesNoTemp(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, CreateInput{Title: "x"})
	entries, _ := os.ReadDir(s.Dir())
	for _, e := range entries {
		if filepath.Ext(e.Name()) == "" && e.Name() != ConfigFileName && e.Name() != lockFileName {
			t.Errorf("unexpected leftover file: %s", e.Name())
		}
	}
}

func labelSet(ls []string) map[string]bool {
	m := map[string]bool{}
	for _, l := range ls {
		m[l] = true
	}
	return m
}
