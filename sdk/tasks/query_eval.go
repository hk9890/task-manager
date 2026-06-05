package tasks

// query_eval.go — lightweight filter-expression evaluator for Store.Query /
// Store.List when Filter.Expr is set.
//
// This is a minimal implementation of the QUERY-SPEC.md grammar sufficient to
// support the hot/cold scope semantics (at-zib.2.4) and the most common
// single-predicate expressions. A full recursive-descent parser backed by
// internal/query is tracked separately; until that lands, this evaluator covers
// the subset required by the acceptance criteria.
//
// Supported syntax:
//
//	status   == "value"   / status   != "value"
//	type     == "value"   / type     != "value"
//	priority == N         / priority <= N / priority >= N / < / >
//	assignee == "value"   / assignee != "value" / assignee ~ "value"
//	parent   == "value"   / parent   != "value"
//	label    == "value"   / label    != "value" / label    ~ "value"
//	text     ~ "value"
//	created / updated / closed  == / != / < / <= / > / >=  "date"
//	ready  /  blocked            (bare predicates)
//	Combinations with && and || (|| is lower precedence than &&).
//
// Empty or whitespace-only expression: always-true (matches every issue in
// scope — QUERY-SPEC.md §1).
//
// Unrecognised tokens or operators: evaluated conservatively (issue included)
// so the caller never silently drops issues due to a parse gap.

import (
	"strconv"
	"strings"
	"time"
)

// evalExpr evaluates a filter expression against an issue. It returns true if
// the issue matches (or if the expression is empty). idx and closedStat are
// required for the "ready" / "blocked" predicates; they may be nil if those
// predicates are not used.
func evalExpr(expr string, iss *Issue, idx map[string]*Issue, closedStat func(string) bool) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return true
	}
	return evalOr(expr, iss, idx, closedStat)
}

// evalOr evaluates an "||"-separated chain (lowest precedence).
func evalOr(expr string, iss *Issue, idx map[string]*Issue, closedStat func(string) bool) bool {
	parts := splitTopLevel(expr, "||")
	if len(parts) == 1 {
		return evalAnd(strings.TrimSpace(parts[0]), iss, idx, closedStat)
	}
	for _, p := range parts {
		if evalAnd(strings.TrimSpace(p), iss, idx, closedStat) {
			return true
		}
	}
	return false
}

// evalAnd evaluates an "&&"-separated chain.
func evalAnd(expr string, iss *Issue, idx map[string]*Issue, closedStat func(string) bool) bool {
	parts := splitTopLevel(expr, "&&")
	for _, p := range parts {
		if !evalUnary(strings.TrimSpace(p), iss, idx, closedStat) {
			return false
		}
	}
	return true
}

// evalUnary handles an optional leading "!" and parenthesised sub-expressions.
func evalUnary(expr string, iss *Issue, idx map[string]*Issue, closedStat func(string) bool) bool {
	if strings.HasPrefix(expr, "!") {
		inner := strings.TrimSpace(expr[1:])
		if strings.HasPrefix(inner, "(") && strings.HasSuffix(inner, ")") {
			inner = strings.TrimSpace(inner[1 : len(inner)-1])
		}
		return !evalPrimary(inner, iss, idx, closedStat)
	}
	if strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") {
		return evalOr(strings.TrimSpace(expr[1:len(expr)-1]), iss, idx, closedStat)
	}
	return evalPrimary(expr, iss, idx, closedStat)
}

