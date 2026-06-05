package query_test

// eval_test.go — L1 pure unit tests for the evaluator (Compile / Predicate / Row).
// No filesystem, no store, no tasks import.
// Covers QUERY-SPEC.md §2 per-field semantics (enum, priority, assignee, label,
// text, date, ready/blocked) and §3 value types.

import (
	"errors"
	"testing"
	"time"

	"github.com/hk9890/agent-tasks/sdk/tasks/internal/query"
)

// ---- fake Row implementation -----------------------------------------------

// fakeRow is a map-backed Row for testing. Fields are stored as query.Value
// values. ready and blocked are booleans.
type fakeRow struct {
	fields  map[string]query.Value
	ready   bool
	blocked bool
}

func (r *fakeRow) Field(name string) (query.Value, bool) {
	v, ok := r.fields[name]
	return v, ok
}

func (r *fakeRow) Ready() bool   { return r.ready }
func (r *fakeRow) Blocked() bool { return r.blocked }

// helpers to build rows

func strVal(s string) *query.StringValue   { return &query.StringValue{S: s} }
func intVal(n int) *query.IntValue         { return &query.IntValue{N: n} }
func dateVal(t time.Time) *query.DateValue { return &query.DateValue{T: t} }
func setVal(labels ...string) *query.StringSetValue {
	return &query.StringSetValue{Members: labels}
}

func row(fields map[string]query.Value, ready, blocked bool) *fakeRow {
	return &fakeRow{fields: fields, ready: ready, blocked: blocked}
}

// mustCompile compiles an expression and fails the test on error.
func mustCompile(t *testing.T, expr string) query.Predicate {
	t.Helper()
	p, err := query.Compile(expr)
	if err != nil {
		t.Fatalf("Compile(%q) unexpected error: %v", expr, err)
	}
	return p
}

// mustCompileFail compiles an expression and expects a *ParseError.
func mustCompileFail(t *testing.T, expr string) *query.ParseError {
	t.Helper()
	_, err := query.Compile(expr)
	if err == nil {
		t.Fatalf("Compile(%q) expected error, got nil", expr)
	}
	var pe *query.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("Compile(%q) expected *ParseError, got %T: %v", expr, err, err)
	}
	return pe
}

// ---- Compile: empty expression is always-true predicate -------------------

func TestCompile_Empty_AlwaysTrue(t *testing.T) {
	cases := []string{"", "   ", "\t\n"}
	r := row(map[string]query.Value{}, false, false)
	for _, expr := range cases {
		p := mustCompile(t, expr)
		if !p.Match(r) {
			t.Errorf("Compile(%q).Match() want true (always-true predicate)", expr)
		}
	}
}

// ---- Compile: malformed expression returns *ParseError --------------------

func TestCompile_Malformed_ParseError(t *testing.T) {
	cases := []string{
		`foobar == "x"`,     // unknown field
		`status < "open"`,   // bad operator for enum
		`priority == 5`,     // priority out of range
		`(status == "open"`, // unbalanced paren
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			mustCompileFail(t, expr)
		})
	}
}

// ---- enum fields: status, type ----------------------------------------------

func TestEval_Status_Eq(t *testing.T) {
	p := mustCompile(t, `status == "open"`)
	match := row(map[string]query.Value{"status": strVal("open")}, false, false)
	noMatch := row(map[string]query.Value{"status": strVal("closed")}, false, false)
	if !p.Match(match) {
		t.Error("status == open: expected match")
	}
	if p.Match(noMatch) {
		t.Error("status == open: expected no match for closed")
	}
}

func TestEval_Status_Neq(t *testing.T) {
	p := mustCompile(t, `status != "open"`)
	match := row(map[string]query.Value{"status": strVal("closed")}, false, false)
	noMatch := row(map[string]query.Value{"status": strVal("open")}, false, false)
	if !p.Match(match) {
		t.Error("status != open: expected match for closed")
	}
	if p.Match(noMatch) {
		t.Error("status != open: expected no match for open")
	}
}

