// criteria.go — Typed selection builder that compiles to a canonical filter
// expression (QUERY-SPEC.md §1) fed to the existing engine. This is sugar over
// the one query language, NOT a second engine.
//
// SDK-SPEC §3 (Criteria/LabelMatch/WorkState/Build), §4 (Find/FindPage/FindOptions).
//
// Pure-core: no os, no vfs import. No filesystem access anywhere in this file.
package tasks

import (
	"fmt"
	"strings"
	"time"
)

// LabelMatch controls how the Labels list is interpreted in a Criteria.
type LabelMatch int

const (
	// LabelMatchAll requires every listed label to be present (default).
	LabelMatchAll LabelMatch = iota
	// LabelMatchAny requires at least one listed label to be present.
	LabelMatchAny
)

// WorkState constrains the ready/blocked state of a result.
type WorkState int

const (
	// WorkAny imposes no ready/blocked constraint (default).
	WorkAny WorkState = iota
	// WorkReady limits results to issues satisfying the `ready` predicate.
	WorkReady
	// WorkBlocked limits results to issues satisfying the `blocked` predicate.
	WorkBlocked
)

// Criteria is a typed, composable description of a selection. It compiles to a
// canonical filter expression (QUERY-SPEC.md) that is fed to the existing engine
// — it is a convenience for structured callers, NOT a second selection engine.
//
// The zero value compiles to "" (the always-true predicate).
// LabelMatch defaults to LabelMatchAll (the issue must carry every listed label).
type Criteria struct {
	// Text is matched as text ~ "..." (case-insensitive substring of id+title+description).
	Text string

	// Statuses lists the statuses the issue may have (OR-ed). Any unknown Status
	// value causes Build to return a *ValidationError.
	Statuses []Status

	// Types lists the types the issue may have (OR-ed). Any unknown Type value
	// causes Build to return a *ValidationError.
	Types []Type

	// Labels is a set of labels to match. How they are combined is controlled by
	// LabelMatch (defaults to LabelMatchAll).
	Labels []string

	// LabelMatch controls whether ALL (default) or ANY of the listed labels must
	// be present.
	LabelMatch LabelMatch

	// Assignee restricts to issues with exactly this assignee.
	Assignee string

	// Creator restricts to issues with exactly this creator.
	Creator string

	// Parent, when non-nil, restricts to issues whose parent equals the pointed-to
	// string. A non-nil pointer to "" means "no parent" (parent == "").
	Parent *string

	// Work restricts to ready or blocked issues. WorkAny (the default) adds no
	// constraint.
	Work WorkState

	// PriorityMin, when non-nil, emits priority >= *PriorityMin. A negative bound
	// is rejected with a *ValidationError.
	PriorityMin *int

	// PriorityMax, when non-nil, emits priority <= *PriorityMax. A negative bound
	// is rejected with a *ValidationError.
	PriorityMax *int

	// Date bounds are HALF-OPEN on the instant:
	//   *From → field >= "From"  (inclusive)
	//   *To   → field <  "To"   (exclusive)
	// To cover a whole day D, pass To as the start of the next day.
	CreatedFrom, CreatedTo *time.Time
	UpdatedFrom, UpdatedTo *time.Time
	ClosedFrom, ClosedTo   *time.Time
}

// Build compiles the criteria to a canonical filter expression. It is the single
// owner of value quoting/escaping and precedence. Pure; no filesystem access.
//
// The zero value compiles to "" (the always-true predicate).
// For well-formed input the result always parses (it never yields a *ParseError).
// Invalid input — an unknown Status or Type, or a negative priority bound — is
// reported as a *ValidationError (§6 of SDK-SPEC), naming the offending field.
func (c Criteria) Build() (string, error) {
	// Validate enums and priority bounds before emitting anything.
	for _, s := range c.Statuses {
		if !s.Valid() {
			return "", invalid("statuses", "unknown status %q", string(s))
		}
	}
	for _, tp := range c.Types {
		if !tp.Valid() {
			return "", invalid("types", "unknown type %q", string(tp))
		}
	}
	if c.PriorityMin != nil && *c.PriorityMin < 0 {
		return "", invalid("priority_min", "priority bound must be non-negative, got %d", *c.PriorityMin)
	}
	if c.PriorityMax != nil && *c.PriorityMax < 0 {
		return "", invalid("priority_max", "priority bound must be non-negative, got %d", *c.PriorityMax)
	}

	// Accumulate top-level AND fragments.
	var parts []string

	// text ~ "..."
	if c.Text != "" {
		parts = append(parts, fmt.Sprintf("text ~ %s", quoteVal(c.Text)))
	}

	// statuses: OR-group
	ss := make([]string, len(c.Statuses))
	for i, s := range c.Statuses {
		ss[i] = string(s)
	}
	if g := eqOrGroup("status", ss); g != "" {
		parts = append(parts, g)
	}

	// types: OR-group
	ts := make([]string, len(c.Types))
	for i, tp := range c.Types {
		ts[i] = string(tp)
	}
	if g := eqOrGroup("type", ts); g != "" {
		parts = append(parts, g)
	}

	// labels
	if len(c.Labels) > 0 {
		if c.LabelMatch == LabelMatchAny {
			// OR-group: any label matches
			if g := eqOrGroup("label", c.Labels); g != "" {
				parts = append(parts, g)
			}
		} else {
			// LabelMatchAll (default): AND-group — each label must be present
			for _, l := range c.Labels {
				parts = append(parts, fmt.Sprintf("label == %s", quoteVal(l)))
			}
		}
	}

	// assignee == "..."
	if c.Assignee != "" {
		parts = append(parts, fmt.Sprintf("assignee == %s", quoteVal(c.Assignee)))
	}

	// creator == "..."
	if c.Creator != "" {
		parts = append(parts, fmt.Sprintf("creator == %s", quoteVal(c.Creator)))
	}

	// parent == "..."  (non-nil "" → parent == "")
	if c.Parent != nil {
		parts = append(parts, fmt.Sprintf("parent == %s", quoteVal(*c.Parent)))
	}

	// Work: bare predicates
	switch c.Work {
	case WorkReady:
		parts = append(parts, "ready")
	case WorkBlocked:
		parts = append(parts, "blocked")
	}

	// priority >= n / priority <= n  (bare, no quotes)
	if c.PriorityMin != nil {
		parts = append(parts, fmt.Sprintf("priority >= %d", *c.PriorityMin))
	}
	if c.PriorityMax != nil {
		parts = append(parts, fmt.Sprintf("priority <= %d", *c.PriorityMax))
	}

	// Date bounds — HALF-OPEN: *From → field >= "From" (inclusive), *To → field < "To" (exclusive).
	if c.CreatedFrom != nil {
		parts = append(parts, fmt.Sprintf("created >= %s", quoteVal(formatTimestamp(*c.CreatedFrom))))
	}
	if c.CreatedTo != nil {
		parts = append(parts, fmt.Sprintf("created < %s", quoteVal(formatTimestamp(*c.CreatedTo))))
	}
	if c.UpdatedFrom != nil {
		parts = append(parts, fmt.Sprintf("updated >= %s", quoteVal(formatTimestamp(*c.UpdatedFrom))))
	}
	if c.UpdatedTo != nil {
		parts = append(parts, fmt.Sprintf("updated < %s", quoteVal(formatTimestamp(*c.UpdatedTo))))
	}
	if c.ClosedFrom != nil {
		parts = append(parts, fmt.Sprintf("closed >= %s", quoteVal(formatTimestamp(*c.ClosedFrom))))
	}
	if c.ClosedTo != nil {
		parts = append(parts, fmt.Sprintf("closed < %s", quoteVal(formatTimestamp(*c.ClosedTo))))
	}

	return strings.Join(parts, " && "), nil
}

