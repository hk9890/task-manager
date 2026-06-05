package query_test

// L1 pure unit tests for the query package (lex + parse + AST).
// No filesystem, no store, no tasks import.
// Covers QUERY-SPEC.md §1 grammar, §6 examples, §4 error classes.

import (
	"errors"
	"testing"

	"github.com/hk9890/agent-tasks/sdk/tasks/internal/query"
)

// ---- helpers ---------------------------------------------------------------

func mustParse(t *testing.T, expr string) query.Node {
	t.Helper()
	n, err := query.Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q) unexpected error: %v", expr, err)
	}
	return n
}

func mustFail(t *testing.T, expr string) *query.ParseError {
	t.Helper()
	_, err := query.Parse(expr)
	if err == nil {
		t.Fatalf("Parse(%q) expected error, got nil", expr)
	}
	var pe *query.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("Parse(%q) expected *ParseError, got %T: %v", expr, err, err)
	}
	return pe
}

// ---- §1: empty expression --------------------------------------------------

func TestParse_EmptyExpr_AlwaysTrue(t *testing.T) {
	cases := []string{"", "   ", "\t\n"}
	for _, c := range cases {
		n := mustParse(t, c)
		if _, ok := n.(*query.TrueNode); !ok {
			t.Errorf("Parse(%q) want TrueNode, got %T", c, n)
		}
	}
}

// ---- §6 examples -----------------------------------------------------------

func TestParse_Section6_Examples(t *testing.T) {
	examples := []string{
		`status == "open"`,
		`status == "open" && priority <= 1`,
		`type == bug && label ~ "area:db"`,
		`ready && priority <= 2`,
		`text ~ "drill" && !blocked`,
		`assignee == "hans" && (type == bug || type == chore)`,
		`closed > "2026-01-01"`,
		`parent == "dtt-0007"`,
	}
	for _, expr := range examples {
		t.Run(expr, func(t *testing.T) {
			mustParse(t, expr)
		})
	}
}

// ---- §1: precedence and associativity -------------------------------------

func TestParse_Precedence_OrThenAnd(t *testing.T) {
	// a || b && c => a || (b && c)
	// parse must produce OR(a, AND(b,c)), not AND(OR(a,b), c)
	n := mustParse(t, `status == "open" || type == bug && priority <= 2`)
	or, ok := n.(*query.BinNode)
	if !ok || or.Op != "||" {
		t.Fatalf("expected top-level ||, got %T %v", n, n)
	}
	_, isAnd := or.Right.(*query.BinNode)
	if !isAnd {
		t.Fatalf("expected right child to be &&, got %T", or.Right)
	}
}

func TestParse_Precedence_NotBindsTighter(t *testing.T) {
	// !ready && blocked => (!ready) && blocked
	n := mustParse(t, `!ready && blocked`)
	and, ok := n.(*query.BinNode)
	if !ok || and.Op != "&&" {
		t.Fatalf("expected top-level &&, got %T", n)
	}
	not, isNot := and.Left.(*query.NotNode)
	if !isNot {
		t.Fatalf("expected left child to be NOT, got %T", and.Left)
	}
	bare, isBare := not.Operand.(*query.BareNode)
	if !isBare || bare.Name != "ready" {
		t.Fatalf("expected bare(ready) inside NOT, got %T %v", not.Operand, not.Operand)
	}
}

func TestParse_Precedence_LeftAssoc_And(t *testing.T) {
	// a && b && c => (a && b) && c (left-assoc)
	n := mustParse(t, `type == bug && priority <= 1 && status == "open"`)
	outer, ok := n.(*query.BinNode)
	if !ok || outer.Op != "&&" {
		t.Fatalf("expected top-level &&, got %T", n)
	}
	inner, ok := outer.Left.(*query.BinNode)
	if !ok || inner.Op != "&&" {
		t.Fatalf("expected left child to be &&, got %T", outer.Left)
	}
}