func TestEval_Type_Eq(t *testing.T) {
	p := mustCompile(t, `type == bug`)
	match := row(map[string]query.Value{"type": strVal("bug")}, false, false)
	noMatch := row(map[string]query.Value{"type": strVal("task")}, false, false)
	if !p.Match(match) {
		t.Error("type == bug: expected match")
	}
	if p.Match(noMatch) {
		t.Error("type == bug: expected no match for task")
	}
}

// Enum comparison is case-sensitive (QUERY-SPEC §2 "==,!= on strings exact & case-sensitive").
func TestEval_Status_CaseSensitive(t *testing.T) {
	p := mustCompile(t, `status == "open"`)
	// "Open" (capital) must not match
	noMatch := row(map[string]query.Value{"status": strVal("Open")}, false, false)
	if p.Match(noMatch) {
		t.Error("status == open: must not match 'Open' (case-sensitive)")
	}
}

// ---- priority field (int, ordering operators) ------------------------------

func TestEval_Priority_Eq(t *testing.T) {
	p := mustCompile(t, `priority == 2`)
	match := row(map[string]query.Value{"priority": intVal(2)}, false, false)
	noMatch := row(map[string]query.Value{"priority": intVal(3)}, false, false)
	if !p.Match(match) {
		t.Error("priority == 2: expected match")
	}
	if p.Match(noMatch) {
		t.Error("priority == 2: expected no match")
	}
}

func TestEval_Priority_LT(t *testing.T) {
	p := mustCompile(t, `priority < 2`)
	match := row(map[string]query.Value{"priority": intVal(1)}, false, false)
	noMatch := row(map[string]query.Value{"priority": intVal(2)}, false, false)
	if !p.Match(match) {
		t.Error("priority < 2: expected match for 1")
	}
	if p.Match(noMatch) {
		t.Error("priority < 2: expected no match for 2")
	}
}

func TestEval_Priority_LE(t *testing.T) {
	p := mustCompile(t, `priority <= 2`)
	eq := row(map[string]query.Value{"priority": intVal(2)}, false, false)
	lt := row(map[string]query.Value{"priority": intVal(1)}, false, false)
	gt := row(map[string]query.Value{"priority": intVal(3)}, false, false)
	if !p.Match(eq) {
		t.Error("priority <= 2: expected match for 2")
	}
	if !p.Match(lt) {
		t.Error("priority <= 2: expected match for 1")
	}
	if p.Match(gt) {
		t.Error("priority <= 2: expected no match for 3")
	}
}

func TestEval_Priority_GT(t *testing.T) {
	p := mustCompile(t, `priority > 1`)
	match := row(map[string]query.Value{"priority": intVal(2)}, false, false)
	noMatch := row(map[string]query.Value{"priority": intVal(1)}, false, false)
	if !p.Match(match) {
		t.Error("priority > 1: expected match for 2")
	}
	if p.Match(noMatch) {
		t.Error("priority > 1: expected no match for 1")
	}
}

func TestEval_Priority_GE(t *testing.T) {
	p := mustCompile(t, `priority >= 2`)
	eq := row(map[string]query.Value{"priority": intVal(2)}, false, false)
	lt := row(map[string]query.Value{"priority": intVal(1)}, false, false)
	gt := row(map[string]query.Value{"priority": intVal(3)}, false, false)
	if !p.Match(eq) {
		t.Error("priority >= 2: expected match for 2")
	}
	if !p.Match(gt) {
		t.Error("priority >= 2: expected match for 3")
	}
	if p.Match(lt) {
		t.Error("priority >= 2: expected no match for 1")
	}
}

// ---- assignee field (string: ==,!=,~) ----------------------------------------

func TestEval_Assignee_Eq(t *testing.T) {
	p := mustCompile(t, `assignee == "hans"`)
	match := row(map[string]query.Value{"assignee": strVal("hans")}, false, false)
	noMatch := row(map[string]query.Value{"assignee": strVal("alice")}, false, false)
	if !p.Match(match) {
		t.Error("assignee == hans: expected match")
	}
	if p.Match(noMatch) {
		t.Error("assignee == hans: expected no match")
	}
}