// quoteVal wraps a string value in double-quotes, escaping `\` → `\\` and
// `"` → `\"` as required by QUERY-SPEC.md §3. This is the ONLY place in the
// SDK that applies value quoting, keeping the rule in one audited location.
func quoteVal(s string) string {
	// Escape backslashes first, then double-quotes.
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// storageTimeLayout is the on-disk storage timestamp layout used across the
// store (TASK-STORAGE-SPEC §6, QUERY-SPEC §3): "YYYY-MM-DDThh:mm:ssZ" (UTC,
// whole seconds). It is the single source of truth shared by formatTimestamp
// and the comment sidecar's parse/format paths.
const storageTimeLayout = "2006-01-02T15:04:05Z"

// formatTimestamp formats t as the storage timestamp form used by
// TASK-STORAGE-SPEC §6 and understood by the query engine (QUERY-SPEC §3):
// "YYYY-MM-DDThh:mm:ssZ" (UTC, whole seconds).
func formatTimestamp(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(storageTimeLayout)
}

// eqOrGroup builds an equality OR-group for one field over vals: "" for no
// values, a bare `field == "v"` for one, and `(field == "a" || ...)` for many.
func eqOrGroup(field string, vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	sub := make([]string, len(vals))
	for i, v := range vals {
		sub[i] = fmt.Sprintf("%s == %s", field, quoteVal(v))
	}
	if len(sub) == 1 {
		return sub[0]
	}
	return "(" + strings.Join(sub, " || ") + ")"
}

// ---------------------------------------------------------------------------
// FindOptions and Store.Find / Store.FindPage
// ---------------------------------------------------------------------------

// FindOptions is the presentation subset of Filter used with a Criteria. The
// selection comes from the Criteria (via Build), not from an Expr. Offset/Limit
// behave exactly as on Filter (SDK-SPEC §3): negatives clamp to 0, Limit 0 = no limit.
type FindOptions struct {
	IncludeClosed bool
	Sort          SortField
	Reverse       bool
	Offset        int
	Limit         int
}

// Find compiles c to a filter expression and calls List with the resulting
// Filter. It is equivalent to List(Filter{Expr: c.Build(), …}).
//
// Cold scope is derived by applying the cold-scope predicate (QUERY-SPEC §5)
// to the built expression — the same detector List uses — so a Criteria and
// its hand-written Expr always scope identically. FindOptions.IncludeClosed is
// the explicit override.
//
// If Criteria.Build fails (unknown Status/Type, or negative priority bound),
// that *ValidationError is returned and no scan runs.
func (s *Store) Find(c Criteria, opt FindOptions) ([]*Issue, error) {
	expr, err := c.Build()
	if err != nil {
		return nil, err
	}
	return s.List(Filter{
		Expr:          expr,
		IncludeClosed: opt.IncludeClosed,
		Sort:          opt.Sort,
		Reverse:       opt.Reverse,
		Offset:        opt.Offset,
		Limit:         opt.Limit,
	})
}

// FindPage compiles c to a filter expression and calls ListPage with the
// resulting Filter. It is equivalent to ListPage(Filter{Expr: c.Build(), …}).
//
// Cold scope and error semantics are the same as Find.
func (s *Store) FindPage(c Criteria, opt FindOptions) (Page, error) {
	expr, err := c.Build()
	if err != nil {
		return Page{}, err
	}
	return s.ListPage(Filter{
		Expr:          expr,
		IncludeClosed: opt.IncludeClosed,
		Sort:          opt.Sort,
		Reverse:       opt.Reverse,
		Offset:        opt.Offset,
		Limit:         opt.Limit,
	})
}
