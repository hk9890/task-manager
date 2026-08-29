// Copyright 2026 Hans Kohlreiter
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package tasks

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Field-constraint constants from TASK-STORAGE-SPEC §4.
const (
	maxTitleLen    = 200
	maxAssigneeLen = 128
	maxCreatorLen  = 128
	maxLabelLen    = 64
	maxLabels      = 64
	maxBlockedBy   = 256
	maxRelated     = 256
)

// labelRe is the per-label pattern from §4: ^[a-z0-9][a-z0-9:._/-]*$
// A single-char label satisfies this because the second part is * (zero or more).
var labelRe = regexp.MustCompile(`^[a-z0-9][a-z0-9:._/\-]*$`)

// ValidationError describes why an issue was rejected. It carries the field so
// callers (and the CLI) can give precise feedback.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func invalid(field, format string, args ...any) *ValidationError {
	return &ValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// hasControlChar reports whether s contains any Unicode control character
// (category Cc) including NUL, LF, CR, TAB, etc.
func hasControlChar(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// validateFields checks the self-contained invariants of a single issue:
// field lengths, patterns, known enums, priority in range, and a sane
// dependency shape (no self-edges, no duplicate IDs). Referential checks
// (do the linked IDs exist? do dependencies form a cycle?) are the store's
// responsibility because they need the whole graph.
//
// Constraints are taken directly from TASK-STORAGE-SPEC §4 + §10. It reports
// the first violation; fieldViolations reports every one.
func validateFields(iss *Issue) error {
	if v := fieldViolations(iss); len(v) > 0 {
		return v[0]
	}
	return nil
}

// fieldViolations collects every field violation of iss, in the order §4 states
// the constraints — so fieldViolations(iss)[0] is the error validateFields has
// always returned.
//
// Every violation is collected rather than only the first because the write path
// has to tell a violation it *introduces* from one it *found* (validateWrite).
// Stopping at the first would let a write grandfather that one while a violation
// it really did make went unseen behind it.
func fieldViolations(iss *Issue) []*ValidationError {
	var out []*ValidationError
	add := func(field, format string, args ...any) {
		out = append(out, invalid(field, format, args...))
	}

	// title: 1-200 chars after trim; single line (no LF); no control characters.
	trimmedTitle := strings.TrimSpace(iss.Title)
	if trimmedTitle == "" {
		add("title", "must not be empty")
	}
	if len([]rune(trimmedTitle)) > maxTitleLen {
		add("title", "must be at most %d characters after trim, got %d", maxTitleLen, len([]rune(trimmedTitle)))
	}
	if strings.ContainsRune(iss.Title, '\n') {
		add("title", "must be a single line (no newline characters)")
	}
	if hasControlChar(iss.Title) {
		add("title", "must not contain control characters")
	}

	if !iss.Status.Valid() {
		add("status", "unknown status %q (want one of %s)", iss.Status, joinEnum(Statuses))
	}
	if !iss.Type.Valid() {
		add("type", "unknown type %q (want one of %s)", iss.Type, joinEnum(Types))
	}
	if iss.Priority < PriorityMin || iss.Priority > PriorityMax {
		add("priority", "must be between %d and %d, got %d", PriorityMin, PriorityMax, iss.Priority)
	}
	if iss.IsClosed() && iss.Closed.IsZero() {
		add("closed", "closed issue must have a closed timestamp")
	}

	// assignee: 0-128 chars; single line; no control characters.
	if len([]rune(iss.Assignee)) > maxAssigneeLen {
		add("assignee", "must be at most %d characters, got %d", maxAssigneeLen, len([]rune(iss.Assignee)))
	}
	if strings.ContainsRune(iss.Assignee, '\n') {
		add("assignee", "must be a single line (no newline characters)")
	}
	if hasControlChar(iss.Assignee) {
		add("assignee", "must not contain control characters")
	}

	// creator: 0-128 chars; single line; no control characters.
	if len([]rune(iss.Creator)) > maxCreatorLen {
		add("creator", "must be at most %d characters, got %d", maxCreatorLen, len([]rune(iss.Creator)))
	}
	if strings.ContainsRune(iss.Creator, '\n') {
		add("creator", "must be a single line (no newline characters)")
	}
	if hasControlChar(iss.Creator) {
		add("creator", "must not contain control characters")
	}

	// labels: 0-64 items; each 1-64 chars matching ^[a-z0-9][a-z0-9:._/-]*$; unique.
	if len(iss.Labels) > maxLabels {
		add("labels", "too many labels: %d (max %d)", len(iss.Labels), maxLabels)
	}
	for _, lbl := range iss.Labels {
		if len([]rune(lbl)) > maxLabelLen {
			add("labels", "label %q exceeds max length of %d", lbl, maxLabelLen)
		}
		if !labelRe.MatchString(lbl) {
			add("labels", "label %q does not match required pattern ^[a-z0-9][a-z0-9:._/-]*$", lbl)
		}
	}

	// blocked_by: 0-256 items.
	if len(iss.BlockedBy) > maxBlockedBy {
		add("blocked_by", "too many blockers: %d (max %d)", len(iss.BlockedBy), maxBlockedBy)
	}

	// related: 0-256 items.
	if len(iss.Related) > maxRelated {
		add("related", "too many related references: %d (max %d)", len(iss.Related), maxRelated)
	}

	if iss.Parent == iss.ID {
		add("parent", "issue cannot be its own parent")
	}
	for _, id := range iss.BlockedBy {
		if id == iss.ID {
			add("blocked_by", "issue cannot block itself")
			break
		}
	}
	if dup := firstDuplicate(iss.BlockedBy); dup != "" {
		add("blocked_by", "duplicate dependency %q", dup)
	}
	if dup := firstDuplicate(iss.Related); dup != "" {
		add("related", "duplicate reference %q", dup)
	}
	return out
}

// fieldUnchanged reports whether prev and next carry identical inputs for the
// constraint named field — every value that constraint reads, not only the field
// it is named after. When they are identical, a violation of it in next is the
// one prev already had rather than one this write made, which is what
// validateWrite needs to know.
//
// A field this function does not model is reported changed: a constraint added
// later must fail closed here rather than be grandfathered by default.
func fieldUnchanged(field string, prev, next *Issue) bool {
	switch field {
	case "title":
		return prev.Title == next.Title
	case "status":
		return prev.Status == next.Status
	case "type":
		return prev.Type == next.Type
	case "priority":
		return prev.Priority == next.Priority
	case "closed":
		return prev.Status == next.Status && prev.Closed.Equal(next.Closed)
	case "assignee":
		return prev.Assignee == next.Assignee
	case "creator":
		return prev.Creator == next.Creator
	case "labels":
		return slices.Equal(prev.Labels, next.Labels)
	case "blocked_by":
		return prev.ID == next.ID && slices.Equal(prev.BlockedBy, next.BlockedBy)
	case "related":
		return slices.Equal(prev.Related, next.Related)
	case "parent":
		return prev.ID == next.ID && prev.Parent == next.Parent
	}
	return false
}

func firstDuplicate(ids []string) string {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			return id
		}
		seen[id] = struct{}{}
	}
	return ""
}

func joinEnum[T ~string](vals []T) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = string(v)
	}
	return strings.Join(parts, ", ")
}