func TestEval_Assignee_Neq(t *testing.T) {
	p := mustCompile(t, `assignee != "hans"`)
	match := row(map[string]query.Value{"assignee": strVal("alice")}, false, false)
	noMatch := row(map[string]query.Value{"assignee": strVal("hans")}, false, false)
	if !p.Match(match) {
		t.Error("assignee != hans: expected match for alice")
	}
	if p.Match(noMatch) {
		t.Error("assignee != hans: expected no match for hans")
	}
}

func TestEval_Assignee_Tilde_CaseInsensitive(t *testing.T) {
	p := mustCompile(t, `assignee ~ "HANS"`)
	match := row(map[string]query.Value{"assignee": strVal("hans kohlreiter")}, false, false)
	noMatch := row(map[string]query.Value{"assignee": strVal("alice")}, false, false)
	if !p.Match(match) {
		t.Error("assignee ~ HANS: expected ci-match for 'hans kohlreiter'")
	}
	if p.Match(noMatch) {
		t.Error("assignee ~ HANS: expected no match for 'alice'")
	}
}

func TestEval_Assignee_Eq_CaseSensitive(t *testing.T) {
	// == is exact/case-sensitive
	p := mustCompile(t, `assignee == "Hans"`)
	noMatch := row(map[string]query.Value{"assignee": strVal("hans")}, false, false)
	if p.Match(noMatch) {
		t.Error("assignee == Hans: must not match 'hans' (exact match)")
	}
}

// ---- parent field (string: ==,!=) -------------------------------------------

func TestEval_Parent_Eq(t *testing.T) {
	p := mustCompile(t, `parent == "dtt-0007"`)
	match := row(map[string]query.Value{"parent": strVal("dtt-0007")}, false, false)
	noMatch := row(map[string]query.Value{"parent": strVal("dtt-0001")}, false, false)
	empty := row(map[string]query.Value{"parent": strVal("")}, false, false)
	if !p.Match(match) {
		t.Error("parent == dtt-0007: expected match")
	}
	if p.Match(noMatch) {
		t.Error("parent == dtt-0007: expected no match")
	}
	if p.Match(empty) {
		t.Error("parent == dtt-0007: expected no match for empty")
	}
}

func TestEval_Parent_Eq_Empty(t *testing.T) {
	// parent == "" matches issues with no parent
	p := mustCompile(t, `parent == ""`)
	noParent := row(map[string]query.Value{"parent": strVal("")}, false, false)
	withParent := row(map[string]query.Value{"parent": strVal("dtt-0007")}, false, false)
	if !p.Match(noParent) {
		t.Error("parent == empty: expected match for no-parent")
	}
	if p.Match(withParent) {
		t.Error("parent == empty: expected no match for issue with parent")
	}
}

// ---- label field (string set: ==,!=,~) --------------------------------------

func TestEval_Label_Eq_Membership(t *testing.T) {
	// label == "x" → true iff the set contains exactly "x" (membership)
	p := mustCompile(t, `label == "area:db"`)
	match := row(map[string]query.Value{"label": setVal("area:db", "priority:high")}, false, false)
	noMatch := row(map[string]query.Value{"label": setVal("area:ui", "priority:high")}, false, false)
	empty := row(map[string]query.Value{"label": setVal()}, false, false)
	if !p.Match(match) {
		t.Error("label == area:db: expected match when set contains it")
	}
	if p.Match(noMatch) {
		t.Error("label == area:db: expected no match when set lacks it")
	}
	if p.Match(empty) {
		t.Error("label == area:db: expected no match for empty set")
	}
}

func TestEval_Label_Neq(t *testing.T) {
	// label != "x" → true iff the set does NOT contain "x"
	p := mustCompile(t, `label != "area:db"`)
	match := row(map[string]query.Value{"label": setVal("area:ui")}, false, false)
	noMatch := row(map[string]query.Value{"label": setVal("area:db")}, false, false)
	if !p.Match(match) {
		t.Error("label != area:db: expected match when set lacks it")
	}
	if p.Match(noMatch) {
		t.Error("label != area:db: expected no match when set contains it")
	}
}

