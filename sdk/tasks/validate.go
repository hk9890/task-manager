package tasks

import (
	"fmt"
	"strings"
)

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

// validateFields checks the self-contained invariants of a single issue:
// non-empty title, known enums, priority in range, and a sane dependency shape
// (no self-edges, no duplicate IDs). Referential checks (do the linked IDs
// exist? do dependencies form a cycle?) are the store's responsibility because
// they need the whole graph.
func validateFields(iss *Issue) error {
	if strings.TrimSpace(iss.Title) == "" {
		return invalid("title", "must not be empty")
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
