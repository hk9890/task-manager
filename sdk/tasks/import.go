package tasks

// import.go — the Import primitive: a validated, direct write of a complete
// issue end-state sourced from an external system (e.g. Jira, GitHub).
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
	// RunHooks gates this import through the create lifecycle hooks (HOOK-SPEC
	// §9). Default false: bulk loading omits hooks, so re-importing N issues does
	// not fire N gates. When true, the import is treated like an ordinary write —
	// a pre-create denial rejects it (returning *HookDeniedError, nothing
	// written) and post-create hooks notify. Regardless of this flag, a malformed
	// hooks config fails the import closed (fail-closed config, §3.4).
	RunHooks bool
}

// Import writes a complete externally-sourced issue and its comment log in one
// locked, validated operation, placing it directly in the correct partition
// (closed/ when Status is closed). The whole record is validated — issue
// fields, references, and every comment — before anything touches disk, so a
// malformed record is rejected atomically. See ImportInput.
func (s *Store) Import(in ImportInput) (*MutationResult, error) {
	// Validate the hooks config (fail-closed, §3.4) even when not running hooks.
	hs, err := s.hooks()
	if err != nil {
		return nil, err
	}
	var out *Issue
	var preHints []string
	err = s.withLock(func() error {
		id, err := s.resolveID(in.ID)
		if err != nil {
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

		iss := buildIssue(id, issueFields{
			Title:       in.Title,
			Description: in.Description,
			Type:        in.Type,
			Priority:    in.Priority,
			Assignee:    in.Assignee,
			Creator:     in.Creator,
			Labels:      in.Labels,
			Parent:      in.Parent,
			BlockedBy:   in.BlockedBy,
			Related:     in.Related,
		}, status, created, updated)
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

		// An import is a create transition (no prior issue), even when it lands
		// closed (HOOK-SPEC §2.1). Gate it through pre-create when requested.
		if in.RunHooks {
			hints, denial, herr := s.runPre(hs, transCreate.preEvent(), nil, iss, nil)
			if herr != nil {
				return herr
			}
			if denial != nil {
				return denial
			}
			preHints = hints
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
	if err != nil {
		return nil, err
	}
	// Post-create hooks run outside the lock only when hooks were requested
	// (fired == in.RunHooks); their hints/warnings are returned in the
	// MutationResult exactly as for the everyday mutations (HOOK-SPEC §6.2).
	return s.postFinish(hs, in.RunHooks, transCreate, nil, out, preHints), nil
}