// evalPrimary evaluates a single predicate (a comparison or a bare keyword).
func evalPrimary(expr string, iss *Issue, idx map[string]*Issue, closedStat func(string) bool) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return true
	}

	// Bare predicates.
	switch expr {
	case "ready":
		return iss.Status == StatusOpen && len(openBlockers(idx, closedStat, iss)) == 0
	case "blocked":
		return !iss.Status.IsClosed() && len(openBlockers(idx, closedStat, iss)) > 0
	}

	// Parse into (field, operator, rawValue).
	field, op, rawVal, ok := parseComparison(expr)
	if !ok {
		// Unknown predicate — include conservatively.
		return true
	}
	val := stripQuotes(rawVal)

	switch field {
	case "status":
		return cmpStr(string(iss.Status), op, val)
	case "type":
		return cmpStr(string(iss.Type), op, val)
	case "assignee":
		return cmpStr(iss.Assignee, op, val)
	case "parent":
		return cmpStr(iss.Parent, op, val)
	case "priority":
		n, err := strconv.Atoi(val)
		if err != nil {
			return true
		}
		return cmpInt(iss.Priority, op, n)
	case "label":
		return cmpLabel(iss.Labels, op, val)
	case "text":
		if op != "~" {
			return true // only ~ is valid for text
		}
		return matchesText(iss, strings.ToLower(val))
	case "created":
		t := parseAnyDate(val)
		if t.IsZero() {
			return true
		}
		return cmpTime(iss.Created, op, t)
	case "updated":
		t := parseAnyDate(val)
		if t.IsZero() {
			return true
		}
		return cmpTime(iss.Updated, op, t)
	case "closed":
		if iss.Closed.IsZero() {
			// An issue without a closed timestamp: all closed comparisons are false.
			return false
		}
		t := parseAnyDate(val)
		if t.IsZero() {
			return true
		}
		return cmpTime(iss.Closed, op, t)
	}
	// Unknown field — include conservatively.
	return true
}

// parseComparison splits "field op value" into its components.
// Operators are tested longest-first to avoid partial matches.
func parseComparison(expr string) (field, op, value string, ok bool) {
	for _, candidate := range []string{"==", "!=", "<=", ">=", "<", ">", "~"} {
		idx := strings.Index(expr, candidate)
		if idx < 0 {
			continue
		}
		f := strings.TrimSpace(expr[:idx])
		v := strings.TrimSpace(expr[idx+len(candidate):])
		if f == "" || v == "" {
			continue
		}
		return f, candidate, v, true
	}
	return "", "", "", false
}

// splitTopLevel splits expr on op but only at depth 0 (not inside parens or
// double-quoted strings).
func splitTopLevel(expr, op string) []string {
	var parts []string
	depth := 0
	inQuote := false
	start := 0
	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		if ch == '"' {
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
		}
		if depth == 0 && i+len(op) <= len(expr) && expr[i:i+len(op)] == op {
			parts = append(parts, expr[start:i])
			i += len(op) - 1
			start = i + 1
		}
	}
	return append(parts, expr[start:])
}

// stripQuotes removes surrounding double-quotes and un-escapes \\ / \".
func stripQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
		s = strings.ReplaceAll(s, `\"`, `"`)
		s = strings.ReplaceAll(s, `\\`, `\`)
	}
	return s
}

// cmpStr applies a string operator (==, !=, ~).
func cmpStr(a, op, b string) bool {
	switch op {
	case "==":
		return a == b
	case "!=":
		return a != b
	case "~":
		return strings.Contains(strings.ToLower(a), strings.ToLower(b))
	}
	return true // unknown op → include
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

// cmpLabel evaluates a label-set operator against the issue's labels.
// label == "x"  → exact membership
// label != "x"  → not a member
// label ~ "x"   → any label contains x (case-insensitive substring)
func cmpLabel(labels []string, op, val string) bool {
	switch op {
	case "==":
		for _, l := range labels {
			if l == val {
				return true
			}
		}
		return false
	case "!=":
		for _, l := range labels {
			if l == val {
				return false
			}
		}
		return true
	case "~":
		lower := strings.ToLower(val)
		for _, l := range labels {
			if strings.Contains(strings.ToLower(l), lower) {
				return true
			}
		}
		return false
	}
	return true
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

// parseAnyDate parses a date value that is either a full RFC3339 UTC timestamp
// ("2026-01-02T15:04:05Z") or a date-only string ("2026-01-02", interpreted as
// midnight UTC). Returns the zero Time if parsing fails.
func parseAnyDate(s string) time.Time {
	t, err := parseTimestamp(s)
	if err == nil {
		return t
	}
	t, err = parseTimestampDate(s)
	if err == nil {
		return t
	}
	return time.Time{}
}

// parseTimestampDate parses a YYYY-MM-DD date string as midnight UTC.
func parseTimestampDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", strings.TrimSpace(s))
}
