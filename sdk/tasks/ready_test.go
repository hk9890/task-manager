package tasks

import "testing"

func TestReadyAndBlocked(t *testing.T) {
	s := newTestStore(t)
	blocker := mustCreate(t, s, CreateInput{Title: "blocker"})
	dependent := mustCreate(t, s, CreateInput{Title: "dependent", BlockedBy: []string{blocker.ID}})
	free := mustCreate(t, s, CreateInput{Title: "free"})

	ready, err := s.Ready()
	if err != nil {
		t.Fatal(err)
	}
	if !containsID(ready, blocker.ID) || !containsID(ready, free.ID) {
		t.Errorf("blocker and free should be ready: %v", ids(ready))
	}
	if containsID(ready, dependent.ID) {
		t.Errorf("dependent must not be ready while blocker is open")
	}

	blocked, err := s.Blocked()
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 1 || blocked[0].Issue.ID != dependent.ID {
		t.Fatalf("expected dependent blocked, got %+v", blocked)
	}
	if len(blocked[0].BlockedBy) != 1 || blocked[0].BlockedBy[0].ID != blocker.ID {
		t.Errorf("blocker ref wrong: %+v", blocked[0].BlockedBy)
	}

	// Closing the blocker makes the dependent ready.
	if _, err := s.Close(blocker.ID, ""); err != nil {
		t.Fatal(err)
	}
	ready, _ = s.Ready()
	if !containsID(ready, dependent.ID) {
		t.Errorf("dependent should be ready after blocker closed: %v", ids(ready))
	}
}

func TestReadyExcludesNonOpen(t *testing.T) {
	s := newTestStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "x"})
	ip := StatusInProgress
	if _, err := s.Update(iss.ID, UpdateInput{Status: &ip}); err != nil {
		t.Fatal(err)
	}
	ready, _ := s.Ready()
	if containsID(ready, iss.ID) {
		t.Errorf("in_progress issue should not be ready")
	}
}

func TestReadyOrderingByPriority(t *testing.T) {
	s := newTestStore(t)
	p3 := 3
	p0 := 0
	low := mustCreate(t, s, CreateInput{Title: "low", Priority: &p3})
	high := mustCreate(t, s, CreateInput{Title: "high", Priority: &p0})

	ready, _ := s.Ready()
	if len(ready) < 2 || ready[0].ID != high.ID || ready[1].ID != low.ID {
		t.Errorf("expected priority order [%s,%s], got %v", high.ID, low.ID, ids(ready))
	}
}

func TestDetailDerivesInverseEdges(t *testing.T) {
	s := newTestStore(t)
	epic := mustCreate(t, s, CreateInput{Title: "epic", Type: TypeEpic})
	child := mustCreate(t, s, CreateInput{Title: "child", Parent: epic.ID})
	blocked := mustCreate(t, s, CreateInput{Title: "blocked", BlockedBy: []string{epic.ID}})
	related := mustCreate(t, s, CreateInput{Title: "related", Related: []string{epic.ID}})

	d, err := s.Detail(epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Children) != 1 || d.Children[0].ID != child.ID {
		t.Errorf("children wrong: %+v", d.Children)
	}
	if len(d.Blocks) != 1 || d.Blocks[0].ID != blocked.ID {
		t.Errorf("blocks wrong: %+v", d.Blocks)
	}

	// The dependent's Detail should resolve its blocker ref and parent ref.
	cd, _ := s.Detail(child.ID)
	if cd.ParentRef == nil || cd.ParentRef.ID != epic.ID {
		t.Errorf("parent ref wrong: %+v", cd.ParentRef)
	}
	_ = related
}

func TestListFilters(t *testing.T) {
	s := newTestStore(t)
	p1 := 1
	bug := mustCreate(t, s, CreateInput{Title: "a bug", Type: TypeBug, Priority: &p1, Labels: []string{"x"}})
	mustCreate(t, s, CreateInput{Title: "a task", Type: TypeTask, Labels: []string{"y"}})
	done := mustCreate(t, s, CreateInput{Title: "done task"})
	if _, err := s.Close(done.ID, ""); err != nil {
		t.Fatal(err)
	}

	// Default excludes closed.
	open, _ := s.List(Filter{})
	if containsID(open, done.ID) {
		t.Errorf("closed should be excluded by default: %v", ids(open))
	}

	// Type filter via Expr.
	bugs, _ := s.List(Filter{Expr: `type == bug`})
	if len(bugs) != 1 || bugs[0].ID != bug.ID {
		t.Errorf("type filter wrong: %v", ids(bugs))
	}

	// Label filter via Expr.
	labeled, _ := s.List(Filter{Expr: `label == "x"`})
	if len(labeled) != 1 || labeled[0].ID != bug.ID {
		t.Errorf("label filter wrong: %v", ids(labeled))
	}

	// Text filter via Expr (case-insensitive ~).
	found, _ := s.List(Filter{Expr: `text ~ "BUG"`})
	if len(found) != 1 || found[0].ID != bug.ID {
		t.Errorf("text filter wrong: %v", ids(found))
	}

	// Status filter via Expr: closed scope auto-included.
	closed, _ := s.List(Filter{Expr: `status == "closed"`})
	if len(closed) != 1 || closed[0].ID != done.ID {
		t.Errorf("status filter wrong: %v", ids(closed))
	}

	// Limit.
	limited, _ := s.List(Filter{IncludeClosed: true, Limit: 2})
	if len(limited) != 2 {
		t.Errorf("limit wrong: %v", ids(limited))
	}
}

func containsID(issues []*Issue, id string) bool {
	for _, i := range issues {
		if i.ID == id {
			return true
		}
	}
	return false
}

func ids(issues []*Issue) []string {
	out := make([]string, len(issues))
	for i, iss := range issues {
		out[i] = iss.ID
	}
	return out
}