func TestEval_Label_Tilde_CISubstring(t *testing.T) {
	// label ~ "x" → some label contains ci-substring x
	p := mustCompile(t, `label ~ "area"`)
	match := row(map[string]query.Value{"label": setVal("AREA:DB", "priority:high")}, false, false)
	noMatch := row(map[string]query.Value{"label": setVal("priority:high")}, false, false)
	if !p.Match(match) {
		t.Error("label ~ area: expected ci-match for 'AREA:DB'")
	}
	if p.Match(noMatch) {
		t.Error("label ~ area: expected no match")
	}
}

func TestEval_Label_Eq_CaseSensitive(t *testing.T) {
	// == is exact and case-sensitive
	p := mustCompile(t, `label == "Area:DB"`)
	noMatch := row(map[string]query.Value{"label": setVal("area:db")}, false, false)
	if p.Match(noMatch) {
		t.Error("label == Area:DB: must not match 'area:db' (case-sensitive)")
	}
}

// ---- text virtual field (~  only) -------------------------------------------

func TestEval_Text_Tilde(t *testing.T) {
	// text ~ "drill" → ci-substring match across id+title+description
	p := mustCompile(t, `text ~ "drill"`)
	// text field is a virtual string; Row provides it pre-concatenated or as a single StringValue
	match := row(map[string]query.Value{"text": strVal("issue about drill navigation")}, false, false)
	noMatch := row(map[string]query.Value{"text": strVal("no match here")}, false, false)
	if !p.Match(match) {
		t.Error("text ~ drill: expected ci-match")
	}
	if p.Match(noMatch) {
		t.Error("text ~ drill: expected no match")
	}
}

func TestEval_Text_Tilde_CaseInsensitive(t *testing.T) {
	p := mustCompile(t, `text ~ "DRILL"`)
	match := row(map[string]query.Value{"text": strVal("fix drill issue")}, false, false)
	if !p.Match(match) {
		t.Error("text ~ DRILL: expected ci-match for lowercase content")
	}
}

// text == "x" is a parse error (only ~ is valid)
func TestEval_Text_EqIsError(t *testing.T) {
	mustCompileFail(t, `text == "x"`)
}

// ---- date fields: created, updated, closed ----------------------------------

func TestEval_Created_GT(t *testing.T) {
	t0, _ := time.Parse("2006-01-02", "2026-01-01")
	t1, _ := time.Parse("2006-01-02", "2026-06-01")
	p := mustCompile(t, `created > "2026-01-01"`)
	match := row(map[string]query.Value{"created": dateVal(t1)}, false, false)
	noMatch := row(map[string]query.Value{"created": dateVal(t0)}, false, false)
	if !p.Match(match) {
		t.Error("created > 2026-01-01: expected match for later date")
	}
	if p.Match(noMatch) {
		t.Error("created > 2026-01-01: expected no match for equal date")
	}
}

func TestEval_Updated_LT(t *testing.T) {
	t0, _ := time.Parse("2006-01-02", "2026-01-01")
	t1, _ := time.Parse("2006-01-02", "2025-06-01")
	p := mustCompile(t, `updated < "2026-01-01"`)
	match := row(map[string]query.Value{"updated": dateVal(t1)}, false, false)
	noMatch := row(map[string]query.Value{"updated": dateVal(t0)}, false, false)
	if !p.Match(match) {
		t.Error("updated < 2026-01-01: expected match for earlier date")
	}
	if p.Match(noMatch) {
		t.Error("updated < 2026-01-01: expected no match for equal date")
	}
}

func TestEval_Closed_CompareFalseWhenUnset(t *testing.T) {
	// closed comparisons must be false when the issue has no closed timestamp
	// QUERY-SPEC §2: "On an issue with no closed timestamp, every closed comparison is false."
	p := mustCompile(t, `closed > "2026-01-01"`)
	// Row with no "closed" field (unset/absent)
	noClosedRow := row(map[string]query.Value{}, false, false)
	if p.Match(noClosedRow) {
		t.Error("closed > date: must be false when closed is unset")
	}
	// Row with zero-time closed (also means unset)
	zeroRow := row(map[string]query.Value{"closed": dateVal(time.Time{})}, false, false)
	if p.Match(zeroRow) {
		t.Error("closed > date: must be false when closed is zero time")
	}
}

