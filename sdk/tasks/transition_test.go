package tasks

import (
	"testing"
	"time"
)

// L1: classify, cloneIssue, and the no-op predicate are pure. HOOK-SPEC §2.1.

func TestClassify(t *testing.T) {
	open := &Issue{Status: StatusOpen}
	inProg := &Issue{Status: StatusInProgress}
	closed := &Issue{Status: StatusClosed}

	cases := []struct {
		name string
		old  *Issue
		new  *Issue
		want transition
	}{
		{"create (no old)", nil, open, transCreate},
		{"open -> closed is close", open, closed, transClose},
		{"in_progress -> closed is close", inProg, closed, transClose},
		{"closed -> open is reopen", closed, open, transReopen},
		{"closed -> in_progress is reopen", closed, inProg, transReopen},
		{"open -> in_progress is update", open, inProg, transUpdate},
		{"open -> open is update", open, open, transUpdate},
		{"closed -> closed is update", closed, closed, transUpdate},
	}
	for _, c := range cases {
		if got := classify(c.old, c.new); got != c.want {
			t.Errorf("%s: classify = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTransitionEventNames(t *testing.T) {
	if transClose.preEvent() != "pre-close" || transClose.postEvent() != "post-close" {
		t.Errorf("close events = %q/%q", transClose.preEvent(), transClose.postEvent())
	}
	if transCreate.preEvent() != "pre-create" || transReopen.postEvent() != "post-reopen" {
		t.Errorf("unexpected event names")
	}
}

func TestCloneIssue_IsDeep(t *testing.T) {
	orig := &Issue{
		ID:        "x-1",
		Labels:    []string{"a", "b"},
		BlockedBy: []string{"x-2"},
		Related:   []string{"x-3"},
	}
	c := cloneIssue(orig)
	c.Labels[0] = "mutated"
	c.BlockedBy = append(c.BlockedBy, "x-9")
	if orig.Labels[0] != "a" {
		t.Error("cloneIssue must deep-copy Labels")
	}
	if len(orig.BlockedBy) != 1 {
		t.Error("cloneIssue must deep-copy BlockedBy")
	}
	if cloneIssue(nil) != nil {
		t.Error("cloneIssue(nil) must be nil")
	}
}

func TestIssuesEqualIgnoringUpdated(t *testing.T) {
	base := func() *Issue {
		return &Issue{
			ID: "x-1", Title: "t", Status: StatusOpen, Type: TypeTask, Priority: 2,
			Labels: []string{"a"}, Created: time.Unix(100, 0), Updated: time.Unix(100, 0),
			Description: "body",
		}
	}
	a := base()

	// Identical except Updated -> equal (the whole point).
	b := base()
	b.Updated = time.Unix(999, 0)
	if !issuesEqualIgnoringUpdated(a, b) {
		t.Error("issues differing only in Updated must be equal (no-op)")
	}

	// Each meaningful field difference -> not equal.
	diffs := map[string]func(*Issue){
		"title":        func(i *Issue) { i.Title = "other" },
		"status":       func(i *Issue) { i.Status = StatusInProgress },
		"priority":     func(i *Issue) { i.Priority = 0 },
		"description":  func(i *Issue) { i.Description = "x" },
		"labels value": func(i *Issue) { i.Labels = []string{"b"} },
		"labels order": func(i *Issue) { i.Labels = []string{"a", "c"} },
		"created":      func(i *Issue) { i.Created = time.Unix(200, 0) },
		"close reason": func(i *Issue) { i.CloseReason = "r" },
	}
	for name, mut := range diffs {
		c := base()
		mut(c)
		if issuesEqualIgnoringUpdated(a, c) {
			t.Errorf("difference in %s must make issues unequal", name)
		}
	}

	if issuesEqualIgnoringUpdated(nil, a) || !issuesEqualIgnoringUpdated(nil, nil) {
		t.Error("nil handling: (nil,x)=false, (nil,nil)=true")
	}
}

// L2: a redundant Update (fields set to their current values) writes nothing and
// does not advance Updated — the engine-level no-op (HOOK-SPEC §2.1).
func TestUpdate_NoOpWritesNothing(t *testing.T) {
	s := newTestStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "orig", Description: "body"})

	// Advance the clock so a write, if it happened, would visibly bump Updated.
	s.SetNow(func() time.Time { return iss.Updated.Add(time.Hour) })

	sameTitle := "orig"
	out, err := s.Update(iss.ID, UpdateInput{Title: &sameTitle})
	if err != nil {
		t.Fatalf("redundant Update: %v", err)
	}
	if !out.Updated.Equal(iss.Updated) {
		t.Errorf("no-op Update must not advance Updated: %v -> %v", iss.Updated, out.Updated)
	}
	reloaded, _ := s.Get(iss.ID)
	if !reloaded.Updated.Equal(iss.Updated) {
		t.Errorf("no-op Update must not write: on-disk Updated changed to %v", reloaded.Updated)
	}

	// A real change still advances Updated.
	newTitle := "changed"
	out2, err := s.Update(iss.ID, UpdateInput{Title: &newTitle})
	if err != nil {
		t.Fatal(err)
	}
	if !out2.Updated.After(iss.Updated) {
		t.Errorf("real Update must advance Updated: %v -> %v", iss.Updated, out2.Updated)
	}
}
