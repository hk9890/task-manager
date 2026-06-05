package query

// parse.go — recursive-descent parser for the QUERY-SPEC.md §1 grammar.
//
// Grammar (reproduced):
//
//	expr     = or_expr
//	or_expr  = and_expr { "||" and_expr }
//	and_expr = unary    { "&&" unary }
//	unary    = [ "!" ] primary
//	primary  = "(" expr ")" | predicate
//	predicate = comparison | bool_field
//	bool_field = "ready" | "blocked"
//	comparison = field op value
//
// Precedence (tightest to loosest): ! → && → ||; all binary operators are
// left-associative.
//
// The empty expression returns TrueNode (always-true predicate).
//
// All §4 error classes are detected at parse time and returned as *ParseError
// with Pos = byte offset of the offending token.

import (
	"strconv"
	"strings"
	"time"
)

// ---- field metadata ---------------------------------------------------------

type fieldKind int

const (
	fieldEnum    fieldKind = iota // status, type
	fieldInt                      // priority
	fieldString                   // assignee, parent, label
	fieldStrSet                   // label (same ops as string)
	fieldText                     // text (only ~)
	fieldDate                     // created, updated, closed
	fieldBool                     // ready, blocked (bare only)
)

type fieldInfo struct {
	kind     fieldKind
	allowOps []string
}

// knownFields maps each field name to its metadata.
var knownFields = map[string]fieldInfo{
	"status":   {kind: fieldEnum, allowOps: []string{"==", "!="}},
	"type":     {kind: fieldEnum, allowOps: []string{"==", "!="}},
	"priority": {kind: fieldInt, allowOps: []string{"==", "!=", "<", "<=", ">", ">="}},
	"assignee": {kind: fieldString, allowOps: []string{"==", "!=", "~"}},
	"parent":   {kind: fieldString, allowOps: []string{"==", "!="}},
	"label":    {kind: fieldStrSet, allowOps: []string{"==", "!=", "~"}},
	"text":     {kind: fieldText, allowOps: []string{"~"}},
	"created":  {kind: fieldDate, allowOps: []string{"==", "!=", "<", "<=", ">", ">="}},
	"updated":  {kind: fieldDate, allowOps: []string{"==", "!=", "<", "<=", ">", ">="}},
	"closed":   {kind: fieldDate, allowOps: []string{"==", "!=", "<", "<=", ">", ">="}},
}

// knownBareFields are the bare boolean predicates.
var knownBareFields = map[string]bool{
	"ready":   true,
	"blocked": true,
}

// validStatusValues are the accepted enum tokens for status.
var validStatusValues = map[string]bool{
	"open":        true,
	"in_progress": true,
	"blocked":     true,
	"closed":      true,
}

// validTypeValues are the accepted enum tokens for type.
var validTypeValues = map[string]bool{
	"task":    true,
	"bug":     true,
	"feature": true,
	"epic":    true,
	"chore":   true,
}

// ---- parser -----------------------------------------------------------------

type parser struct {
	tokens []token
	pos    int // index into tokens
	src    string
}

// Parse parses the filter expression and returns the AST root.
// An empty or whitespace-only expression returns *TrueNode.
// Any syntax, field, operator, or value error returns *ParseError.
func Parse(expr string) (Node, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return &TrueNode{}, nil
	}

	toks, err := tokenize(expr)
	if err != nil {
		return nil, err
	}

	p := &parser{tokens: toks, src: expr}
	n, perr := p.parseExpr()
	if perr != nil {
		return nil, perr
	}

	// After a complete expression the next token must be EOF.
	if p.peek().Kind != tokEOF {
		tok := p.peek()
		return nil, parseErr(tok.Pos, "unexpected token %q after expression", tok.Val)
	}
	return n, nil
}

// peek returns the current token without consuming it.
func (p *parser) peek() token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return token{Kind: tokEOF, Pos: len(p.src)}
}

// consume returns the current token and advances the cursor.
func (p *parser) consume() token {
	t := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return t
}

// parseExpr = or_expr
func (p *parser) parseExpr() (Node, *ParseError) {
	return p.parseOrExpr()
}

// parseOrExpr = and_expr { "||" and_expr }
func (p *parser) parseOrExpr() (Node, *ParseError) {
	left, err := p.parseAndExpr()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == tokOr {
		p.consume() // consume "||"
		right, err := p.parseAndExpr()
		if err != nil {
			return nil, err
		}
		left = &BinNode{Op: "||", Left: left, Right: right}
	}
	return left, nil
}

// parseAndExpr = unary { "&&" unary }
func (p *parser) parseAndExpr() (Node, *ParseError) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == tokAnd {
		p.consume() // consume "&&"
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &BinNode{Op: "&&", Left: left, Right: right}
	}
	return left, nil
}

// parseUnary = [ "!" ] primary
func (p *parser) parseUnary() (Node, *ParseError) {
	if p.peek().Kind == tokNot {
		p.consume() // consume "!"
		operand, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &NotNode{Operand: operand}, nil
	}
	return p.parsePrimary()
}

// parsePrimary = "(" expr ")" | predicate
func (p *parser) parsePrimary() (Node, *ParseError) {
	if p.peek().Kind == tokLParen {
		p.consume() // consume "("
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek().Kind != tokRParen {
			tok := p.peek()
			return nil, parseErr(tok.Pos, "expected ')' but got %q", tok.Val)
		}
		p.consume() // consume ")"
		return inner, nil
	}
	return p.parsePredicate()
}