func TestEval_Closed_EqFalseWhenUnset(t *testing.T) {
	p := mustCompile(t, `closed == "2026-01-01"`)
	noClosedRow := row(map[string]query.Value{}, false, false)
	if p.Match(noClosedRow) {
		t.Error("closed == date: must be false when closed is unset")
	}
}

func TestEval_Closed_Comparison_MatchWhenSet(t *testing.T) {
	closedAt, _ := time.Parse("2006-01-02", "2026-03-15")
	p := mustCompile(t, `closed > "2026-01-01"`)
	match := row(map[string]query.Value{"closed": dateVal(closedAt)}, false, false)
	if !p.Match(match) {
		t.Error("closed > 2026-01-01: expected match when closed is set to later date")
	}
}

func TestEval_Date_EqBoundary(t *testing.T) {
	// YYYY-MM-DD value is parsed as midnight UTC; == compares exact instant
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := mustCompile(t, `created == "2026-01-01"`)
	match := row(map[string]query.Value{"created": dateVal(t0)}, false, false)
	later := row(map[string]query.Value{"created": dateVal(t0.Add(time.Second))}, false, false)
	if !p.Match(match) {
		t.Error("created == 2026-01-01: expected match for midnight UTC")
	}
	if p.Match(later) {
		t.Error("created == 2026-01-01: expected no match for 1s after midnight")
	}
}

// ---- ready / blocked bare predicates ----------------------------------------

func TestEval_Ready_True(t *testing.T) {
	p := mustCompile(t, `ready`)
	r := row(map[string]query.Value{}, true, false)
	notR := row(map[string]query.Value{}, false, false)
	if !p.Match(r) {
		t.Error("ready: expected match for ready=true")
	}
	if p.Match(notR) {
		t.Error("ready: expected no match for ready=false")
	}
}

func TestEval_Blocked_True(t *testing.T) {
	p := mustCompile(t, `blocked`)
	b := row(map[string]query.Value{}, false, true)
	notB := row(map[string]query.Value{}, false, false)
	if !p.Match(b) {
		t.Error("blocked: expected match for blocked=true")
	}
	if p.Match(notB) {
		t.Error("blocked: expected no match for blocked=false")
	}
}

func TestEval_NotReady(t *testing.T) {
	p := mustCompile(t, `!ready`)
	r := row(map[string]query.Value{}, true, false)
	notR := row(map[string]query.Value{}, false, false)
	if p.Match(r) {
		t.Error("!ready: expected no match for ready=true")
	}
	if !p.Match(notR) {
		t.Error("!ready: expected match for ready=false")
	}
}

// ---- boolean operators: && and || ------------------------------------------

func TestEval_And(t *testing.T) {
	p := mustCompile(t, `status == "open" && priority <= 1`)
	match := row(map[string]query.Value{"status": strVal("open"), "priority": intVal(1)}, false, false)
	noMatchStatus := row(map[string]query.Value{"status": strVal("closed"), "priority": intVal(1)}, false, false)
	noMatchPriority := row(map[string]query.Value{"status": strVal("open"), "priority": intVal(2)}, false, false)
	if !p.Match(match) {
		t.Error("status==open && priority<=1: expected match")
	}
	if p.Match(noMatchStatus) {
		t.Error("status==open && priority<=1: expected no match for wrong status")
	}
	if p.Match(noMatchPriority) {
		t.Error("status==open && priority<=1: expected no match for priority 2")
	}
}

func TestEval_Or(t *testing.T) {
	p := mustCompile(t, `type == bug || type == chore`)
	bug := row(map[string]query.Value{"type": strVal("bug")}, false, false)
	chore := row(map[string]query.Value{"type": strVal("chore")}, false, false)
	task := row(map[string]query.Value{"type": strVal("task")}, false, false)
	if !p.Match(bug) {
		t.Error("type==bug||type==chore: expected match for bug")
	}
	if !p.Match(chore) {
		t.Error("type==bug||type==chore: expected match for chore")
	}
	if p.Match(task) {
		t.Error("type==bug||type==chore: expected no match for task")
	}
}

