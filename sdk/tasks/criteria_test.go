// criteria_test.go — Tests for Criteria.Build, Find, FindPage, and FindOptions.
//
// SDK-SPEC §3 (Criteria/Build), §4 (Find/FindPage/FindOptions).
// QUERY-SPEC §5/§7.
//
// L1 (pure): Build() table tests — each field, combinations, escaping, empty.
// L2 (Mem): Find/FindPage against storetest fixture; cold-scope parity.
package tasks_test

import (
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks"
	"github.com/hk9890/task-manager/sdk/tasks/internal/storetest"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func ptr[T any](v T) *T { return &v }

// sortIssueIDs returns the IDs of the given issues, sorted.
func sortIssueIDs(issues []*tasks.Issue) []string {
	ids := make([]string, len(issues))
	for i, iss := range issues {
		ids[i] = iss.ID
	}
	sort.Strings(ids)
	return ids
}

// ── L1: Build() table tests ───────────────────────────────────────────────────

func TestCriteria_Build_Empty(t *testing.T) {
	expr, err := tasks.Criteria{}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if expr != "" {
		t.Errorf("empty Criteria: got %q, want %q", expr, "")
	}
}

func TestCriteria_Build_Text(t *testing.T) {
	expr, err := tasks.Criteria{Text: "hello"}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `text ~ "hello"`
	if expr != want {
		t.Errorf("Text field: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_TextPhrase_IsDefaultZeroValue(t *testing.T) {
	// TextMatch zero value must be TextPhrase so existing Text callers are
	// unaffected: a multi-word Text stays one contiguous substring.
	var tm tasks.TextMatch
	if tm != tasks.TextPhrase {
		t.Errorf("TextMatch zero value = %d, want TextPhrase = %d", tm, tasks.TextPhrase)
	}
	expr, err := tasks.Criteria{Text: "drill nav"}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `text ~ "drill nav"`
	if expr != want {
		t.Errorf("default (phrase) multi-word: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_TextAllWords_MultipleWords_ANDGroup(t *testing.T) {
	expr, err := tasks.Criteria{Text: "drill nav", TextMatch: tasks.TextAllWords}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	// Each word is a separate AND clause; order-independent at match time.
	want := `text ~ "drill" && text ~ "nav"`
	if expr != want {
		t.Errorf("TextAllWords multi-word: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_TextAllWords_SingleWord(t *testing.T) {
	// A single word is identical to phrase mode, so `search foo` == `text ~ "foo"`.
	expr, err := tasks.Criteria{Text: "drill", TextMatch: tasks.TextAllWords}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `text ~ "drill"`
	if expr != want {
		t.Errorf("TextAllWords single word: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_TextAllWords_CollapsesWhitespace(t *testing.T) {
	// Runs of whitespace (incl. tabs) collapse to single word boundaries.
	expr, err := tasks.Criteria{Text: "  drill\t nav  ", TextMatch: tasks.TextAllWords}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `text ~ "drill" && text ~ "nav"`
	if expr != want {
		t.Errorf("TextAllWords whitespace: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_TextAllWords_Empty(t *testing.T) {
	// Empty / whitespace-only Text yields no constraint (the zero expression).
	for _, in := range []string{"", "   ", "\t\n"} {
		expr, err := tasks.Criteria{Text: in, TextMatch: tasks.TextAllWords}.Build()
		if err != nil {
			t.Fatalf("Build(%q) error: %v", in, err)
		}
		if expr != "" {
			t.Errorf("TextAllWords %q: got %q, want %q", in, expr, "")
		}
	}
}

func TestCriteria_Build_TextAllWords_ComposesWithFacets(t *testing.T) {
	// The per-word text fragments drop into the existing top-level && chain and
	// compose cleanly with a self-parenthesizing status OR-group — no precedence
	// trap, because text never emits || internally.
	expr, err := tasks.Criteria{
		Text:      "drill nav",
		TextMatch: tasks.TextAllWords,
		Statuses:  []tasks.Status{tasks.StatusOpen, tasks.StatusInProgress},
	}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `text ~ "drill" && text ~ "nav" && (status == "open" || status == "in_progress")`
	if expr != want {
		t.Errorf("text+facets: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_SingleStatus(t *testing.T) {
	expr, err := tasks.Criteria{Statuses: []tasks.Status{tasks.StatusOpen}}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `status == "open"`
	if expr != want {
		t.Errorf("single status: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_MultipleStatuses_ORGroup(t *testing.T) {
	expr, err := tasks.Criteria{
		Statuses: []tasks.Status{tasks.StatusOpen, tasks.StatusInProgress},
	}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `(status == "open" || status == "in_progress")`
	if expr != want {
		t.Errorf("multi-status: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_SingleType(t *testing.T) {
	expr, err := tasks.Criteria{Types: []tasks.Type{tasks.TypeBug}}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `type == "bug"`
	if expr != want {
		t.Errorf("single type: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_MultipleTypes_ORGroup(t *testing.T) {
	expr, err := tasks.Criteria{
		Types: []tasks.Type{tasks.TypeBug, tasks.TypeFeature},
	}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `(type == "bug" || type == "feature")`
	if expr != want {
		t.Errorf("multi-type: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_LabelMatchAll_SingleLabel(t *testing.T) {
	expr, err := tasks.Criteria{
		Labels:     []string{"area:db"},
		LabelMatch: tasks.LabelMatchAll,
	}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `label == "area:db"`
	if expr != want {
		t.Errorf("single label (LabelMatchAll): got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_LabelMatchAll_MultipleLabels_ANDGroup(t *testing.T) {
	expr, err := tasks.Criteria{
		Labels:     []string{"area:db", "prio:high"},
		LabelMatch: tasks.LabelMatchAll,
	}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	// Both labels must be present; each is a separate AND clause.
	want := `label == "area:db" && label == "prio:high"`
	if expr != want {
		t.Errorf("multi-label (LabelMatchAll): got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_LabelMatchAny_SingleLabel(t *testing.T) {
	expr, err := tasks.Criteria{
		Labels:     []string{"area:db"},
		LabelMatch: tasks.LabelMatchAny,
	}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `label == "area:db"`
	if expr != want {
		t.Errorf("single label (LabelMatchAny): got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_LabelMatchAny_MultipleLabels_ORGroup(t *testing.T) {
	expr, err := tasks.Criteria{
		Labels:     []string{"area:db", "prio:high"},
		LabelMatch: tasks.LabelMatchAny,
	}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `(label == "area:db" || label == "prio:high")`
	if expr != want {
		t.Errorf("multi-label (LabelMatchAny): got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_LabelMatchAll_IsDefaultZeroValue(t *testing.T) {
	// LabelMatch zero value must be LabelMatchAll.
	var lm tasks.LabelMatch
	if lm != tasks.LabelMatchAll {
		t.Errorf("LabelMatch zero value = %d, want LabelMatchAll = %d", lm, tasks.LabelMatchAll)
	}
}

func TestCriteria_Build_Assignee(t *testing.T) {
	expr, err := tasks.Criteria{Assignee: "alice"}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `assignee == "alice"`
	if expr != want {
		t.Errorf("assignee: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_Creator(t *testing.T) {
	expr, err := tasks.Criteria{Creator: "bob"}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `creator == "bob"`
	if expr != want {
		t.Errorf("creator: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_Parent_NonEmptyID(t *testing.T) {
	expr, err := tasks.Criteria{Parent: ptr("tst-0007")}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `parent == "tst-0007"`
	if expr != want {
		t.Errorf("parent non-empty: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_Parent_EmptyString_MeansNoParent(t *testing.T) {
	// A non-nil pointer to "" means "no parent" (parent == "").
	expr, err := tasks.Criteria{Parent: ptr("")}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `parent == ""`
	if expr != want {
		t.Errorf("parent == empty: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_Parent_Nil_NoConstraint(t *testing.T) {
	expr, err := tasks.Criteria{Parent: nil}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if expr != "" {
		t.Errorf("nil parent: got %q, want empty", expr)
	}
}

func TestCriteria_Build_WorkReady(t *testing.T) {
	expr, err := tasks.Criteria{Work: tasks.WorkReady}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := "ready"
	if expr != want {
		t.Errorf("WorkReady: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_WorkBlocked(t *testing.T) {
	expr, err := tasks.Criteria{Work: tasks.WorkBlocked}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := "blocked"
	if expr != want {
		t.Errorf("WorkBlocked: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_PriorityMin(t *testing.T) {
	expr, err := tasks.Criteria{PriorityMin: ptr(1)}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := "priority >= 1"
	if expr != want {
		t.Errorf("PriorityMin: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_PriorityMax(t *testing.T) {
	expr, err := tasks.Criteria{PriorityMax: ptr(3)}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := "priority <= 3"
	if expr != want {
		t.Errorf("PriorityMax: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_PriorityBothBounds(t *testing.T) {
	expr, err := tasks.Criteria{PriorityMin: ptr(1), PriorityMax: ptr(3)}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := "priority >= 1 && priority <= 3"
	if expr != want {
		t.Errorf("priority both bounds: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_PriorityMin_AboveStorableRange_EmittedAsIs(t *testing.T) {
	// A positive bound above the storable range (>= 5) is emitted AS-IS — no error.
	expr, err := tasks.Criteria{PriorityMin: ptr(5)}.Build()
	if err != nil {
		t.Fatalf("out-of-range positive bound should not be an error, got: %v", err)
	}
	want := "priority >= 5"
	if expr != want {
		t.Errorf("PriorityMin=5 (out of range): got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_PriorityMin_Negative_ValidationError(t *testing.T) {
	_, err := tasks.Criteria{PriorityMin: ptr(-1)}.Build()
	if err == nil {
		t.Fatal("negative PriorityMin: expected *ValidationError, got nil")
	}
	var ve *tasks.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *tasks.ValidationError, got %T: %v", err, err)
	}
	if ve.Field != "priority_min" {
		t.Errorf("ValidationError.Field = %q, want %q", ve.Field, "priority_min")
	}
}

func TestCriteria_Build_PriorityMax_Negative_ValidationError(t *testing.T) {
	_, err := tasks.Criteria{PriorityMax: ptr(-2)}.Build()
	if err == nil {
		t.Fatal("negative PriorityMax: expected *ValidationError, got nil")
	}
	var ve *tasks.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *tasks.ValidationError, got %T: %v", err, err)
	}
	if ve.Field != "priority_max" {
		t.Errorf("ValidationError.Field = %q, want %q", ve.Field, "priority_max")
	}
}

// ── Date bounds (half-open) ───────────────────────────────────────────────────

func TestCriteria_Build_CreatedFrom_GTE_Inclusive(t *testing.T) {
	ts := time.Date(2026, 1, 15, 12, 30, 0, 0, time.UTC)
	expr, err := tasks.Criteria{CreatedFrom: &ts}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `created >= "2026-01-15T12:30:00Z"`
	if expr != want {
		t.Errorf("CreatedFrom: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_CreatedTo_LT_Exclusive(t *testing.T) {
	ts := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)
	expr, err := tasks.Criteria{CreatedTo: &ts}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `created < "2026-01-16T00:00:00Z"`
	if expr != want {
		t.Errorf("CreatedTo: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_UpdatedBounds(t *testing.T) {
	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	expr, err := tasks.Criteria{UpdatedFrom: &from, UpdatedTo: &to}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `updated >= "2026-02-01T00:00:00Z" && updated < "2026-03-01T00:00:00Z"`
	if expr != want {
		t.Errorf("UpdatedFrom+To: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_ClosedBounds(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	expr, err := tasks.Criteria{ClosedFrom: &from, ClosedTo: &to}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `closed >= "2026-01-01T00:00:00Z" && closed < "2026-02-01T00:00:00Z"`
	if expr != want {
		t.Errorf("ClosedFrom+To: got %q, want %q", expr, want)
	}
}

// ── Escaping edges ────────────────────────────────────────────────────────────

func TestCriteria_Build_Escaping_DoubleQuoteInString(t *testing.T) {
	// Assignee name containing a double quote must be escaped.
	expr, err := tasks.Criteria{Assignee: `alice"dev`}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `assignee == "alice\"dev"`
	if expr != want {
		t.Errorf("assignee with quote: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_Escaping_BackslashInString(t *testing.T) {
	expr, err := tasks.Criteria{Assignee: `alice\bob`}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `assignee == "alice\\bob"`
	if expr != want {
		t.Errorf("assignee with backslash: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_Escaping_SpacesInText(t *testing.T) {
	// Text with spaces is always quoted (spaces are not bareword characters).
	expr, err := tasks.Criteria{Text: "drill nav"}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `text ~ "drill nav"`
	if expr != want {
		t.Errorf("text with spaces: got %q, want %q", expr, want)
	}
}

func TestCriteria_Build_Escaping_BothQuoteAndBackslash(t *testing.T) {
	// A string with both backslash and double-quote.
	expr, err := tasks.Criteria{Creator: `a\b"c`}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	// Backslash is escaped first: a\b"c → a\\b"c → a\\b\"c
	want := `creator == "a\\b\"c"`
	if expr != want {
		t.Errorf("creator with backslash+quote: got %q, want %q", expr, want)
	}
}

// ── Unknown enum values → ValidationError ─────────────────────────────────────

func TestCriteria_Build_UnknownStatus_ValidationError(t *testing.T) {
	_, err := tasks.Criteria{
		Statuses: []tasks.Status{"bogus"},
	}.Build()
	if err == nil {
		t.Fatal("unknown Status: expected *ValidationError, got nil")
	}
	var ve *tasks.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *tasks.ValidationError, got %T: %v", err, err)
	}
	if ve.Field != "statuses" {
		t.Errorf("ValidationError.Field = %q, want %q", ve.Field, "statuses")
	}
}

func TestCriteria_Build_UnknownType_ValidationError(t *testing.T) {
	_, err := tasks.Criteria{
		Types: []tasks.Type{"unknown"},
	}.Build()
	if err == nil {
		t.Fatal("unknown Type: expected *ValidationError, got nil")
	}
	var ve *tasks.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *tasks.ValidationError, got %T: %v", err, err)
	}
	if ve.Field != "types" {
		t.Errorf("ValidationError.Field = %q, want %q", ve.Field, "types")
	}
}

// ── Combination expression ─────────────────────────────────────────────────────

func TestCriteria_Build_Combination(t *testing.T) {
	expr, err := tasks.Criteria{
		Statuses:    []tasks.Status{tasks.StatusOpen},
		Types:       []tasks.Type{tasks.TypeBug},
		Assignee:    "alice",
		PriorityMax: ptr(2),
	}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	want := `status == "open" && type == "bug" && assignee == "alice" && priority <= 2`
	if expr != want {
		t.Errorf("combination: got %q, want %q", expr, want)
	}
}

// ── Round-trip: valid Build() output always parses ────────────────────────────

func TestCriteria_Build_RoundTrip_ValidInputAlwaysParses(t *testing.T) {
	// Create a store with a real issue so Query doesn't fail with "no store".
	// We just need to compile the expressions — not actually match anything.
	s := storetest.New(t).Issue("tst-0001").Mem()

	ts := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	cases := []tasks.Criteria{
		{},
		{Text: "hello world"},
		{Statuses: []tasks.Status{tasks.StatusOpen}},
		{Statuses: []tasks.Status{tasks.StatusOpen, tasks.StatusInProgress}},
		{Types: []tasks.Type{tasks.TypeBug}},
		{Types: []tasks.Type{tasks.TypeBug, tasks.TypeChore}},
		{Labels: []string{"area:db"}, LabelMatch: tasks.LabelMatchAll},
		{Labels: []string{"area:db", "prio:high"}, LabelMatch: tasks.LabelMatchAll},
		{Labels: []string{"area:db", "prio:high"}, LabelMatch: tasks.LabelMatchAny},
		{Assignee: "alice"},
		{Creator: "bob"},
		{Parent: ptr("tst-0007")},
		{Parent: ptr("")},
		{Work: tasks.WorkReady},
		{Work: tasks.WorkBlocked},
		{PriorityMin: ptr(1)},
		{PriorityMax: ptr(3)},
		{PriorityMin: ptr(0), PriorityMax: ptr(4)},
		{PriorityMin: ptr(5)}, // above storable range, harmless
		{CreatedFrom: &ts},
		{CreatedTo: &ts},
		{UpdatedFrom: &ts, UpdatedTo: &ts},
		{ClosedFrom: &ts},
		// Complex combination
		{
			Statuses:    []tasks.Status{tasks.StatusOpen},
			Types:       []tasks.Type{tasks.TypeBug},
			Labels:      []string{"area:db"},
			Assignee:    "alice",
			PriorityMax: ptr(2),
			CreatedFrom: &ts,
		},
		// Escaping
		{Assignee: `alice"bob`},
		{Assignee: `alice\bob`},
		{Creator: `a\b"c`},
		{Text: "multi word text"},
	}

	for i, c := range cases {
		expr, err := c.Build()
		if err != nil {
			t.Errorf("case %d: Build() returned unexpected error: %v", i, err)
			continue
		}
		// Round-trip: Query the store with the built expression — must not be *ParseError.
		_, queryErr := s.Query(expr)
		if queryErr != nil {
			var pe *tasks.ParseError
			if errors.As(queryErr, &pe) {
				t.Errorf("case %d: Build() output %q failed to parse: %v", i, expr, queryErr)
			}
			// A non-ParseError (e.g. ErrNoStore) would be a real failure too.
			// But since we have a valid store, any error is unexpected.
			t.Errorf("case %d: Query(%q) returned unexpected error: %v", i, expr, queryErr)
		}
	}
}

// ── L2: Find/FindPage against storetest fixture ──────────────────────────────

// TestFind_ByStatus returns only issues matching the requested status.
func TestFind_ByStatus(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Open).
		Issue("tst-0002", storetest.InProgress).
		Issue("tst-0003", storetest.Open).
		Mem()

	issues, err := s.Find(tasks.Criteria{Statuses: []tasks.Status{tasks.StatusOpen}}, tasks.FindOptions{})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	ids := sortIssueIDs(issues)
	if len(ids) != 2 {
		t.Fatalf("Find open: got %v, want 2 issues", ids)
	}
	if ids[0] != "tst-0001" || ids[1] != "tst-0003" {
		t.Errorf("Find open: got %v, want [tst-0001, tst-0003]", ids)
	}
}

// TestFind_ByCreator selects only issues with the matching creator.
func TestFind_ByCreator(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Creator("alice")).
		Issue("tst-0002", storetest.Creator("bob")).
		Issue("tst-0003", storetest.Creator("alice")).
		Mem()

	issues, err := s.Find(tasks.Criteria{Creator: "alice"}, tasks.FindOptions{})
	if err != nil {
		t.Fatalf("Find by creator: %v", err)
	}
	ids := sortIssueIDs(issues)
	if len(ids) != 2 {
		t.Fatalf("Find creator=alice: got %v, want 2 issues", ids)
	}
	if ids[0] != "tst-0001" || ids[1] != "tst-0003" {
		t.Errorf("Find creator=alice: got %v, want [tst-0001, tst-0003]", ids)
	}
}

// TestFind_ByAssignee selects only issues with the matching assignee.
func TestFind_ByAssignee(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Assignee("alice")).
		Issue("tst-0002", storetest.Assignee("bob")).
		Mem()

	issues, err := s.Find(tasks.Criteria{Assignee: "alice"}, tasks.FindOptions{})
	if err != nil {
		t.Fatalf("Find by assignee: %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "tst-0001" {
		t.Errorf("Find assignee=alice: got %v, want [tst-0001]", sortIssueIDs(issues))
	}
}

// TestFind_ByType selects only issues with the matching type.
func TestFind_ByType(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.IssueType(tasks.TypeBug)).
		Issue("tst-0002", storetest.IssueType(tasks.TypeTask)).
		Issue("tst-0003", storetest.IssueType(tasks.TypeBug)).
		Mem()

	issues, err := s.Find(tasks.Criteria{Types: []tasks.Type{tasks.TypeBug}}, tasks.FindOptions{})
	if err != nil {
		t.Fatalf("Find by type: %v", err)
	}
	ids := sortIssueIDs(issues)
	if len(ids) != 2 {
		t.Fatalf("Find type=bug: got %v, want 2 issues", ids)
	}
	if ids[0] != "tst-0001" || ids[1] != "tst-0003" {
		t.Errorf("Find type=bug: got %v, want [tst-0001, tst-0003]", ids)
	}
}

// TestFind_ByLabel selects issues with the given label (LabelMatchAll default).
func TestFind_ByLabel(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Label("area:db")).
		Issue("tst-0002", storetest.Label("area:ui")).
		Issue("tst-0003", storetest.Label("area:db")).
		Mem()

	issues, err := s.Find(tasks.Criteria{Labels: []string{"area:db"}}, tasks.FindOptions{})
	if err != nil {
		t.Fatalf("Find by label: %v", err)
	}
	ids := sortIssueIDs(issues)
	if len(ids) != 2 || ids[0] != "tst-0001" || ids[1] != "tst-0003" {
		t.Errorf("Find label=area:db: got %v, want [tst-0001, tst-0003]", ids)
	}
}

// TestFind_EmptyCriteria_ReturnsAll returns all hot issues for empty Criteria.
func TestFind_EmptyCriteria_ReturnsAll(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001").
		Issue("tst-0002").
		Issue("tst-0003").
		Mem()

	issues, err := s.Find(tasks.Criteria{}, tasks.FindOptions{})
	if err != nil {
		t.Fatalf("Find empty: %v", err)
	}
	if len(issues) != 3 {
		t.Errorf("Find empty: got %d issues, want 3", len(issues))
	}
}

// TestFind_ValidationError_NoScan returns *ValidationError on bad Criteria
// and runs NO scan (no issues are returned).
func TestFind_ValidationError_NoScan(t *testing.T) {
	s := storetest.New(t).Issue("tst-0001").Mem()

	_, err := s.Find(tasks.Criteria{PriorityMin: ptr(-1)}, tasks.FindOptions{})
	if err == nil {
		t.Fatal("negative PriorityMin: expected error, got nil")
	}
	var ve *tasks.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *tasks.ValidationError, got %T: %v", err, err)
	}
}

// TestFindPage_ReturnsWindowAndTotal verifies that FindPage returns correct
// windowed results and the total count.
func TestFindPage_ReturnsWindowAndTotal(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Priority(0), storetest.Creator("alice")).
		Issue("tst-0002", storetest.Priority(1), storetest.Creator("alice")).
		Issue("tst-0003", storetest.Priority(2), storetest.Creator("alice")).
		Issue("tst-0004", storetest.Priority(3), storetest.Creator("bob")).
		Mem()

	// Find alice's issues, page 1 (Offset=0, Limit=2).
	p, err := s.FindPage(
		tasks.Criteria{Creator: "alice"},
		tasks.FindOptions{Offset: 0, Limit: 2},
	)
	if err != nil {
		t.Fatalf("FindPage: %v", err)
	}
	if p.Total != 3 {
		t.Errorf("FindPage Total = %d, want 3", p.Total)
	}
	if len(p.Issues) != 2 {
		t.Errorf("FindPage window len = %d, want 2; ids=%v", len(p.Issues), sortIssueIDs(p.Issues))
	}
}

// TestFind_CriteriaAndExpr_IdenticalResults verifies that a Criteria and its
// equivalent hand-written Expr produce identical results.
func TestFind_CriteriaAndExpr_IdenticalResults(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Creator("alice"), storetest.Priority(1)).
		Issue("tst-0002", storetest.Creator("bob"), storetest.Priority(2)).
		Issue("tst-0003", storetest.Creator("alice"), storetest.Priority(3)).
		Mem()

	// Using Criteria.
	criteria := tasks.Criteria{Creator: "alice"}
	criteriaIssues, err := s.Find(criteria, tasks.FindOptions{})
	if err != nil {
		t.Fatalf("Find(criteria): %v", err)
	}

	// Build the expression manually to compare.
	expr, err := criteria.Build()
	if err != nil {
		t.Fatalf("criteria.Build(): %v", err)
	}

	// Using hand-written Expr.
	exprIssues, err := s.List(tasks.Filter{Expr: expr})
	if err != nil {
		t.Fatalf("List(expr): %v", err)
	}

	criteriaIDs := sortIssueIDs(criteriaIssues)
	exprIDs := sortIssueIDs(exprIssues)

	if len(criteriaIDs) != len(exprIDs) {
		t.Fatalf("criteria vs expr: different counts — criteria=%v, expr=%v", criteriaIDs, exprIDs)
	}
	for i := range criteriaIDs {
		if criteriaIDs[i] != exprIDs[i] {
			t.Errorf("criteria vs expr: position %d: %q != %q", i, criteriaIDs[i], exprIDs[i])
		}
	}
}

// TestFind_ColdScope_StatusClosed_AutoScansClosedPartition verifies that a
// Criteria with Statuses including StatusClosed causes the cold partition to be
// scanned automatically (same as a hand-written Expr with status == "closed").
//
// This tests QUERY-SPEC §5: cold scope is derived from the BUILT expression by
// List, not from inspecting the Criteria struct.
func TestFind_ColdScope_StatusClosed_AutoScansClosedPartition(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001").  // hot (open)
		Closed("tst-0002"). // cold (closed)
		Mem()

	// Find only closed issues. No IncludeClosed override needed — the expression
	// `status == "closed"` triggers auto-scan of the cold partition.
	issues, err := s.Find(
		tasks.Criteria{Statuses: []tasks.Status{tasks.StatusClosed}},
		tasks.FindOptions{},
	)
	if err != nil {
		t.Fatalf("Find closed status: %v", err)
	}
	ids := sortIssueIDs(issues)
	if len(ids) != 1 || ids[0] != "tst-0002" {
		t.Errorf("Find closed: got %v, want [tst-0002]", ids)
	}

	// Verify parity: the equivalent hand-written Expr produces the same result.
	exprIssues, err := s.List(tasks.Filter{Expr: `status == "closed"`})
	if err != nil {
		t.Fatalf("List expr closed: %v", err)
	}
	exprIDs := sortIssueIDs(exprIssues)
	if len(ids) != len(exprIDs) || (len(ids) > 0 && ids[0] != exprIDs[0]) {
		t.Errorf("cold-scope parity: criteria=%v, expr=%v", ids, exprIDs)
	}
}

// TestFind_ColdScope_ClosedField_AutoScansClosedPartition verifies that a
// Criteria with ClosedFrom/ClosedTo triggers the cold partition scan, matching
// QUERY-SPEC §5 (any closed-field comparison triggers cold scope).
func TestFind_ColdScope_ClosedField_AutoScansClosedPartition(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001").  // hot
		Closed("tst-0002"). // cold
		Mem()

	// Use a ClosedFrom bound far in the past so it matches any closed issue.
	epoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	issues, err := s.Find(
		tasks.Criteria{ClosedFrom: &epoch},
		tasks.FindOptions{},
	)
	if err != nil {
		t.Fatalf("Find ClosedFrom: %v", err)
	}

	// The closed issue must appear (cold partition auto-scanned from closed>=epoch expression).
	ids := sortIssueIDs(issues)
	found := false
	for _, id := range ids {
		if id == "tst-0002" {
			found = true
		}
	}
	if !found {
		t.Errorf("Find ClosedFrom: closed issue tst-0002 not found; got %v", ids)
	}

	// Parity: built expression and its hand-written equivalent must match.
	builtExpr, err := tasks.Criteria{ClosedFrom: &epoch}.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	exprIssues, err := s.List(tasks.Filter{Expr: builtExpr})
	if err != nil {
		t.Fatalf("List with built expr: %v", err)
	}
	if len(issues) != len(exprIssues) {
		t.Errorf("cold-scope parity: Find returned %d, List returned %d", len(issues), len(exprIssues))
	}
}

// TestFind_FindOptions_Offset_Limit verifies that FindOptions.Offset and Limit
// behave the same as Filter.Offset and Filter.Limit.
func TestFind_FindOptions_Offset_Limit(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Priority(0)).
		Issue("tst-0002", storetest.Priority(1)).
		Issue("tst-0003", storetest.Priority(2)).
		Issue("tst-0004", storetest.Priority(3)).
		Mem()

	// Offset=1, Limit=2 → skip tst-0001, take tst-0002 and tst-0003.
	issues, err := s.Find(tasks.Criteria{}, tasks.FindOptions{Offset: 1, Limit: 2})
	if err != nil {
		t.Fatalf("Find with Offset+Limit: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("Find Offset=1,Limit=2: got %d, want 2; ids=%v", len(issues), sortIssueIDs(issues))
	}
	if issues[0].ID != "tst-0002" || issues[1].ID != "tst-0003" {
		t.Errorf("Find Offset=1,Limit=2: got %v, want [tst-0002 tst-0003]", sortIssueIDs(issues))
	}
}

// TestFind_FindOptions_IncludeClosed overrides cold scope explicitly.
func TestFind_FindOptions_IncludeClosed(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001").
		Closed("tst-0002").
		Mem()

	// Without IncludeClosed: should only see hot issue.
	issues, err := s.Find(tasks.Criteria{}, tasks.FindOptions{})
	if err != nil {
		t.Fatalf("Find without IncludeClosed: %v", err)
	}
	if len(issues) != 1 {
		t.Errorf("without IncludeClosed: got %d issues, want 1", len(issues))
	}

	// With IncludeClosed: should see both.
	issues, err = s.Find(tasks.Criteria{}, tasks.FindOptions{IncludeClosed: true})
	if err != nil {
		t.Fatalf("Find with IncludeClosed: %v", err)
	}
	if len(issues) != 2 {
		t.Errorf("with IncludeClosed: got %d issues, want 2", len(issues))
	}
}

// TestFind_FindPage_ValidationError_NoScan verifies FindPage also returns
// *ValidationError on bad Criteria without scanning.
func TestFind_FindPage_ValidationError_NoScan(t *testing.T) {
	s := storetest.New(t).Issue("tst-0001").Mem()

	_, err := s.FindPage(tasks.Criteria{Statuses: []tasks.Status{"invalid"}}, tasks.FindOptions{})
	if err == nil {
		t.Fatal("invalid Criteria: expected error, got nil")
	}
	var ve *tasks.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *tasks.ValidationError, got %T: %v", err, err)
	}
}
