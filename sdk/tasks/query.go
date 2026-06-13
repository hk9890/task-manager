// query.go — Row adapter over *Issue and the ParseError type alias.
//
// This file wires the pure internal/query engine into the store:
//   - type ParseError = query.ParseError  (re-exported for callers)
//   - issueRow adapts *Issue → query.Row  (field mapping per QUERY-SPEC.md §2)
//   - compileExpr compiles an expression, returning the always-true predicate for "".
//
// Store.Query is defined in ready.go (it delegates to List).
// List uses compileExpr + issueRow to apply the expression filter.
//
// import-cycle rule: internal/query must NOT import tasks; tasks imports query.
package tasks

import (
	"strings"

	"github.com/hk9890/task-manager/sdk/tasks/internal/query"
)

// ParseError is the typed error returned when a filter expression cannot be
// parsed. Re-exported as a type alias so callers only need to import tasks.
//
//	var pe *tasks.ParseError
//	if errors.As(err, &pe) { ... }
type ParseError = query.ParseError

// compileExpr compiles the filter expression and returns a Predicate.
// An empty or whitespace-only expression returns the always-true predicate.
// A malformed expression returns a *ParseError (never nil on malformed input).
func compileExpr(expr string) (query.Predicate, error) {
	return query.Compile(expr)
}

// issueRow adapts a *Issue to the query.Row interface so the pure evaluator can
// operate on it without importing the tasks package.
//
// Field mappings (QUERY-SPEC.md §2):
//   - "status"   → *StringValue(string(iss.Status))
//   - "type"     → *StringValue(string(iss.Type))
//   - "priority" → *IntValue(iss.Priority)
//   - "assignee" → *StringValue(iss.Assignee)
//   - "creator"  → *StringValue(iss.Creator)
//   - "parent"   → *StringValue(iss.Parent)
//   - "label"    → *StringSetValue{Members: iss.Labels}
//   - "text"     → *StringValue(lower(id+" "+title+" "+description))
//   - "created"  → *DateValue(iss.Created)
//   - "updated"  → *DateValue(iss.Updated)
//   - "closed"   → *DateValue(iss.Closed)  (zero means not closed)
type issueRow struct {
	iss       *Issue
	isReady   bool
	isBlocked bool
}

// newIssueRow builds an issueRow for iss. ready and blocked are pre-computed
// by the caller (List) using openBlockers so the evaluator doesn't need to
// re-run that logic per-expression-node.
func newIssueRow(iss *Issue, idx map[string]*Issue, closedStat func(string) bool) *issueRow {
	var open []string
	if !iss.Status.IsClosed() {
		open = openBlockers(idx, closedStat, iss)
	}
	ready := iss.Status == StatusOpen && len(open) == 0
	blocked := !iss.Status.IsClosed() && len(open) > 0
	return &issueRow{iss: iss, isReady: ready, isBlocked: blocked}
}

// Field implements query.Row.
func (r *issueRow) Field(name string) (query.Value, bool) {
	iss := r.iss
	switch name {
	case "status":
		return &query.StringValue{S: string(iss.Status)}, true
	case "type":
		return &query.StringValue{S: string(iss.Type)}, true
	case "priority":
		return &query.IntValue{N: iss.Priority}, true
	case "assignee":
		return &query.StringValue{S: iss.Assignee}, true
	case "creator":
		return &query.StringValue{S: iss.Creator}, true
	case "parent":
		return &query.StringValue{S: iss.Parent}, true
	case "label":
		return &query.StringSetValue{Members: iss.Labels}, true
	case "text":
		// Virtual field: case-insensitive concatenation of id, title, description.
		text := strings.ToLower(iss.ID + " " + iss.Title + " " + iss.Description)
		return &query.StringValue{S: text}, true
	case "created":
		return &query.DateValue{T: iss.Created}, true
	case "updated":
		return &query.DateValue{T: iss.Updated}, true
	case "closed":
		// Zero time means not closed (evalDateField treats zero as absent).
		return &query.DateValue{T: iss.Closed}, true
	}
	return nil, false
}

// Ready implements query.Row.
func (r *issueRow) Ready() bool { return r.isReady }

// Blocked implements query.Row.
func (r *issueRow) Blocked() bool { return r.isBlocked }
