// paging_test.go — L1/L2 tests for Filter.Offset, List paging, ListPage, and
// cross-partition read dedup (at-dny.1).
//
// L2 (Mem): all tests exercise Store methods on an in-memory FS.
// L2 (RawFixture): cross-partition dedup test uses raw bytes to simulate a
// concurrent close/reopen — the same issue ID present in both hot and closed/.
package tasks_test

import (
	"testing"

	"github.com/hk9890/agent-tasks/sdk/tasks"
	"github.com/hk9890/agent-tasks/sdk/tasks/internal/storetest"
)

// ---- helpers -----------------------------------------------------------------

// mustList calls store.List and fatals on error.
func mustList(t *testing.T, s *tasks.Store, f tasks.Filter) []*tasks.Issue {
	t.Helper()
	issues, err := s.List(f)
	if err != nil {
		t.Fatalf("List(%+v): %v", f, err)
	}
	return issues
}

// mustListPage calls store.ListPage and fatals on error.
func mustListPage(t *testing.T, s *tasks.Store, f tasks.Filter) tasks.Page {
	t.Helper()
	p, err := s.ListPage(f)
	if err != nil {
		t.Fatalf("ListPage(%+v): %v", f, err)
	}
	return p
}

// ---- Offset correctness -------------------------------------------------------

// TestList_Offset_SkipsCorrectly verifies that Offset skips the first N sorted
// matches and returns the remainder (up to Limit if set).
func TestList_Offset_SkipsCorrectly(t *testing.T) {
	// 4 issues with priorities 0,1,2,3 → sorted work order: tst-0001(P0),
	// tst-0002(P1), tst-0003(P2), tst-0004(P3).
	s := storetest.New(t).
		Issue("tst-0001", storetest.Priority(0)).
		Issue("tst-0002", storetest.Priority(1)).
		Issue("tst-0003", storetest.Priority(2)).
		Issue("tst-0004", storetest.Priority(3)).
		Mem()

	// Offset=2 → should return tst-0003, tst-0004.
	issues := mustList(t, s, tasks.Filter{Offset: 2})
	if len(issues) != 2 {
		t.Fatalf("Offset=2: got %d issues, want 2; ids=%v", len(issues), issueIDs(issues))
	}
	if issues[0].ID != "tst-0003" || issues[1].ID != "tst-0004" {
		t.Errorf("Offset=2: got %v, want [tst-0003 tst-0004]", issueIDs(issues))
	}
}

// TestList_Offset_WithLimit verifies that Offset is applied before Limit.
func TestList_Offset_WithLimit(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Priority(0)).
		Issue("tst-0002", storetest.Priority(1)).
		Issue("tst-0003", storetest.Priority(2)).
		Issue("tst-0004", storetest.Priority(3)).
		Mem()

	// Offset=1, Limit=2 → skip tst-0001, take tst-0002 and tst-0003.
	issues := mustList(t, s, tasks.Filter{Offset: 1, Limit: 2})
	if len(issues) != 2 {
		t.Fatalf("Offset=1,Limit=2: got %d, want 2; ids=%v", len(issues), issueIDs(issues))
	}
	if issues[0].ID != "tst-0002" || issues[1].ID != "tst-0003" {
		t.Errorf("Offset=1,Limit=2: got %v, want [tst-0002 tst-0003]", issueIDs(issues))
	}
}

// TestList_Offset_GTE_Total_EmptyResult verifies that when Offset >= total
// matches, List returns nil/empty (not an error).
func TestList_Offset_GTE_Total_EmptyResult(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001").
		Issue("tst-0002").
		Mem()

	// Offset=2 equals total count → empty.
	issues := mustList(t, s, tasks.Filter{Offset: 2})
	if len(issues) != 0 {
		t.Errorf("Offset=2 (== total): got %d issues, want 0; ids=%v", len(issues), issueIDs(issues))
	}

	// Offset=100 far beyond total → empty.
	issues = mustList(t, s, tasks.Filter{Offset: 100})
	if len(issues) != 0 {
		t.Errorf("Offset=100: got %d issues, want 0", len(issues))
	}
}

// ---- Negative offset/limit clamp to 0 ----------------------------------------

// TestList_NegativeOffset_ClampsToZero verifies that a negative Offset is
// treated as 0 (no skip).
func TestList_NegativeOffset_ClampsToZero(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Priority(0)).
		Issue("tst-0002", storetest.Priority(1)).
		Mem()

	issues := mustList(t, s, tasks.Filter{Offset: -5})
	if len(issues) != 2 {
		t.Errorf("Offset=-5: got %d issues, want 2 (clamped to 0)", len(issues))
	}
}

