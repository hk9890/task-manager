// hookpayload.go — the engine-owned serializer for the JSON written to a hook's
// stdin (HOOK-SPEC §5). Hooks fire inside the Store, so the engine (not the CLI)
// owns this payload. Its issue object must stay byte-identical in SHAPE to the
// CLI's issueDTO (CLI-SPEC §6) — a contract the two keep in lockstep — but the
// CLI's rendering DTO is deliberately not reachable from here (the CLI imports
// the SDK, never the reverse). Pure JSON, no filesystem: L1-testable.
package tasks

import (
	"encoding/json"
	"time"
)

// hookPayloadSchema is the stdin payload schema version (HOOK-SPEC §5). Adding a
// field is additive; a removal/repurpose bumps this and is a breaking change.
const hookPayloadSchema = 1

// hookIssue mirrors the stable CLI issueDTO (CLI-SPEC §6) plus the description
// body, with empty optional fields omitted exactly as in the CLI's JSON output.
// Field order matches the CLI (issueDTO fields, then description) so the
// serialized shape is identical.
type hookIssue struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	Type        string     `json:"type"`
	Priority    int        `json:"priority"`
	Assignee    string     `json:"assignee,omitempty"`
	Creator     string     `json:"creator,omitempty"`
	Labels      []string   `json:"labels,omitempty"`
	Parent      string     `json:"parent,omitempty"`
	BlockedBy   []string   `json:"blocked_by,omitempty"`
	Related     []string   `json:"related,omitempty"`
	Created     time.Time  `json:"created"`
	Updated     time.Time  `json:"updated"`
	Closed      *time.Time `json:"closed,omitempty"`
	CloseReason string     `json:"close_reason,omitempty"`
	Description string     `json:"description,omitempty"`
}

// toHookIssue converts an *Issue to the hook issue object, or nil (→ JSON null)
// for a nil issue. Mirrors cmd's toIssueDTO: a zero Closed time is omitted
// rather than serialized.
func toHookIssue(i *Issue) *hookIssue {
	if i == nil {
		return nil
	}
	h := &hookIssue{
		ID: i.ID, Title: i.Title, Status: string(i.Status), Type: string(i.Type),
		Priority: i.Priority, Assignee: i.Assignee, Creator: i.Creator, Labels: i.Labels,
		Parent: i.Parent, BlockedBy: i.BlockedBy, Related: i.Related,
		Created: i.Created, Updated: i.Updated, CloseReason: i.CloseReason,
		Description: i.Description,
	}
	if !i.Closed.IsZero() {
		c := i.Closed
		h.Closed = &c
	}
	return h
}

// hookPayload is the {schema,event,issue_id,old,new} envelope (HOOK-SPEC §5).
// Old is a pointer with no omitempty so it serializes as an explicit null for a
// create rather than being dropped.
type hookPayload struct {
	Schema  int        `json:"schema"`
	Event   string     `json:"event"`
	IssueID string     `json:"issue_id"`
	Old     *hookIssue `json:"old"`
	New     *hookIssue `json:"new"`
}

// buildHookPayload serializes the stdin payload for one hook invocation. old is
// nil for a create; new is always present, and issue_id equals new.id (§5).
func buildHookPayload(event string, old, newIss *Issue) ([]byte, error) {
	p := hookPayload{
		Schema:  hookPayloadSchema,
		Event:   event,
		IssueID: newIss.ID,
		Old:     toHookIssue(old),
		New:     toHookIssue(newIss),
	}
	return json.Marshal(p)
}
