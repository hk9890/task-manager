package query

// eval.go — Row interface, Predicate interface, and Compile function.
//
// Compile parses a filter expression (QUERY-SPEC.md §1) and returns a
// Predicate that can be evaluated against any Row. The internal/query package
// is PURE: no os, no vfs, no tasks import. The tasks package adapts
// *tasks.Issue → Row and uses Compile from here.
//
// Interface summary:
//
//	type Row interface { Field(name string) (Value, bool); Ready() bool; Blocked() bool }
//	type Predicate interface { Match(row Row) bool }
//	func Compile(expr string) (Predicate, error)  // *ParseError on malformed

import (
	"strings"
	"time"
)

// ---- additional Value type (for label sets) ---------------------------------

// StringSetValue represents the set of labels on an issue. The Row
// implementation for the tasks package populates this type for the "label"
// field. The eval functions check membership and substring operations against
// the Members slice.
type StringSetValue struct {
	Members []string
}

func (*StringSetValue) valueMarker() {}

// ---- Row interface ----------------------------------------------------------

// Row is an abstract view of a single issue used during predicate evaluation.
// It is the only interface the evaluator calls; the tasks package adapts
// *Issue → Row. internal/query must NOT import tasks (import cycle).
//
// Field returns the typed Value for the named field and reports whether the
// field is set. Unset fields return (nil, false). The Value types are:
//
//   - StringValue   — status, type, assignee, parent, text (virtual)
//   - IntValue      — priority
//   - DateValue     — created, updated, closed (zero T means "not set")
//   - StringSetValue — label (the full label set)
//
// For date fields (created, updated, closed): a present DateValue with a zero
// T is treated as "not set", identical to (nil, false) — every comparison
// returns false.
type Row interface {
	// Field returns the value of the named field and whether it is set.
	Field(name string) (Value, bool)
	// Ready reports whether the issue is open with no open blockers
	// (TASK-STORAGE-SPEC §9).
	Ready() bool
	// Blocked reports whether the issue has at least one open blocker
	// (TASK-STORAGE-SPEC §9).
	Blocked() bool
}

// ---- Predicate interface ---------------------------------------------------

// Predicate is a compiled filter expression that can be evaluated against a
// Row. The always-true predicate (empty expression) always returns true.
type Predicate interface {
	// Match returns true if the Row satisfies the predicate.
	Match(row Row) bool
}

// ---- Compile ---------------------------------------------------------------

// Compile parses the filter expression and returns a Predicate ready to
// evaluate against Row values. An empty or whitespace-only expression returns
// the always-true predicate. A malformed expression returns a *ParseError
// (Pos = byte offset of the first error).
//
// Compile never touches disk, never imports tasks. It is safe to call from
// multiple goroutines.
func Compile(expr string) (Predicate, error) {
	node, err := Parse(expr)
	if err != nil {
		return nil, err
	}
	return &compiledPredicate{node: node}, nil
}

// ---- compiled predicate implementation -------------------------------------

type compiledPredicate struct {
	node Node
}

func (p *compiledPredicate) Match(row Row) bool {
	return evalNode(p.node, row)
}

// evalNode dispatches on node type.
//
// Recursion depth: evalNode mirrors the AST structure built by the parser.
// Parse caps expression nesting at maxExprDepth (256) levels, so any tree that
// passed Parse cannot be deeper than that. No separate depth cap is needed here.
func evalNode(n Node, row Row) bool {
	switch v := n.(type) {
	case *TrueNode:
		return true
	case *BinNode:
		return evalBin(v, row)
	case *NotNode:
		return !evalNode(v.Operand, row)
	case *BareNode:
		return evalBare(v, row)
	case *CmpNode:
		return evalCmp(v, row)
	}
	// Unknown node type — include conservatively.
	return true
}

func evalBin(n *BinNode, row Row) bool {
	switch n.Op {
	case "&&":
		return evalNode(n.Left, row) && evalNode(n.Right, row)
	case "||":
		return evalNode(n.Left, row) || evalNode(n.Right, row)
	}
	return true
}

func evalBare(n *BareNode, row Row) bool {
	switch n.Name {
	case "ready":
		return row.Ready()
	case "blocked":
		return row.Blocked()
	}
	return false
}

// evalCmp evaluates a field comparison CmpNode against the row.
// Per-field semantics follow QUERY-SPEC.md §2.
func evalCmp(n *CmpNode, row Row) bool {
	switch n.Field {
	case "status", "type", "assignee", "creator", "parent":
		return evalStringField(n, row)
	case "label":
		return evalLabelField(n, row)
	case "text":
		return evalTextField(n, row)
	case "priority":
		return evalIntField(n, row)
	case "created", "updated":
		return evalDateField(n, row, false)
	case "closed":
		return evalDateField(n, row, true)
	}
	// Unknown field — include conservatively.
	return true
}