// TestList_NegativeLimit_ClampsToZero verifies that a negative Limit is treated
// as 0 (no limit — all remaining matches returned).
func TestList_NegativeLimit_ClampsToZero(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Priority(0)).
		Issue("tst-0002", storetest.Priority(1)).
		Issue("tst-0003", storetest.Priority(2)).
		Mem()

	issues := mustList(t, s, tasks.Filter{Limit: -3})
	if len(issues) != 3 {
		t.Errorf("Limit=-3: got %d issues, want 3 (clamped to 0 = no limit)", len(issues))
	}
}

// TestList_BothNegative_ClampsToZero verifies that both negative Offset and
// negative Limit clamp to 0.
func TestList_BothNegative_ClampsToZero(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001").
		Issue("tst-0002").
		Mem()

	issues := mustList(t, s, tasks.Filter{Offset: -1, Limit: -1})
	if len(issues) != 2 {
		t.Errorf("Offset=-1,Limit=-1: got %d issues, want 2", len(issues))
	}
}

// ---- Limit==0 with Offset returns all from Offset onward ----------------------

// TestList_LimitZero_ReturnsAllFromOffset verifies that Limit==0 means "no
// limit" and returns all matches from Offset onward.
func TestList_LimitZero_ReturnsAllFromOffset(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Priority(0)).
		Issue("tst-0002", storetest.Priority(1)).
		Issue("tst-0003", storetest.Priority(2)).
		Issue("tst-0004", storetest.Priority(3)).
		Mem()

	// Offset=1, Limit=0 → all from position 1 onward: tst-0002, tst-0003, tst-0004.
	issues := mustList(t, s, tasks.Filter{Offset: 1, Limit: 0})
	if len(issues) != 3 {
		t.Fatalf("Offset=1,Limit=0: got %d issues, want 3; ids=%v", len(issues), issueIDs(issues))
	}
	if issues[0].ID != "tst-0002" {
		t.Errorf("Offset=1,Limit=0: first = %s, want tst-0002", issues[0].ID)
	}
}

// ---- Existing callers identical at Offset==0 ----------------------------------

// TestList_Offset0_IdenticalToNoOffset verifies that Offset==0 produces the
// same result as not setting Offset at all (the zero value).
func TestList_Offset0_IdenticalToNoOffset(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Priority(0)).
		Issue("tst-0002", storetest.Priority(1)).
		Issue("tst-0003", storetest.Priority(2)).
		Mem()

	withoutOffset := mustList(t, s, tasks.Filter{})
	withOffset0 := mustList(t, s, tasks.Filter{Offset: 0})

	if len(withoutOffset) != len(withOffset0) {
		t.Fatalf("Offset=0: len %d != len %d", len(withoutOffset), len(withOffset0))
	}
	for i := range withoutOffset {
		if withoutOffset[i].ID != withOffset0[i].ID {
			t.Errorf("Offset=0: position %d: %s != %s", i, withoutOffset[i].ID, withOffset0[i].ID)
		}
	}
}

// ---- ListPage: Total + window from one snapshot ------------------------------

// TestListPage_ReturnsTotal verifies that ListPage returns the total count of
// all matches before Offset/Limit, alongside the correct window.
func TestListPage_ReturnsTotal(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Priority(0)).
		Issue("tst-0002", storetest.Priority(1)).
		Issue("tst-0003", storetest.Priority(2)).
		Issue("tst-0004", storetest.Priority(3)).
		Mem()

	// Page 1: Offset=0, Limit=2 → Issues=[tst-0001, tst-0002], Total=4.
	p := mustListPage(t, s, tasks.Filter{Offset: 0, Limit: 2})
	if p.Total != 4 {
		t.Errorf("ListPage page1: Total=%d, want 4", p.Total)
	}
	if len(p.Issues) != 2 {
		t.Fatalf("ListPage page1: len(Issues)=%d, want 2; ids=%v", len(p.Issues), issueIDs(p.Issues))
	}
	if p.Issues[0].ID != "tst-0001" || p.Issues[1].ID != "tst-0002" {
		t.Errorf("ListPage page1: Issues=%v, want [tst-0001 tst-0002]", issueIDs(p.Issues))
	}

	// Page 2: Offset=2, Limit=2 → Issues=[tst-0003, tst-0004], Total=4.
	p = mustListPage(t, s, tasks.Filter{Offset: 2, Limit: 2})
	if p.Total != 4 {
		t.Errorf("ListPage page2: Total=%d, want 4", p.Total)
	}
	if len(p.Issues) != 2 {
		t.Fatalf("ListPage page2: len(Issues)=%d, want 2; ids=%v", len(p.Issues), issueIDs(p.Issues))
	}
	if p.Issues[0].ID != "tst-0003" || p.Issues[1].ID != "tst-0004" {
		t.Errorf("ListPage page2: Issues=%v, want [tst-0003 tst-0004]", issueIDs(p.Issues))
	}
}

