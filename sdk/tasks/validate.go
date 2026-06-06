package tasks

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Field-constraint constants from TASK-STORAGE-SPEC §4.
const (
	maxTitleLen    = 200
	maxAssigneeLen = 128
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
// Constraints are taken directly from TASK-STORAGE-SPEC §4 + §10.
func validateFields(iss *Issue) error {
	// title: 1-200 chars after trim; single line (no LF); no control characters.
	trimmedTitle := strings.TrimSpace(iss.Title)
	if trimmedTitle == "" {
		return invalid("title", "must not be empty")
	}
	if len([]rune(trimmedTitle)) > maxTitleLen {
		return invalid("title", "must be at most %d characters after trim, got %d", maxTitleLen, len([]rune(trimmedTitle)))
	}
	if strings.ContainsRune(iss.Title, '\n') {
		return invalid("title", "must be a single line (no newline characters)")
	}
	if hasControlChar(iss.Title) {
		return invalid("title", "must not contain control characters")
	}

	if !iss.Status.Valid() {
		return invalid("status", "unknown status %q (want one of %s)", iss.Status, joinStatuses())
	}
	if !iss.Type.Valid() {
		return invalid("type", "unknown type %q (want one of %s)", iss.Type, joinTypes())
	}
	if iss.Priority < PriorityMin || iss.Priority > PriorityMax {
		return invalid("priority", "must be between %d and %d, got %d", PriorityMin, PriorityMax, iss.Priority)
	}
	if iss.IsClosed() && iss.Closed.IsZero() {
		return invalid("closed", "closed issue must have a closed timestamp")
	}

	// assignee: 0-128 chars; single line; no control characters.
	if len([]rune(iss.Assignee)) > maxAssigneeLen {
		return invalid("assignee", "must be at most %d characters, got %d", maxAssigneeLen, len([]rune(iss.Assignee)))
	}
	if strings.ContainsRune(iss.Assignee, '\n') {
		return invalid("assignee", "must be a single line (no newline characters)")
	}
	if hasControlChar(iss.Assignee) {
		return invalid("assignee", "must not contain control characters")
	}

	// labels: 0-64 items; each 1-64 chars matching ^[a-z0-9][a-z0-9:._/-]*$; unique.
	if len(iss.Labels) > maxLabels {
		return invalid("labels", "too many labels: %d (max %d)", len(iss.Labels), maxLabels)
	}
	for _, lbl := range iss.Labels {
		if len([]rune(lbl)) > maxLabelLen {
			return invalid("labels", "label %q exceeds max length of %d", lbl, maxLabelLen)
		}
		if !labelRe.MatchString(lbl) {
			return invalid("labels", "label %q does not match required pattern ^[a-z0-9][a-z0-9:._/-]*$", lbl)
		}
	}

	// blocked_by: 0-256 items.
	if len(iss.BlockedBy) > maxBlockedBy {
		return invalid("blocked_by", "too many blockers: %d (max %d)", len(iss.BlockedBy), maxBlockedBy)
	}

	// related: 0-256 items.
	if len(iss.Related) > maxRelated {
		return invalid("related", "too many related references: %d (max %d)", len(iss.Related), maxRelated)
	}

	if iss.Parent == iss.ID {
		return invalid("parent", "issue cannot be its own parent")
	}
	for _, id := range iss.BlockedBy {
		if id == iss.ID {
			return invalid("blocked_by", "issue cannot block itself")
		}
	}
	if dup := firstDuplicate(iss.BlockedBy); dup != "" {
		return invalid("blocked_by", "duplicate dependency %q", dup)
	}
	if dup := firstDuplicate(iss.Related); dup != "" {
		return invalid("related", "duplicate reference %q", dup)
	}
	return nil
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

func joinStatuses() string {
	parts := make([]string, len(Statuses))
	for i, s := range Statuses {
		parts[i] = string(s)
	}
	return strings.Join(parts, ", ")
}

func joinTypes() string {
	parts := make([]string, len(Types))
	for i, t := range Types {
		parts[i] = string(t)
	}
	return strings.Join(parts, ", ")
}