func TestParse_Precedence_LeftAssoc_Or(t *testing.T) {
	// a || b || c => (a || b) || c (left-assoc)
	n := mustParse(t, `type == bug || type == chore || type == epic`)
	outer, ok := n.(*query.BinNode)
	if !ok || outer.Op != "||" {
		t.Fatalf("expected top-level ||, got %T", n)
	}
	inner, ok := outer.Left.(*query.BinNode)
	if !ok || inner.Op != "||" {
		t.Fatalf("expected left child to be ||, got %T", outer.Left)
	}
}

func TestParse_Parens_Override(t *testing.T) {
	// (a || b) && c => AND((a||b), c)
	n := mustParse(t, `(type == bug || type == chore) && priority <= 2`)
	and, ok := n.(*query.BinNode)
	if !ok || and.Op != "&&" {
		t.Fatalf("expected top-level &&, got %T", n)
	}
	_, isOr := and.Left.(*query.BinNode)
	if !isOr {
		t.Fatalf("expected left to be ||, got %T", and.Left)
	}
}

// ---- bool fields (bare predicates) ----------------------------------------

func TestParse_BareField_Ready(t *testing.T) {
	n := mustParse(t, `ready`)
	bare, ok := n.(*query.BareNode)
	if !ok || bare.Name != "ready" {
		t.Fatalf("expected BareNode{ready}, got %T %v", n, n)
	}
}

func TestParse_BareField_Blocked(t *testing.T) {
	n := mustParse(t, `blocked`)
	bare, ok := n.(*query.BareNode)
	if !ok || bare.Name != "blocked" {
		t.Fatalf("expected BareNode{blocked}, got %T %v", n, n)
	}
}

func TestParse_NotReady(t *testing.T) {
	n := mustParse(t, `!ready`)
	not, ok := n.(*query.NotNode)
	if !ok {
		t.Fatalf("expected NotNode, got %T", n)
	}
	bare, ok := not.Operand.(*query.BareNode)
	if !ok || bare.Name != "ready" {
		t.Fatalf("expected BareNode{ready}, got %T %v", not.Operand, not.Operand)
	}
}

// ---- comparison nodes -------------------------------------------------------

func TestParse_Comparison_StatusEq(t *testing.T) {
	n := mustParse(t, `status == "open"`)
	cmp, ok := n.(*query.CmpNode)
	if !ok {
		t.Fatalf("expected CmpNode, got %T", n)
	}
	if cmp.Field != "status" || cmp.Op != "==" {
		t.Errorf("field=%q op=%q want status/==", cmp.Field, cmp.Op)
	}
	sv, ok := cmp.Value.(*query.StringValue)
	if !ok || sv.S != "open" {
		t.Errorf("value: got %T %v, want StringValue{open}", cmp.Value, cmp.Value)
	}
}

func TestParse_Comparison_PriorityLE(t *testing.T) {
	n := mustParse(t, `priority <= 2`)
	cmp, ok := n.(*query.CmpNode)
	if !ok {
		t.Fatalf("expected CmpNode, got %T", n)
	}
	if cmp.Field != "priority" || cmp.Op != "<=" {
		t.Errorf("field=%q op=%q want priority/<=", cmp.Field, cmp.Op)
	}
	iv, ok := cmp.Value.(*query.IntValue)
	if !ok || iv.N != 2 {
		t.Errorf("value: got %T %v, want IntValue{2}", cmp.Value, cmp.Value)
	}
}

func TestParse_Comparison_LabelTilde(t *testing.T) {
	n := mustParse(t, `label ~ "area:db"`)
	cmp, ok := n.(*query.CmpNode)
	if !ok {
		t.Fatalf("expected CmpNode, got %T", n)
	}
	if cmp.Field != "label" || cmp.Op != "~" {
		t.Errorf("field=%q op=%q want label/~", cmp.Field, cmp.Op)
	}
	sv, ok := cmp.Value.(*query.StringValue)
	if !ok || sv.S != "area:db" {
		t.Errorf("value: got %T %v, want StringValue{area:db}", cmp.Value, cmp.Value)
	}
}