// parsePredicate = comparison | bool_field
func (p *parser) parsePredicate() (Node, *ParseError) {
	tok := p.peek()
	if tok.Kind == tokEOF {
		return nil, parseErr(tok.Pos, "unexpected end of expression")
	}
	if tok.Kind != tokWord {
		return nil, parseErr(tok.Pos, "expected field name or predicate, got %q", tok.Val)
	}

	name := tok.Val

	// Check for bare boolean predicates: ready, blocked.
	if knownBareFields[name] {
		p.consume()
		return &BareNode{Pos: tok.Pos, Name: name}, nil
	}

	// Must be a comparison field.
	fi, ok := knownFields[name]
	if !ok {
		return nil, parseErr(tok.Pos, "unknown field or predicate %q", name)
	}
	p.consume() // consume field name

	// Next must be an operator.
	opTok := p.peek()
	op, perr := p.parseOp()
	if perr != nil {
		return nil, perr
	}

	// Validate operator is permitted for this field.
	if !opAllowed(fi.allowOps, op) {
		return nil, parseErr(opTok.Pos, "operator %q is not permitted for field %q", op, name)
	}

	// Parse and validate the value.
	val, perr := p.parseValue(name, fi, op)
	if perr != nil {
		return nil, perr
	}

	return &CmpNode{Pos: tok.Pos, Field: name, Op: op, Value: val}, nil
}

// parseOp reads the next operator token and returns its string form.
func (p *parser) parseOp() (string, *ParseError) {
	tok := p.peek()
	switch tok.Kind {
	case tokEqEq:
		p.consume()
		return "==", nil
	case tokNotEq:
		p.consume()
		return "!=", nil
	case tokLT:
		p.consume()
		return "<", nil
	case tokLE:
		p.consume()
		return "<=", nil
	case tokGT:
		p.consume()
		return ">", nil
	case tokGE:
		p.consume()
		return ">=", nil
	case tokTilde:
		p.consume()
		return "~", nil
	default:
		return "", parseErr(tok.Pos, "expected operator, got %q", tok.Val)
	}
}

// parseValue reads and validates the value token for the given field.
func (p *parser) parseValue(field string, fi fieldInfo, op string) (Value, *ParseError) {
	tok := p.peek()

	// Collect the raw string from the token.
	switch tok.Kind {
	case tokString, tokWord, tokNumber:
		// acceptable
	default:
		return nil, parseErr(tok.Pos, "expected value for field %q, got %q", field, tok.Val)
	}
	p.consume()

	switch fi.kind {
	case fieldInt:
		return p.parseIntValue(tok)
	case fieldDate:
		return p.parseDateValue(tok)
	case fieldEnum:
		return p.parseEnumValue(field, tok)
	case fieldString, fieldStrSet:
		// Any string or bareword is fine (no further validation).
		return &StringValue{S: tok.Val}, nil
	case fieldText:
		// Only ~ is allowed (enforced above); any string is fine as pattern.
		return &StringValue{S: tok.Val}, nil
	}
	return &StringValue{S: tok.Val}, nil
}

func (p *parser) parseIntValue(tok token) (Value, *ParseError) {
	if tok.Kind != tokNumber {
		return nil, parseErr(tok.Pos, "priority requires an integer value, got %q", tok.Val)
	}
	n, err := strconv.Atoi(tok.Val)
	if err != nil {
		return nil, parseErr(tok.Pos, "invalid integer %q", tok.Val)
	}
	if n < 0 || n > 4 {
		return nil, parseErr(tok.Pos, "priority must be 0–4, got %d", n)
	}
	return &IntValue{N: n}, nil
}

func (p *parser) parseDateValue(tok token) (Value, *ParseError) {
	s := tok.Val
	// Try full RFC3339 first, then date-only.
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse("2006-01-02", s)
		if err != nil {
			return nil, parseErr(tok.Pos, "invalid date %q (want YYYY-MM-DD or YYYY-MM-DDThh:mm:ssZ)", s)
		}
		// Date-only: interpret as midnight UTC.
		t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
	return &DateValue{T: t}, nil
}

func (p *parser) parseEnumValue(field string, tok token) (Value, *ParseError) {
	s := tok.Val
	switch field {
	case "status":
		if !validStatusValues[s] {
			return nil, parseErr(tok.Pos, "unknown status %q (want: open, in_progress, blocked, closed)", s)
		}
	case "type":
		if !validTypeValues[s] {
			return nil, parseErr(tok.Pos, "unknown type %q (want: task, bug, feature, epic, chore)", s)
		}
	}
	return &StringValue{S: s}, nil
}

// opAllowed reports whether op is in the allowed list.
func opAllowed(allowed []string, op string) bool {
	for _, a := range allowed {
		if a == op {
			return true
		}
	}
	return false
}

// ---- §5 helpers (ReferencesClosedWork for Store.List) ----------------------

// ReferencesClosedWork reports whether the expression refers to closed work
// that requires the cold partition to be scanned. It delegates to a proper
// parse so it is accurate (no heuristic).
func ReferencesClosedWork(expr string) bool {
	n, err := Parse(expr)
	if err != nil || n == nil {
		return false
	}
	return nodeReferencesClosedWork(n)
}

func nodeReferencesClosedWork(n Node) bool {
	switch v := n.(type) {
	case *TrueNode:
		return false
	case *BareNode:
		return false
	case *NotNode:
		return nodeReferencesClosedWork(v.Operand)
	case *BinNode:
		return nodeReferencesClosedWork(v.Left) || nodeReferencesClosedWork(v.Right)
	case *CmpNode:
		// status == "closed" (or != "closed")
		if v.Field == "status" {
			if sv, ok := v.Value.(*StringValue); ok && sv.S == "closed" {
				return true
			}
		}
		// any comparison against the "closed" date field
		if v.Field == "closed" {
			return true
		}
		return false
	}
	return false
}
