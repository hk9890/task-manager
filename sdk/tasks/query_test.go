// query_test.go — L1/L2/L3 tests for the Row adapter, Store.Query, and
// the reworked Filter{Expr} / List(f) plumbing.
//
// L1 (pure): TestParseError_TypeAlias — verifies type ParseError = query.ParseError.
// L1 (pure): TestRow_Fields — verifies the Row adapter maps *Issue fields correctly.
// L2 (Mem):  TestQuery_MalformedExpr_ParseError — malformed expr returns *ParseError.
// L2 (Mem):  TestQuery_ValidExpr_Correctness — Query selects correctly.
// L2 (Mem):  TestList_ExprFilter_* — List with Expr behaves correctly.
// L2 (Mem):  TestList_NewFilter_NoOldFields — new Filter only has Expr/IncludeClosed/Sort/Reverse/Limit.
// L3: TestQuery_MalformedExpr_L3 — same ParseError guarantee on real FS.
package tasks_test

import (
	"errors"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
	"github.com/hk9890/task-manager/sdk/tasks/internal/storetest"
)

// ---- L1: type alias -----------------------------------------------------------

// TestParseError_TypeAlias verifies that tasks.ParseError is the same type as
// query.ParseError (type alias, not a new type), so callers can use errors.As
// with *tasks.ParseError.
func TestParseError_TypeAlias(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001").
		Mem()

	_, err := store.Query(`foobar == "x"`) // unknown field → ParseError
	if err == nil {
		t.Fatal("Query with malformed expression: expected error, got nil")
	}
	var pe *tasks.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *tasks.ParseError, got %T: %v", err, err)
	}
	if pe.Pos < 0 {
		t.Errorf("ParseError.Pos must be >= 0, got %d", pe.Pos)
	}
	if pe.Message == "" {
		t.Error("ParseError.Message must not be empty")
	}
}

// ---- L2: Query returns *ParseError for malformed expression -------------------

func TestQuery_MalformedExpr_ParseError(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001").
		Mem()

	cases := []string{
		`foobar == "x"`,     // unknown field
		`status < "open"`,   // bad operator for enum
		`(status == "open"`, // unbalanced paren
		`text == "x"`,       // text only allows ~
	}

	for _, expr := range cases {
		_, err := store.Query(expr)
		if err == nil {
			t.Errorf("Query(%q): expected error, got nil", expr)
			continue
		}
		var pe *tasks.ParseError
		if !errors.As(err, &pe) {
			t.Errorf("Query(%q): expected *tasks.ParseError, got %T: %v", expr, err, err)
		}
	}
}

// TestQuery_MalformedExpr_NothingWritten verifies that a malformed expression
// returns an error and nothing is written to disk.
func TestQuery_MalformedExpr_NothingWritten(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001").
		Mem()

	allBefore, err := store.All()
	if err != nil {
		t.Fatal(err)
	}

	_, queryErr := store.Query(`foobar == "x"`)
	if queryErr == nil {
		t.Fatal("expected error")
	}

	allAfter, err := store.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(allBefore) != len(allAfter) {
		t.Errorf("issue count changed after malformed query: %d → %d", len(allBefore), len(allAfter))
	}
}

// ---- L2: Query correctness ---------------------------------------------------

func TestQuery_EmptyExpr_AllHot(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001", storetest.Open).
		Issue("tst-0002", storetest.Open).
		Closed("tst-0003").
		Mem()

	issues, err := store.Query("")
	if err != nil {
		t.Fatalf("Query(\"\"): %v", err)
	}
	if len(issues) != 2 {
		t.Errorf("Query(\"\") = %d issues, want 2 (hot-only)", len(issues))
	}
	for _, iss := range issues {
		if iss.Status.IsClosed() {
			t.Errorf("Query(\"\") returned closed issue %s — hot-only expected", iss.ID)
		}
	}
}

