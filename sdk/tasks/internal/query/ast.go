package query

import "time"

// Node is a node in the parsed AST.
type Node interface {
	nodeMarker()
}

// TrueNode is the always-true predicate produced by an empty expression.
type TrueNode struct{}

func (*TrueNode) nodeMarker() {}

// BinNode is a binary boolean operator: Op is "&&" or "||".
type BinNode struct {
	Op    string // "&&" or "||"
	Left  Node
	Right Node
}

func (*BinNode) nodeMarker() {}

// NotNode is a logical negation of its operand.
type NotNode struct {
	Operand Node
}

func (*NotNode) nodeMarker() {}

// BareNode is a bare boolean predicate: "ready" or "blocked".
type BareNode struct {
	Pos  int    // byte offset of the token in the original expression
	Name string // "ready" or "blocked"
}

func (*BareNode) nodeMarker() {}

// CmpNode is a field comparison: field op value.
type CmpNode struct {
	Pos   int    // byte offset of the field token
	Field string // e.g. "status", "priority"
	Op    string // ==, !=, <, <=, >, >=, ~
	Value Value  // parsed typed value
}

func (*CmpNode) nodeMarker() {}

// Value is the typed right-hand side of a comparison.
type Value interface {
	valueMarker()
}

// StringValue holds a bare or quoted string value.
type StringValue struct {
	S string
}

func (*StringValue) valueMarker() {}

// IntValue holds a decimal integer value (used for priority).
type IntValue struct {
	N int
}

func (*IntValue) valueMarker() {}

// DateValue holds a parsed timestamp (YYYY-MM-DD → midnight UTC, or full RFC3339).
type DateValue struct {
	T time.Time
}

func (*DateValue) valueMarker() {}