// evalStringField handles status, type, assignee, parent:
//   - == / != are exact and case-sensitive
//   - ~ is case-insensitive substring
func evalStringField(n *CmpNode, row Row) bool {
	sv, ok := n.Value.(*StringValue)
	if !ok {
		return true
	}
	fieldVal, present := row.Field(n.Field)
	var s string
	if present {
		if fv, ok := fieldVal.(*StringValue); ok {
			s = fv.S
		}
	}
	return cmpString(s, n.Op, sv.S)
}

// cmpString applies a string operator.
// ==,!= are exact/case-sensitive; ~ is case-insensitive substring.
func cmpString(a, op, b string) bool {
	switch op {
	case "==":
		return a == b
	case "!=":
		return a != b
	case "~":
		return strings.Contains(strings.ToLower(a), strings.ToLower(b))
	}
	return true
}

// evalLabelField handles the label set field:
//   - label == "x"  → true iff the set contains exactly "x" (membership, case-sensitive)
//   - label != "x"  → negation
//   - label ~ "x"   → true iff some label contains the ci-substring "x"
func evalLabelField(n *CmpNode, row Row) bool {
	sv, ok := n.Value.(*StringValue)
	if !ok {
		return true
	}
	fieldVal, present := row.Field("label")
	var members []string
	if present {
		if ssv, ok := fieldVal.(*StringSetValue); ok {
			members = ssv.Members
		}
	}
	switch n.Op {
	case "==":
		for _, m := range members {
			if m == sv.S {
				return true
			}
		}
		return false
	case "!=":
		for _, m := range members {
			if m == sv.S {
				return false
			}
		}
		return true
	case "~":
		lower := strings.ToLower(sv.S)
		for _, m := range members {
			if strings.Contains(strings.ToLower(m), lower) {
				return true
			}
		}
		return false
	}
	return true
}

// evalTextField handles the "text" virtual field. Only ~ is permitted (the
// parser enforces this at compile time). The Row supplies the pre-concatenated
// searchable text as a *StringValue.
func evalTextField(n *CmpNode, row Row) bool {
	if n.Op != "~" {
		return true // parser should have rejected other ops; be conservative
	}
	sv, ok := n.Value.(*StringValue)
	if !ok {
		return true
	}
	fieldVal, present := row.Field("text")
	if !present {
		return false
	}
	fv, ok := fieldVal.(*StringValue)
	if !ok {
		return true
	}
	return strings.Contains(strings.ToLower(fv.S), strings.ToLower(sv.S))
}

// evalIntField handles priority with all six comparison operators.
func evalIntField(n *CmpNode, row Row) bool {
	iv, ok := n.Value.(*IntValue)
	if !ok {
		return true
	}
	fieldVal, present := row.Field(n.Field)
	var fieldInt int
	if present {
		if fv, ok := fieldVal.(*IntValue); ok {
			fieldInt = fv.N
		}
	}
	return cmpInt(fieldInt, n.Op, iv.N)
}

// cmpInt applies a numeric comparison operator.
func cmpInt(a int, op string, b int) bool {
	switch op {
	case "==":
		return a == b
	case "!=":
		return a != b
	case "<":
		return a < b
	case "<=":
		return a <= b
	case ">":
		return a > b
	case ">=":
		return a >= b
	}
	return true
}

// evalDateField handles created, updated, and closed fields.
//
// Absence / zero semantics (QUERY-SPEC §2):
//   - "closed": if the field is absent or the time is zero, every comparison
//     returns false ("On an issue with no closed timestamp, every closed
//     comparison is false.").
//   - "created" / "updated": if the field is absent or the time is zero, every
//     ordering comparison (==, !=, <, <=, >, >=) returns false. A missing
//     created/updated has no value to satisfy any bound. This is consistent with
//     how the closed field handles absence. (closedIsSpecial=false path)
func evalDateField(n *CmpNode, row Row, closedIsSpecial bool) bool {
	dv, ok := n.Value.(*DateValue)
	if !ok {
		return true
	}
	fieldVal, present := row.Field(n.Field)
	if !present {
		// Absent field: no value can satisfy any comparison → false.
		return false
	}
	fv, ok := fieldVal.(*DateValue)
	if !ok {
		return false
	}
	if fv.T.IsZero() {
		// Zero time is treated as "not set" → false for all comparisons.
		return false
	}
	return cmpTime(fv.T, n.Op, dv.T)
}

// cmpTime applies a chronological operator.
func cmpTime(a time.Time, op string, b time.Time) bool {
	switch op {
	case "==":
		return a.Equal(b)
	case "!=":
		return !a.Equal(b)
	case "<":
		return a.Before(b)
	case "<=":
		return a.Before(b) || a.Equal(b)
	case ">":
		return a.After(b)
	case ">=":
		return a.After(b) || a.Equal(b)
	}
	return true
}