func TestParse_Comparison_TypeBareword(t *testing.T) {
	n := mustParse(t, `type == bug`)
	cmp, ok := n.(*query.CmpNode)
	if !ok {
		t.Fatalf("expected CmpNode, got %T", n)
	}
	sv, ok := cmp.Value.(*query.StringValue)
	if !ok || sv.S != "bug" {
		t.Errorf("value: got %T %v, want StringValue{bug}", cmp.Value, cmp.Value)
	}
}

func TestParse_Comparison_DateBareword(t *testing.T) {
	n := mustParse(t, `closed > "2026-01-01"`)
	cmp, ok := n.(*query.CmpNode)
	if !ok {
		t.Fatalf("expected CmpNode, got %T", n)
	}
	if cmp.Field != "closed" || cmp.Op != ">" {
		t.Errorf("field=%q op=%q want closed/>", cmp.Field, cmp.Op)
	}
	dv, ok := cmp.Value.(*query.DateValue)
	if !ok {
		t.Errorf("value: got %T %v, want DateValue", cmp.Value, cmp.Value)
	}
	_ = dv
}

func TestParse_Comparison_ParentQuoted(t *testing.T) {
	n := mustParse(t, `parent == "dtt-0007"`)
	cmp, ok := n.(*query.CmpNode)
	if !ok {
		t.Fatalf("expected CmpNode, got %T", n)
	}
	if cmp.Field != "parent" {
		t.Errorf("field=%q want parent", cmp.Field)
	}
	sv, ok := cmp.Value.(*query.StringValue)
	if !ok || sv.S != "dtt-0007" {
		t.Errorf("value: got %T %v, want StringValue{dtt-0007}", cmp.Value, cmp.Value)
	}
}

// Test all operators are parsed correctly.
func TestParse_AllOperators(t *testing.T) {
	ops := []struct {
		expr string
		op   string
	}{
		{`priority == 1`, "=="},
		{`priority != 1`, "!="},
		{`priority < 1`, "<"},
		{`priority <= 1`, "<="},
		{`priority > 1`, ">"},
		{`priority >= 1`, ">="},
		{`assignee ~ "hans"`, "~"},
	}
	for _, tc := range ops {
		t.Run(tc.op, func(t *testing.T) {
			n := mustParse(t, tc.expr)
			cmp, ok := n.(*query.CmpNode)
			if !ok {
				t.Fatalf("expected CmpNode, got %T", n)
			}
			if cmp.Op != tc.op {
				t.Errorf("op=%q want %q", cmp.Op, tc.op)
			}
		})
	}
}

// ---- string escapes in quoted values ----------------------------------------

func TestParse_QuotedString_EscapeBackslash(t *testing.T) {
	n := mustParse(t, `assignee == "a\\b"`)
	cmp := n.(*query.CmpNode)
	sv := cmp.Value.(*query.StringValue)
	if sv.S != `a\b` {
		t.Errorf("got %q want %q", sv.S, `a\b`)
	}
}

func TestParse_QuotedString_EscapeDoubleQuote(t *testing.T) {
	n := mustParse(t, `assignee == "say \"hi\""`)
	cmp := n.(*query.CmpNode)
	sv := cmp.Value.(*query.StringValue)
	if sv.S != `say "hi"` {
		t.Errorf("got %q want %q", sv.S, `say "hi"`)
	}
}

// ---- §4 error classes -------------------------------------------------------

func TestParse_Error_UnknownField(t *testing.T) {
	pe := mustFail(t, `foobar == "x"`)
	if pe.Pos < 0 {
		t.Errorf("Pos should be >= 0, got %d", pe.Pos)
	}
}