// TestListPage_OffsetGTE_Total_EmptyIssues verifies that when Offset >= Total,
// ListPage returns an empty (nil) Issues slice and the correct Total.
func TestListPage_OffsetGTE_Total_EmptyIssues(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001").
		Issue("tst-0002").
		Mem()

	// Offset=2 equals total=2 → empty Issues, Total=2.
	p := mustListPage(t, s, tasks.Filter{Offset: 2, Limit: 10})
	if p.Total != 2 {
		t.Errorf("ListPage Offset>=Total: Total=%d, want 2", p.Total)
	}
	if len(p.Issues) != 0 {
		t.Errorf("ListPage Offset>=Total: len(Issues)=%d, want 0; ids=%v", len(p.Issues), issueIDs(p.Issues))
	}
}

// TestListPage_NegativeClamp verifies that negative Offset/Limit clamp to 0 in
// ListPage, and Total is still accurate.
func TestListPage_NegativeClamp(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Priority(0)).
		Issue("tst-0002", storetest.Priority(1)).
		Issue("tst-0003", storetest.Priority(2)).
		Mem()

	// Negative Offset clamps to 0: all 3 issues returned.
	p := mustListPage(t, s, tasks.Filter{Offset: -1, Limit: 10})
	if p.Total != 3 {
		t.Errorf("ListPage Offset<0: Total=%d, want 3", p.Total)
	}
	if len(p.Issues) != 3 {
		t.Errorf("ListPage Offset<0: len(Issues)=%d, want 3", len(p.Issues))
	}

	// Negative Limit clamps to 0 (no limit): all 3 from offset 1 returned.
	p = mustListPage(t, s, tasks.Filter{Offset: 1, Limit: -1})
	if p.Total != 3 {
		t.Errorf("ListPage Limit<0: Total=%d, want 3", p.Total)
	}
	if len(p.Issues) != 2 {
		t.Errorf("ListPage Limit<0: len(Issues)=%d, want 2 (from offset 1)", len(p.Issues))
	}
}

// TestListPage_LimitZero_AllFromOffset verifies that Limit==0 in ListPage
// returns all matches from Offset onward, with correct Total.
func TestListPage_LimitZero_AllFromOffset(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Priority(0)).
		Issue("tst-0002", storetest.Priority(1)).
		Issue("tst-0003", storetest.Priority(2)).
		Mem()

	p := mustListPage(t, s, tasks.Filter{Offset: 1, Limit: 0})
	if p.Total != 3 {
		t.Errorf("ListPage Limit=0: Total=%d, want 3", p.Total)
	}
	if len(p.Issues) != 2 {
		t.Fatalf("ListPage Limit=0: len(Issues)=%d, want 2; ids=%v", len(p.Issues), issueIDs(p.Issues))
	}
	if p.Issues[0].ID != "tst-0002" {
		t.Errorf("ListPage Limit=0: first=%s, want tst-0002", p.Issues[0].ID)
	}
}

// ---- Cross-partition read dedup -----------------------------------------------