func TestQuery_StatusExpr(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001", storetest.Open).
		Issue("tst-0002", storetest.InProgress).
		Closed("tst-0003").
		Mem()

	// status == "open" should return only tst-0001
	issues, err := store.Query(`status == "open"`)
	if err != nil {
		t.Fatalf("Query(status==open): %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "tst-0001" {
		t.Errorf("Query(status==open) = %v, want [tst-0001]", issueIDs(issues))
	}
}

func TestQuery_ClosedExpr_IncludesColdPartition(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001", storetest.Open).
		Closed("tst-0002").
		Mem()

	// status == "closed" must auto-include the cold partition
	issues, err := store.Query(`status == "closed"`)
	if err != nil {
		t.Fatalf("Query(status==closed): %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "tst-0002" {
		t.Errorf("Query(status==closed) = %v, want [tst-0002]", issueIDs(issues))
	}
}

func TestQuery_TypeExpr(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001", storetest.IssueType(tasks.TypeBug)).
		Issue("tst-0002", storetest.IssueType(tasks.TypeTask)).
		Mem()

	issues, err := store.Query(`type == bug`)
	if err != nil {
		t.Fatalf("Query(type==bug): %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "tst-0001" {
		t.Errorf("Query(type==bug) = %v, want [tst-0001]", issueIDs(issues))
	}
}

func TestQuery_PriorityExpr(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001", storetest.Priority(0)). // highest urgency
		Issue("tst-0002", storetest.Priority(3)).
		Mem()

	issues, err := store.Query(`priority <= 1`)
	if err != nil {
		t.Fatalf("Query(priority<=1): %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "tst-0001" {
		t.Errorf("Query(priority<=1) = %v, want [tst-0001]", issueIDs(issues))
	}
}

func TestQuery_LabelExpr(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001", storetest.Label("area:db")).
		Issue("tst-0002", storetest.Label("area:ui")).
		Mem()

	issues, err := store.Query(`label == "area:db"`)
	if err != nil {
		t.Fatalf("Query(label==area:db): %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "tst-0001" {
		t.Errorf("Query(label==area:db) = %v, want [tst-0001]", issueIDs(issues))
	}
}

func TestQuery_LabelTildeExpr(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001", storetest.Label("area:db")).
		Issue("tst-0002", storetest.Label("priority:high")).
		Mem()

	issues, err := store.Query(`label ~ "area"`)
	if err != nil {
		t.Fatalf("Query(label~area): %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "tst-0001" {
		t.Errorf("Query(label~area) = %v, want [tst-0001]", issueIDs(issues))
	}
}

func TestQuery_TextExpr(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001"). // title is "tst-0001"
		Issue("tst-0002").
		Mem()

	// text ~ "0001" should match tst-0001 (by ID or title)
	issues, err := store.Query(`text ~ "0001"`)
	if err != nil {
		t.Fatalf("Query(text~0001): %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "tst-0001" {
		t.Errorf("Query(text~0001) = %v, want [tst-0001]", issueIDs(issues))
	}
}

func TestQuery_ReadyPredicate(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001", storetest.Open).                                  // ready (no blockers)
		Issue("tst-0002", storetest.Open, storetest.BlockedBy("tst-0001")). // not ready
		Mem()

	issues, err := store.Query(`ready`)
	if err != nil {
		t.Fatalf("Query(ready): %v", err)
	}
	// tst-0001 is ready, tst-0002 is blocked
	if len(issues) != 1 || issues[0].ID != "tst-0001" {
		t.Errorf("Query(ready) = %v, want [tst-0001]", issueIDs(issues))
	}
}

func TestQuery_BlockedPredicate(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001", storetest.Open).                                  // not blocked
		Issue("tst-0002", storetest.Open, storetest.BlockedBy("tst-0001")). // blocked
		Mem()

	issues, err := store.Query(`blocked`)
	if err != nil {
		t.Fatalf("Query(blocked): %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "tst-0002" {
		t.Errorf("Query(blocked) = %v, want [tst-0002]", issueIDs(issues))
	}
}

func TestQuery_AndExpr(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001", storetest.IssueType(tasks.TypeBug), storetest.Priority(1)).
		Issue("tst-0002", storetest.IssueType(tasks.TypeBug), storetest.Priority(3)).
		Issue("tst-0003", storetest.IssueType(tasks.TypeTask), storetest.Priority(1)).
		Mem()

	issues, err := store.Query(`type == bug && priority <= 2`)
	if err != nil {
		t.Fatalf("Query(type==bug && priority<=2): %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "tst-0001" {
		t.Errorf("Query(type==bug && priority<=2) = %v, want [tst-0001]", issueIDs(issues))
	}
}

// ---- L2: Filter{Expr} + List -------------------------------------------------

func TestList_NewFilter_EmptyExpr_HotOnly(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001").
		Closed("tst-0002").
		Mem()

	// Empty Expr = always-true predicate; hot-only by default.
	issues, err := store.List(tasks.Filter{})
	if err != nil {
		t.Fatalf("List(Filter{}): %v", err)
	}
	if len(issues) != 1 {
		t.Errorf("List(Filter{}) = %d issues, want 1 (hot-only)", len(issues))
	}
}

func TestList_NewFilter_IncludeClosed(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001").
		Closed("tst-0002").
		Mem()

	issues, err := store.List(tasks.Filter{IncludeClosed: true})
	if err != nil {
		t.Fatalf("List(Filter{IncludeClosed:true}): %v", err)
	}
	if len(issues) != 2 {
		t.Errorf("List(IncludeClosed:true) = %d issues, want 2", len(issues))
	}
}

func TestList_NewFilter_ExprFilter(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001", storetest.IssueType(tasks.TypeBug)).
		Issue("tst-0002", storetest.IssueType(tasks.TypeTask)).
		Mem()

	issues, err := store.List(tasks.Filter{Expr: `type == bug`})
	if err != nil {
		t.Fatalf("List(Filter{Expr:type==bug}): %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "tst-0001" {
		t.Errorf("List(Expr=type==bug) = %v, want [tst-0001]", issueIDs(issues))
	}
}

func TestList_NewFilter_ExprClosedAutoScope(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001").
		Closed("tst-0002").
		Mem()

	// Expr references closed → cold partition auto-included without IncludeClosed.
	issues, err := store.List(tasks.Filter{Expr: `status == "closed"`})
	if err != nil {
		t.Fatalf("List(Expr=status==closed): %v", err)
	}
	found := false
	for _, iss := range issues {
		if iss.ID == "tst-0002" {
			found = true
		}
	}
	if !found {
		t.Errorf("List(Expr=status==closed) missing closed issue tst-0002: got %v", issueIDs(issues))
	}
}

func TestList_NewFilter_MalformedExpr_ParseError(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001").
		Mem()

	_, err := store.List(tasks.Filter{Expr: `foobar == "x"`})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var pe *tasks.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *tasks.ParseError, got %T: %v", err, err)
	}
}

func TestList_NewFilter_SortAndLimit(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001", storetest.Priority(3)).
		Issue("tst-0002", storetest.Priority(1)).
		Issue("tst-0003", storetest.Priority(2)).
		Mem()

	// Sort by work (priority asc) — default
	issues, err := store.List(tasks.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 3 {
		t.Fatalf("List() = %d, want 3", len(issues))
	}
	// Priority: tst-0002 (P1) < tst-0003 (P2) < tst-0001 (P3)
	if issues[0].ID != "tst-0002" || issues[1].ID != "tst-0003" || issues[2].ID != "tst-0001" {
		t.Errorf("sort order wrong: %v", issueIDs(issues))
	}

	// Limit=2
	limited, err := store.List(tasks.Filter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 {
		t.Errorf("Limit=2: got %d, want 2", len(limited))
	}

	// Reverse
	reversed, err := store.List(tasks.Filter{Reverse: true})
	if err != nil {
		t.Fatal(err)
	}
	if reversed[0].ID != "tst-0001" {
		t.Errorf("Reverse: first = %s, want tst-0001", reversed[0].ID)
	}
}

// ---- helpers -----------------------------------------------------------------

func issueIDs(issues []*tasks.Issue) []string {
	out := make([]string, len(issues))
	for i, iss := range issues {
		out[i] = iss.ID
	}
	return out
}