func TestParse_Error_UnknownBarePredicate(t *testing.T) {
	pe := mustFail(t, `foobar`)
	if pe.Pos < 0 {
		t.Errorf("Pos should be >= 0, got %d", pe.Pos)
	}
}

func TestParse_Error_OpNotPermittedForField_Status(t *testing.T) {
	// status only allows == and !=
	pe := mustFail(t, `status < "open"`)
	if pe.Pos < 0 {
		t.Errorf("Pos=%d want >= 0", pe.Pos)
	}
}

func TestParse_Error_OpNotPermittedForField_Text(t *testing.T) {
	// text only allows ~
	pe := mustFail(t, `text == "x"`)
	if pe.Pos < 0 {
		t.Errorf("Pos=%d want >= 0", pe.Pos)
	}
}

func TestParse_Error_MalformedValue_PriorityNotInt(t *testing.T) {
	pe := mustFail(t, `priority == open`)
	if pe.Pos < 0 {
		t.Errorf("Pos=%d want >= 0", pe.Pos)
	}
}

func TestParse_Error_MalformedValue_PriorityOutOfRange(t *testing.T) {
	pe := mustFail(t, `priority == 5`)
	if pe.Pos < 0 {
		t.Errorf("Pos=%d want >= 0", pe.Pos)
	}
}

func TestParse_Error_MalformedValue_PriorityNegative(t *testing.T) {
	pe := mustFail(t, `priority == -1`)
	if pe.Pos < 0 {
		t.Errorf("Pos=%d want >= 0", pe.Pos)
	}
}

func TestParse_Error_MalformedValue_UnknownStatus(t *testing.T) {
	pe := mustFail(t, `status == "flying"`)
	if pe.Pos < 0 {
		t.Errorf("Pos=%d want >= 0", pe.Pos)
	}
}

func TestParse_Error_MalformedValue_UnknownType(t *testing.T) {
	pe := mustFail(t, `type == "flying"`)
	if pe.Pos < 0 {
		t.Errorf("Pos=%d want >= 0", pe.Pos)
	}
}

func TestParse_Error_MalformedValue_BadDate(t *testing.T) {
	pe := mustFail(t, `closed > "not-a-date"`)
	if pe.Pos < 0 {
		t.Errorf("Pos=%d want >= 0", pe.Pos)
	}
}

func TestParse_Error_SyntaxError_UnbalancedParen(t *testing.T) {
	pe := mustFail(t, `(status == "open"`)
	if pe.Pos < 0 {
		t.Errorf("Pos=%d want >= 0", pe.Pos)
	}
}

func TestParse_Error_SyntaxError_DanglingOperator(t *testing.T) {
	pe := mustFail(t, `status == "open" &&`)
	if pe.Pos < 0 {
		t.Errorf("Pos=%d want >= 0", pe.Pos)
	}
}

func TestParse_Error_SyntaxError_MissingValue(t *testing.T) {
	pe := mustFail(t, `status ==`)
	if pe.Pos < 0 {
		t.Errorf("Pos=%d want >= 0", pe.Pos)
	}
}

func TestParse_Error_SyntaxError_TrailingTokens(t *testing.T) {
	pe := mustFail(t, `status == "open" "extra"`)
	if pe.Pos < 0 {
		t.Errorf("Pos=%d want >= 0", pe.Pos)
	}
}

func TestParse_Error_Pos_ByteOffset(t *testing.T) {
	// The error position for unknown field "foobar" should be near byte 0.
	pe := mustFail(t, `foobar == "x"`)
	if pe.Pos != 0 {
		t.Errorf("Pos=%d want 0 for unknown field at start", pe.Pos)
	}
}

func TestParse_Error_Pos_NotAtStart(t *testing.T) {
	// Error is on the second clause — Pos should be after the first clause.
	pe := mustFail(t, `status == "open" && foobar`)
	if pe.Pos <= 0 {
		t.Errorf("Pos=%d want > 0 for error in second clause", pe.Pos)
	}
}