func TestEval_Parens(t *testing.T) {
	p := mustCompile(t, `assignee == "hans" && (type == bug || type == chore)`)
	match := row(map[string]query.Value{"assignee": strVal("hans"), "type": strVal("bug")}, false, false)
	noMatchType := row(map[string]query.Value{"assignee": strVal("hans"), "type": strVal("task")}, false, false)
	noMatchAssignee := row(map[string]query.Value{"assignee": strVal("alice"), "type": strVal("bug")}, false, false)
	if !p.Match(match) {
		t.Error("assignee==hans && (type==bug||type==chore): expected match")
	}
	if p.Match(noMatchType) {
		t.Error("assignee==hans && (type==bug||type==chore): expected no match for wrong type")
	}
	if p.Match(noMatchAssignee) {
		t.Error("assignee==hans && (type==bug||type==chore): expected no match for wrong assignee")
	}
}

// ---- Section 6 examples (eval, not just parse) ----------------------------

func TestEval_Section6_StatusOpen(t *testing.T) {
	p := mustCompile(t, `status == "open"`)
	match := row(map[string]query.Value{"status": strVal("open")}, false, false)
	noMatch := row(map[string]query.Value{"status": strVal("closed")}, false, false)
	if !p.Match(match) {
		t.Error("status==open: expected match")
	}
	if p.Match(noMatch) {
		t.Error("status==open: expected no match")
	}
}

func TestEval_Section6_ReadyAndPriority(t *testing.T) {
	p := mustCompile(t, `ready && priority <= 2`)
	match := row(map[string]query.Value{"priority": intVal(2)}, true, false)
	noMatchReady := row(map[string]query.Value{"priority": intVal(2)}, false, false)
	noMatchPriority := row(map[string]query.Value{"priority": intVal(3)}, true, false)
	if !p.Match(match) {
		t.Error("ready && priority<=2: expected match")
	}
	if p.Match(noMatchReady) {
		t.Error("ready && priority<=2: expected no match for !ready")
	}
	if p.Match(noMatchPriority) {
		t.Error("ready && priority<=2: expected no match for priority 3")
	}
}

func TestEval_Section6_TextAndNotBlocked(t *testing.T) {
	p := mustCompile(t, `text ~ "drill" && !blocked`)
	match := row(map[string]query.Value{"text": strVal("drill navigation fix")}, false, false)
	noMatchText := row(map[string]query.Value{"text": strVal("unrelated issue")}, false, false)
	noMatchBlocked := row(map[string]query.Value{"text": strVal("drill navigation fix")}, false, true)
	if !p.Match(match) {
		t.Error("text~drill && !blocked: expected match")
	}
	if p.Match(noMatchText) {
		t.Error("text~drill && !blocked: expected no match for wrong text")
	}
	if p.Match(noMatchBlocked) {
		t.Error("text~drill && !blocked: expected no match for blocked issue")
	}
}

func TestEval_Section6_ClosedDate(t *testing.T) {
	closedAt := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	p := mustCompile(t, `closed > "2026-01-01"`)
	match := row(map[string]query.Value{"closed": dateVal(closedAt)}, false, false)
	unset := row(map[string]query.Value{}, false, false)
	if !p.Match(match) {
		t.Error("closed > 2026-01-01: expected match")
	}
	if p.Match(unset) {
		t.Error("closed > 2026-01-01: expected no match when unset")
	}
}

func TestEval_Section6_ParentEq(t *testing.T) {
	p := mustCompile(t, `parent == "dtt-0007"`)
	match := row(map[string]query.Value{"parent": strVal("dtt-0007")}, false, false)
	noMatch := row(map[string]query.Value{"parent": strVal("")}, false, false)
	if !p.Match(match) {
		t.Error("parent == dtt-0007: expected match")
	}
	if p.Match(noMatch) {
		t.Error("parent == dtt-0007: expected no match for empty parent")
	}
}

// ---- ParseError returned by Compile (not Parse) -----------------------------

func TestCompile_ParseError_HasPos(t *testing.T) {
	pe := mustCompileFail(t, `foobar == "x"`)
	if pe.Pos < 0 {
		t.Errorf("ParseError.Pos should be >= 0, got %d", pe.Pos)
	}
}

func TestCompile_ParseError_Message(t *testing.T) {
	pe := mustCompileFail(t, `foobar`)
	if pe.Message == "" {
		t.Error("ParseError.Message should not be empty")
	}
}
