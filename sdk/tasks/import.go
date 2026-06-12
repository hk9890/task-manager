package tasks

// import.go — the Import primitive: a validated, direct write of a complete
// issue end-state sourced from an external system (beads, Jira, …).
//
// Unlike Create — which AUTHORS a new issue (store clock, always StatusOpen,
// comments added one call at a time) — Import RECONSTRUCTS an existing issue:
// the caller supplies the final Status, the original Created/Updated/Closed
// timestamps, and the full comment log, and the store writes it verbatim
// (subject to the same validation as Create) in a single locked operation.
//
// Because it is a direct write of the end-state and not a create→update→close
// replay, Import can materialize a *closed* issue with *backdated* timestamps
// and its comments in one atomic step — something the lifecycle methods, which
// stamp s.now() and route closed issues through closeMove, cannot do.

import (
	"strings"
	"time"
)

// ImportComment is one comment attached to an imported issue, preserving its
// original author and timestamp. Body is sanitized and validated exactly like
// a comment added via AddComment. Created defaults to the issue's Created when
// zero.
type ImportComment struct {
	Author  string
	Created time.Time
	Body    string
}

// ImportInput describes a complete, externally-sourced issue to write verbatim.
//
// Edge fields (Parent, BlockedBy, Related) are taskmgr IDs that must already
// exist: Import enforces referential integrity and acyclicity exactly like
// Create. A caller migrating from another system is therefore responsible for
// importing in dependency order and translating foreign IDs to taskmgr IDs.
//
// Zero values fall back to the same defaults as Create (TypeTask,
// PriorityDefault, StatusOpen). Timestamps default forward: an unset Updated
// inherits Created; an unset Created inherits the store clock.
type ImportInput struct {
	// ID, when non-empty, is used verbatim (must carry the store prefix, match
	// the ID grammar, and not already exist). Empty → the store allocates one.
	ID          string
	Title       string
	Description string
	Type        Type
	Priority    *int
	Status      Status
	Assignee    string
	Creator     string
	Labels      []string
	Parent      string
	BlockedBy   []string
	Related     []string
	Created     time.Time // defaults to now() when zero
	Updated     time.Time // defaults to Created when zero
	Closed      time.Time // required when Status == closed; defaults to Updated
	CloseReason string
	Comments    []ImportComment
}

// Import writes a complete externally-sourced issue and its comment log in one
// locked, validated operation, placing it directly in the correct partition
// (closed/ when Status is closed). The whole record is validated — issue
// fields, references, and every comment — before anything touches disk, so a
// malformed record is rejected atomically. See ImportInput.
func (s *Store) Import(in ImportInput) (*Issue, error) {
	var out *Issue
	err := s.withLock(func() error {
		id := strings.TrimSpace(in.ID)
		if id == "" {
			var err error
			if id, err = s.nextID(); err != nil {
				return err
			}
		} else if err := s.validateNewID(id); err != nil {
			return err
		}

		now := s.now()
		created := in.Created
		if created.IsZero() {
			created = now
		}
		updated := in.Updated
		if updated.IsZero() {
			updated = created
		}
		status := in.Status
		if status == "" {
			status = StatusOpen
		}

		iss := &Issue{
			ID:          id,
			Title:       strings.TrimSpace(in.Title),
			Status:      status,
			Type:        in.Type,
			Priority:    PriorityDefault,
			Assignee:    in.Assignee,
			Creator:     strings.TrimSpace(in.Creator),
			Labels:      dedupe(in.Labels),
			Parent:      in.Parent,
			BlockedBy:   dedupe(in.BlockedBy),
			Related:     dedupe(in.Related),
			Created:     created,
			Updated:     updated,
			Description: in.Description,
		}
		if iss.Type == "" {
			iss.Type = TypeTask
		}
		if in.Priority != nil {
			iss.Priority = *in.Priority
		}
		if status == StatusClosed {
			closed := in.Closed
			if closed.IsZero() {
				closed = updated
			}
			iss.Closed = closed
			iss.CloseReason = in.CloseReason
		}

		// Validate the end-state and its references before any write.
		if err := validateFields(iss); err != nil {
			return err
		}
		if err := s.checkRefs(iss); err != nil {
			return err
		}

		// Pre-build and validate every comment so a bad comment rejects the
		// whole import before anything touches disk.
		docs := make([]Comment, 0, len(in.Comments))
		for _, c := range in.Comments {
			cCreated := c.Created
			if cCreated.IsZero() {
				cCreated = created
			}
			doc := Comment{
				ID:      newCommentID(),
				Author:  c.Author,
				Created: cCreated,
				Body:    sanitizeCommentBody(c.Body),
			}
			if err := validateCommentDoc(doc); err != nil {
				return err
			}
			docs = append(docs, doc)
		}

		// Write the issue into the correct partition: closeMove lands a closed
		// issue directly in closed/ (preserving the git-rename history anchor);
		// otherwise it goes to the hot dir.
		if status == StatusClosed {
			if err := s.closeMove(iss); err != nil {
				return err
			}
		} else {
			if err := s.writeIssue(iss); err != nil {
				return err
			}
		}

		// Append the already-validated comment log.
		for _, doc := range docs {
			if err := appendCommentDoc(s.fs, s.commentsPath(id), doc); err != nil {
				return err
			}
		}

		out = iss
		return nil
	})
	return out, err
}