// ---- priority valid range --------------------------------------------------

func TestParse_Priority_ValidRange(t *testing.T) {
	for i := 0; i <= 4; i++ {
		expr := "priority == " + string(rune('0'+i))
		mustParse(t, expr)
	}
}

// ---- all fields accepted ---------------------------------------------------

func TestParse_AllComparisonFields(t *testing.T) {
	cases := []string{
		`status == "open"`,
		`type == bug`,
		`priority >= 1`,
		`assignee == "hans"`,
		`parent == "dtt-0001"`,
		`label == "area:db"`,
		`text ~ "drill"`,
		`created > "2026-01-01"`,
		`updated < "2026-01-01"`,
		`closed > "2026-01-01"`,
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			mustParse(t, expr)
		})
	}
}

// ---- all valid enum values --------------------------------------------------

func TestParse_StatusEnumValues(t *testing.T) {
	valid := []string{"open", "in_progress", "blocked", "closed"}
	for _, v := range valid {
		mustParse(t, `status == "`+v+`"`)
	}
}

func TestParse_TypeEnumValues(t *testing.T) {
	valid := []string{"task", "bug", "feature", "epic", "chore"}
	for _, v := range valid {
		mustParse(t, `type == `+v)
	}
}

// ---- date parsing (ISO YYYY-MM-DD and full timestamp) ----------------------

func TestParse_DateValue_YYYYMMDDIsMidnightUTC(t *testing.T) {
	n := mustParse(t, `created == "2026-01-15"`)
	cmp := n.(*query.CmpNode)
	dv, ok := cmp.Value.(*query.DateValue)
	if !ok {
		t.Fatalf("expected DateValue, got %T", cmp.Value)
	}
	if dv.T.IsZero() {
		t.Error("DateValue.T should not be zero")
	}
	// YYYY-MM-DD -> midnight UTC
	if dv.T.Hour() != 0 || dv.T.Minute() != 0 || dv.T.Second() != 0 {
		t.Errorf("expected midnight UTC, got %v", dv.T)
	}
	if dv.T.UTC().Format("2006-01-02") != "2026-01-15" {
		t.Errorf("date mismatch: %v", dv.T)
	}
}

func TestParse_DateValue_FullTimestamp(t *testing.T) {
	n := mustParse(t, `created == "2026-01-15T10:30:00Z"`)
	cmp := n.(*query.CmpNode)
	dv, ok := cmp.Value.(*query.DateValue)
	if !ok {
		t.Fatalf("expected DateValue, got %T", cmp.Value)
	}
	if dv.T.IsZero() {
		t.Error("DateValue.T should not be zero")
	}
	if dv.T.Hour() != 10 {
		t.Errorf("expected hour=10, got %d", dv.T.Hour())
	}
}

// ---- bareword allowed chars -------------------------------------------------

func TestParse_Bareword_AllowedChars(t *testing.T) {
	// Bareword chars: [A-Za-z0-9_:./@-]
	cases := []string{
		`assignee == hans`,
		`label == area:db`,
		`parent == dtt-0007`,
		`label == some.label`,
		`label == user/project`,
		`label == a@b`,
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			mustParse(t, expr)
		})
	}
}

// ---- ParseError struct -------------------------------------------------------

func TestParseError_Type(t *testing.T) {
	_, err := query.Parse(`foobar`)
	if err == nil {
		t.Fatal("expected error")
	}
	var pe *query.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ParseError, got %T: %v", err, err)
	}
	if pe.Message == "" {
		t.Error("ParseError.Message should not be empty")
	}
}

func TestParseError_Implements_Error(t *testing.T) {
	pe := &query.ParseError{Pos: 5, Message: "test error"}
	s := pe.Error()
	if s == "" {
		t.Error("Error() should not return empty string")
	}
}