// TestList_CrossPartitionDedup_NoDuplicateID verifies that a scan spanning both
// hot and closed/ partitions never returns the same issue ID twice, even when
// that ID appears in both partitions simultaneously (simulating a concurrent
// close/reopen in-flight). This is achieved using a RawFixture to write the
// same issue file into both partitions.
func TestList_CrossPartitionDedup_NoDuplicateID(t *testing.T) {
	// Build the raw bytes for an issue that appears in both the hot set and closed/.
	// We use two different IDs (tst-0001 hot-only, tst-0002 in BOTH partitions).
	dir := t.TempDir()
	rf := storetest.NewRawFixture(t, dir)

	// A normal hot issue (only in hot set).
	hotOnly := []byte("---\nid: tst-0001\ntitle: hot only\nstatus: open\ntype: task\npriority: 2\n---\n")
	rf.WriteIssue("tst-0001.md", hotOnly)

	// A "ghost" issue appearing in BOTH hot and closed/ simultaneously —
	// simulates a concurrent close that wrote to closed/ but hasn't removed the
	// hot file yet, or a concurrent reopen that put it back in hot but the
	// closed/ file wasn't removed yet.
	ghost := []byte("---\nid: tst-0002\ntitle: ghost\nstatus: open\ntype: task\npriority: 2\n---\n")
	rf.WriteIssue("tst-0002.md", ghost)
	ghostClosed := []byte("---\nid: tst-0002\ntitle: ghost\nstatus: closed\ntype: task\npriority: 2\n---\n")
	rf.WriteIssue("closed/tst-0002.md", ghostClosed)

	// A normal closed issue (only in closed/).
	closedOnly := []byte("---\nid: tst-0003\ntitle: closed only\nstatus: closed\ntype: task\npriority: 2\n---\n")
	rf.WriteIssue("closed/tst-0003.md", closedOnly)

	s, err := tasks.Open(rf.Dir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Scan both partitions — IncludeClosed triggers the cross-partition merge.
	issues, err := s.List(tasks.Filter{IncludeClosed: true})
	if err != nil {
		t.Fatalf("List(IncludeClosed=true): %v", err)
	}

	// Verify no duplicate IDs.
	seen := make(map[string]int)
	for _, iss := range issues {
		seen[iss.ID]++
	}
	for id, count := range seen {
		if count > 1 {
			t.Errorf("cross-partition dedup: ID %q appears %d times, want exactly 1", id, count)
		}
	}

	// All three logical issues should be present exactly once.
	for _, id := range []string{"tst-0001", "tst-0002", "tst-0003"} {
		if seen[id] != 1 {
			t.Errorf("cross-partition dedup: ID %q count=%d, want 1; full set=%v", id, seen[id], issueIDs(issues))
		}
	}
}

// TestListPage_CrossPartitionDedup_NoDuplicateID verifies the same dedup
// guarantee through ListPage.
func TestListPage_CrossPartitionDedup_NoDuplicateID(t *testing.T) {
	dir := t.TempDir()
	rf := storetest.NewRawFixture(t, dir)

	// Ghost issue in both partitions.
	ghost := []byte("---\nid: tst-0001\ntitle: ghost\nstatus: open\ntype: task\npriority: 2\n---\n")
	rf.WriteIssue("tst-0001.md", ghost)
	ghostClosed := []byte("---\nid: tst-0001\ntitle: ghost\nstatus: closed\ntype: task\npriority: 2\n---\n")
	rf.WriteIssue("closed/tst-0001.md", ghostClosed)

	// Normal closed issue.
	closed := []byte("---\nid: tst-0002\ntitle: done\nstatus: closed\ntype: task\npriority: 2\n---\n")
	rf.WriteIssue("closed/tst-0002.md", closed)

	s, err := tasks.Open(rf.Dir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	p, err := s.ListPage(tasks.Filter{IncludeClosed: true})
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}

	seen := make(map[string]int)
	for _, iss := range p.Issues {
		seen[iss.ID]++
	}
	for id, count := range seen {
		if count > 1 {
			t.Errorf("ListPage cross-partition dedup: ID %q appears %d times, want 1", id, count)
		}
	}

	// Total must equal the number of unique logical issues (2), not the raw file count (3).
	if p.Total != 2 {
		t.Errorf("ListPage cross-partition dedup: Total=%d, want 2 (deduplicated)", p.Total)
	}
}

// ---- ParseError still propagates through ListPage ----------------------------

// TestListPage_MalformedExpr_ParseError verifies that a malformed Expr returns
// a *tasks.ParseError from ListPage and no scan runs.
func TestListPage_MalformedExpr_ParseError(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001").
		Mem()

	_, err := s.ListPage(tasks.Filter{Expr: `foobar == "x"`})
	if err == nil {
		t.Fatal("expected *ParseError, got nil")
	}
	var pe *tasks.ParseError
	if !isParseError(err, &pe) {
		t.Fatalf("expected *tasks.ParseError, got %T: %v", err, err)
	}
}

// isParseError is a helper to avoid importing errors in the test (errors.As
// is available; we just want a clean type check).
func isParseError(err error, pe **tasks.ParseError) bool {
	if err == nil {
		return false
	}
	if x, ok := err.(*tasks.ParseError); ok {
		*pe = x
		return true
	}
	return false
}
