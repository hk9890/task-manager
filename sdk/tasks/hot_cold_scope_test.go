// L1/L2 tests for hot/cold partition scoping semantics (at-zib.2.4).
//
// L1 (pure): TestExprReferencesClosedWork — tests exprReferencesClosedWork,
// a pure function with no disk access.
//
// L2 (vfs.Mem): the remaining tests exercise Store methods on an in-memory
// FS to prove:
//   - All() is hot-only: never opens closed/ or comments/.
//   - List(Filter{IncludeClosed:true}) returns hot+closed.
//   - Query("status == \"closed\"") triggers closed-scope and returns closed issues.
//   - Ready() and Labels() are hot-only.
package tasks

import (
	"testing"
	"time"

	"github.com/hk9890/agent-tasks/sdk/tasks/internal/vfs"
)

// ── L1: pure-function tests ───────────────────────────────────────────────────

// TestExprReferencesClosedWork verifies the closed-scope detection heuristic
// used by List/Query to decide whether to include the cold partition.
func TestExprReferencesClosedWork(t *testing.T) {
	cases := []struct {
		expr string
		want bool
	}{
		// Empty expression: no closed scope.
		{"", false},
		{"   ", false},

		// status == "closed" (various spacings).
		{`status == "closed"`, true},
		{`status=="closed"`, true},
		{`status != "closed"`, true},

		// closed field comparisons.
		{`closed > "2026-01-01"`, true},
		{`closed < "2026-01-01"`, true},
		{`closed >= "2026-01-01T00:00:00Z"`, true},
		{`closed == "2026-01-01"`, true},

		// Expressions that do NOT reference closed.
		{`status == "open"`, false},
		{`status == "in_progress"`, false},
		{`type == "bug"`, false},
		{`priority <= 2`, false},
		{`text ~ "done"`, false},
		{`assignee == "alice"`, false},

		// Combined expressions that include a closed reference.
		{`status == "closed" && priority <= 2`, true},
		{`type == "bug" || status == "closed"`, true},
		{`closed > "2026-01-01" && assignee == "hans"`, true},
	}

	for _, tc := range cases {
		got := exprReferencesClosedWork(tc.expr)
		if got != tc.want {
			t.Errorf("exprReferencesClosedWork(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

// newScopedMemStore returns a mem-backed store with two open issues and one
// closed issue (in closed/) plus a deterministic clock.
func newScopedMemStore(t *testing.T) (*Store, *vfs.Mem, string, string, string) {
	t.Helper()
	m := vfs.NewMem()
	if err := m.MkdirAll("/.tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s := openWithFS("/", m)
	s.cfg = Config{Prefix: "agt"}
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}

	open1, err := s.Create(CreateInput{Title: "open one", Labels: []string{"area:hot"}})
	if err != nil {
		t.Fatalf("Create open1: %v", err)
	}
	open2, err := s.Create(CreateInput{Title: "open two", Labels: []string{"area:hot"}})
	if err != nil {
		t.Fatalf("Create open2: %v", err)
	}
	closed1, err := s.Create(CreateInput{Title: "closed one", Labels: []string{"area:cold"}})
	if err != nil {
		t.Fatalf("Create closed1: %v", err)
	}
	if _, err := s.Close(closed1.ID, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return s, m, open1.ID, open2.ID, closed1.ID
}

// TestAll_HotSetOnly verifies that All() returns only active (hot) issues and
// never returns closed issues from the cold partition.
func TestAll_HotSetOnly(t *testing.T) {
	s, _, open1ID, open2ID, closedID := newScopedMemStore(t)

	all, err := s.All()
	if err != nil {
		t.Fatalf("All(): %v", err)
	}
	if len(all) != 2 {
		ids := make([]string, len(all))
		for i, iss := range all {
			ids[i] = iss.ID
		}
		t.Errorf("All() = %v (len %d), want [%s %s] (len 2)", ids, len(all), open1ID, open2ID)
	}
	for _, iss := range all {
		if iss.ID == closedID {
			t.Errorf("All() contained closed issue %s — must be hot-set only", closedID)
		}
	}
}

// TestAll_NeverDescendsIntoClosed verifies that All() never reads from the
// closed/ subdirectory by checking the returned issues don't include closed ones
// even when closed/ is populated.
func TestAll_NeverDescendsIntoClosed(t *testing.T) {
	s, m, _, _, closedID := newScopedMemStore(t)

	// Poison the closed/ directory with a file that, if parsed, would cause an
	// error (garbled content) — if All() descends into closed/ the parse would fail.
	poisonPath := "/.tasks/closed/" + closedID + ".md"
	if err := m.WriteAtomic(poisonPath, []byte("GARBLED NOT YAML"), 0o644); err != nil {
		t.Fatalf("WriteAtomic poison: %v", err)
	}

	// All() must not error — it must not even try to read closed/.
	all, err := s.All()
	if err != nil {
		t.Fatalf("All() returned error (likely descended into closed/): %v", err)
	}
	// Should only see the 2 open issues (the closed one's hot-dir file was already
	// removed by Close, so the poisoned file in closed/ is the only one there).
	for _, iss := range all {
		if iss.Status.IsClosed() {
			t.Errorf("All() returned closed issue %s — hot-set only expected", iss.ID)
		}
	}
}

// TestAll_NeverDescendsIntoComments verifies that All() never reads from the
// comments/ subdirectory. Comments are sidecars and must not be mistaken for
// issue files.
func TestAll_NeverDescendsIntoComments(t *testing.T) {
	s, m, open1ID, _, _ := newScopedMemStore(t)

	// Write a garbled file in comments/ — if All() descends, it would try to parse
	// it as an issue and fail.
	garbledPath := "/.tasks/comments/garbage.md"
	if err := m.WriteAtomic(garbledPath, []byte("GARBLED COMMENT FILE"), 0o644); err != nil {
		t.Fatalf("WriteAtomic comments garble: %v", err)
	}

	all, err := s.All()
	if err != nil {
		t.Fatalf("All() returned error (likely descended into comments/): %v", err)
	}
	_ = open1ID
	// Ensure count is still correct (garbled file in comments/ is not parsed).
	for _, iss := range all {
		if iss.ID == "garbage" {
			t.Error("All() parsed a file from comments/ — should skip subdirectories")
		}
	}
}

// TestList_Default_HotOnly verifies that List(Filter{}) returns only active
// (hot) issues, excluding closed issues.
func TestList_Default_HotOnly(t *testing.T) {
	s, _, _, _, closedID := newScopedMemStore(t)

	issues, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List(Filter{}): %v", err)
	}
	for _, iss := range issues {
		if iss.ID == closedID {
			t.Errorf("List(Filter{}) contained closed issue %s — hot-only default violated", closedID)
		}
	}
	if len(issues) != 2 {
		t.Errorf("List(Filter{}) = %d issues, want 2 (hot-only)", len(issues))
	}
}

// TestList_IncludeClosed_ReturnsHotAndCold verifies that
// List(Filter{IncludeClosed:true}) returns both active and closed issues.
func TestList_IncludeClosed_ReturnsHotAndCold(t *testing.T) {
	s, _, open1ID, open2ID, closedID := newScopedMemStore(t)

	issues, err := s.List(Filter{IncludeClosed: true})
	if err != nil {
		t.Fatalf("List(IncludeClosed=true): %v", err)
	}
	if len(issues) != 3 {
		ids := make([]string, len(issues))
		for i, iss := range issues {
			ids[i] = iss.ID
		}
		t.Errorf("List(IncludeClosed=true) = %v (len %d), want 3 (hot+cold)", ids, len(issues))
	}
	have := map[string]bool{}
	for _, iss := range issues {
		have[iss.ID] = true
	}
	for _, id := range []string{open1ID, open2ID, closedID} {
		if !have[id] {
			t.Errorf("List(IncludeClosed=true) missing issue %s", id)
		}
	}
}

// TestQuery_ClosedExpr_IncludesClosed verifies that Query with an expression
// that references closed status automatically includes the cold partition, so
// closed issues are visible in the result.
func TestQuery_ClosedExpr_IncludesClosed(t *testing.T) {
	s, _, _, _, closedID := newScopedMemStore(t)

	// An expression that explicitly asks for closed issues must include the cold
	// partition; otherwise closed issues are unreachable.
	issues, err := s.Query(`status == "closed"`)
	if err != nil {
		t.Fatalf("Query(status==closed): %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("Query(status==\"closed\") returned 0 issues — closed partition not included")
	}
	found := false
	for _, iss := range issues {
		if iss.ID == closedID {
			found = true
		}
		if !iss.Status.IsClosed() {
			t.Errorf("Query(status==\"closed\") returned non-closed issue %s", iss.ID)
		}
	}
	if !found {
		t.Errorf("Query(status==\"closed\") missing closed issue %s", closedID)
	}
}

// TestQuery_EmptyExpr_HotOnly verifies that Query("") behaves like List(Filter{})
// and returns only the active (hot) set.
func TestQuery_EmptyExpr_HotOnly(t *testing.T) {
	s, _, _, _, closedID := newScopedMemStore(t)

	issues, err := s.Query("")
	if err != nil {
		t.Fatalf("Query(\"\"): %v", err)
	}
	for _, iss := range issues {
		if iss.ID == closedID {
			t.Errorf("Query(\"\") returned closed issue %s — should be hot-only by default", closedID)
		}
	}
	if len(issues) != 2 {
		t.Errorf("Query(\"\") = %d issues, want 2 (hot-only)", len(issues))
	}
}

// TestQuery_NonClosedExpr_HotOnly verifies that Query with an expression that
// does not reference closed work stays hot-only.
func TestQuery_NonClosedExpr_HotOnly(t *testing.T) {
	s, _, _, _, closedID := newScopedMemStore(t)

	issues, err := s.Query(`status == "open"`)
	if err != nil {
		t.Fatalf("Query(status==open): %v", err)
	}
	for _, iss := range issues {
		if iss.ID == closedID {
			t.Errorf("Query(status==\"open\") returned closed issue %s — should be hot-only", closedID)
		}
	}
}

// TestLabels_HotOnly verifies that Labels() returns only labels from the active
// (hot) set. A closed issue with a unique label must not appear in Labels().
func TestLabels_HotOnly(t *testing.T) {
	s, _, _, _, _ := newScopedMemStore(t)

	labels, err := s.Labels()
	if err != nil {
		t.Fatalf("Labels(): %v", err)
	}
	for _, l := range labels {
		if l == "area:cold" {
			t.Errorf("Labels() returned label %q from the cold partition — hot-only expected", l)
		}
	}
	// "area:hot" must be present (from open issues).
	hasHot := false
	for _, l := range labels {
		if l == "area:hot" {
			hasHot = true
		}
	}
	if !hasHot {
		t.Errorf("Labels() missing expected hot-set label 'area:hot'; got: %v", labels)
	}
}

// TestReady_HotOnly verifies that Ready() only evaluates active (hot) issues.
// A closed issue that would otherwise satisfy "open with no open blockers" must
// not appear in the ready list (it is not open).
func TestReady_HotOnly(t *testing.T) {
	s, _, _, _, closedID := newScopedMemStore(t)

	ready, err := s.Ready()
	if err != nil {
		t.Fatalf("Ready(): %v", err)
	}
	for _, iss := range ready {
		if iss.ID == closedID {
			t.Errorf("Ready() returned closed issue %s — should be hot-only", closedID)
		}
	}
	if len(ready) != 2 {
		t.Errorf("Ready() = %d, want 2 (both open issues are ready)", len(ready))
	}
}
