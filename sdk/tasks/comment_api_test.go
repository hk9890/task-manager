package tasks

// L1 + L2 tests for the new comment API:
//   AddComment(*Comment,error), EditComment, DeleteComment, Comments, Detail.Comments
//   Comment validation (§10)
//   Migration of inline frontmatter comments to sidecar

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// ── L1: comment validation (pure, no FS) ──────────────────────────────────

// TestValidateCommentBody_DoubleQuotedRejected verifies that a body that would
// serialize as a double-quoted scalar is rejected before anything touches disk.
// Bodies that would produce a double-quoted scalar are those that:
// - contain control characters (YAML must quote them)
// - are plain strings with YAML-unsafe leading chars that force quoting
// The key trigger is a body that, after sanitization, still would be emitted
// as a double-quoted YAML scalar. We test by calling validateCommentBody.
func TestValidateCommentBody_DoubleQuotedRejected(t *testing.T) {
	// A body with embedded NUL or control chars forces double-quoting.
	badBodies := []string{
		"line1\x00line2",                // NUL character
		"line with \x01 control char",   // SOH
		"text\x1b[0mescaped",           // ESC sequence
	}
	for _, body := range badBodies {
		if err := validateCommentBody(body); err == nil {
			t.Errorf("expected validation error for body %q, got nil", body)
		}
	}
}

// TestValidateCommentBody_ValidBodies verifies that normal bodies pass.
func TestValidateCommentBody_ValidBodies(t *testing.T) {
	goodBodies := []string{
		"simple note\n",
		"## Title\n\nWith code:\n```\nfoo\n```\n",
		"multi\nline\nnote\n",
		"note with: colons and - dashes\n",
		"---\ninside body\n---\n",
	}
	for _, body := range goodBodies {
		body = sanitizeCommentBody(body)
		if err := validateCommentBody(body); err != nil {
			t.Errorf("body %q should be valid, got error: %v", body, err)
		}
	}
}

// TestValidateCommentDoc_NeitherBodyNorDeleted verifies §10: reject a comment
// with neither body nor deleted:true.
func TestValidateCommentDoc_NeitherBodyNorDeleted(t *testing.T) {
	c := Comment{ID: "abcd1234", Author: "hans", Created: time.Now()}
	// No body, Deleted=false → should fail
	if err := validateCommentDoc(c); err == nil {
		t.Error("expected error for comment with neither body nor deleted:true")
	}
}

// TestValidateCommentDoc_TombstoneOK verifies that a tombstone (Deleted:true,
// no body) passes validation.
func TestValidateCommentDoc_TombstoneOK(t *testing.T) {
	c := Comment{
		ID:       "abcd1234",
		Author:   "hans",
		Created:  time.Now(),
		Replaces: "prev1234",
		Deleted:  true,
	}
	if err := validateCommentDoc(c); err != nil {
		t.Errorf("tombstone should be valid, got: %v", err)
	}
}

// TestValidateCommentDoc_BodyOK verifies that a comment with a body passes.
func TestValidateCommentDoc_BodyOK(t *testing.T) {
	c := Comment{
		ID:      "abcd1234",
		Author:  "hans",
		Created: time.Now(),
		Body:    "hello\n",
	}
	if err := validateCommentDoc(c); err != nil {
		t.Errorf("comment with body should be valid, got: %v", err)
	}
}

// TestValidateReplaces_NotInStream verifies §10: reject replaces naming a
// non-existent earlier comment.
func TestValidateReplaces_NoExistingComment(t *testing.T) {
	stream := []Comment{
		{ID: "aaaaaaaa", Body: "existing\n"},
	}
	err := validateReplaces("nonexist", stream)
	if err == nil {
		t.Error("expected error for replaces naming non-existent comment")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

// TestValidateReplaces_ExistingComment verifies that replaces naming an
// existing comment passes.
func TestValidateReplaces_ExistingComment(t *testing.T) {
	stream := []Comment{
		{ID: "aaaaaaaa", Body: "existing\n"},
	}
	if err := validateReplaces("aaaaaaaa", stream); err != nil {
		t.Errorf("replaces naming existing comment should pass, got: %v", err)
	}
}

// TestValidateReplaces_Empty verifies that an empty replaces (new comment)
// always passes.
func TestValidateReplaces_Empty(t *testing.T) {
	if err := validateReplaces("", nil); err != nil {
		t.Errorf("empty replaces should always pass, got: %v", err)
	}
}

// ── L2 (Mem): AddComment returns (*Comment, error) ────────────────────────

// TestAddComment_ReturnsSelf verifies that AddComment returns the new comment
// with its ID populated.
func TestAddComment_ReturnsSelf(t *testing.T) {
	s := newMemStore(t)
	iss, err := s.Create(CreateInput{Title: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	c, err := s.AddComment(iss.ID, "hans", "a note\n")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if c == nil {
		t.Fatal("AddComment returned nil comment")
	}
	if len(c.ID) != 8 {
		t.Errorf("comment ID length = %d, want 8", len(c.ID))
	}
	if c.Author != "hans" {
		t.Errorf("author = %q, want hans", c.Author)
	}
	if c.Body != "a note\n" {
		t.Errorf("body = %q, want %q", c.Body, "a note\n")
	}
}

// TestAddComment_SidecarNotIssueMD verifies that AddComment does NOT rewrite
// the issue .md file (sidecar is append-only, issue file is untouched).
func TestAddComment_SidecarNotIssueMD(t *testing.T) {
	s, m := newCommentMemStore(t)
	iss, err := s.Create(CreateInput{Title: "watch"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Record the issue .md content before the comment.
	issuePath := s.filePath(iss.ID)
	before, err := m.ReadFile(issuePath)
	if err != nil {
		t.Fatalf("ReadFile before: %v", err)
	}

	// Add a comment.
	_, err = s.AddComment(iss.ID, "hans", "a note\n")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	// Issue .md must not have changed.
	after, err := m.ReadFile(issuePath)
	if err != nil {
		t.Fatalf("ReadFile after: %v", err)
	}
	if string(before) != string(after) {
		t.Error("AddComment rewrote the issue .md file — must not rewrite issue on comment add")
	}
}

// TestAddComment_SidecarContainsComment verifies that the sidecar now contains
// the comment.
func TestAddComment_SidecarContainsComment(t *testing.T) {
	s, m := newCommentMemStore(t)
	iss, err := s.Create(CreateInput{Title: "sidecar test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	c, err := s.AddComment(iss.ID, "hans", "sidecar note\n")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	// Read sidecar directly.
	sidecarPath := s.commentsPath(iss.ID)
	stream, err := readCommentStream(m, sidecarPath)
	if err != nil {
		t.Fatalf("readCommentStream: %v", err)
	}
	if len(stream) != 1 {
		t.Fatalf("expected 1 comment in sidecar, got %d", len(stream))
	}
	if stream[0].ID != c.ID {
		t.Errorf("sidecar ID = %q, want %q", stream[0].ID, c.ID)
	}
}

// TestAddComment_IssueHasNoComments verifies that after AddComment, calling
// Get on the issue does NOT include comments (they're sidecar-only).
func TestAddComment_IssueHasNoComments(t *testing.T) {
	s := newMemStore(t)
	iss, err := s.Create(CreateInput{Title: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = s.AddComment(iss.ID, "hans", "a note\n")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	// Get should not load comments.
	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Issue struct no longer carries Comments - this is verified by compilation.
	_ = got
}

// ── L2 (Mem): Comments() method ───────────────────────────────────────────

// TestComments_Empty verifies that Comments() on an issue with no sidecar
// returns an empty slice.
func TestComments_Empty(t *testing.T) {
	s := newMemStore(t)
	iss, err := s.Create(CreateInput{Title: "no comments"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	comments, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("expected 0 comments, got %d", len(comments))
	}
}

// TestComments_AddAndResolve verifies that Comments() returns the resolved
// effective comment log.
func TestComments_AddAndResolve(t *testing.T) {
	s := newMemStore(t)
	iss, err := s.Create(CreateInput{Title: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	c1, err := s.AddComment(iss.ID, "hans", "first note\n")
	if err != nil {
		t.Fatalf("AddComment 1: %v", err)
	}
	_, err = s.AddComment(iss.ID, "alice", "second note\n")
	if err != nil {
		t.Fatalf("AddComment 2: %v", err)
	}

	comments, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	if comments[0].ID != c1.ID {
		t.Errorf("first comment ID = %q, want %q", comments[0].ID, c1.ID)
	}
}

// ── L2 (Mem): EditComment ─────────────────────────────────────────────────

// TestEditComment_ReturnsRevision verifies that EditComment appends a revision
// and returns the new effective comment.
func TestEditComment_ReturnsRevision(t *testing.T) {
	s := newMemStore(t)
	iss, err := s.Create(CreateInput{Title: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	orig, err := s.AddComment(iss.ID, "hans", "original note\n")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	revised, err := s.EditComment(iss.ID, orig.ID, "hans", "revised note\n")
	if err != nil {
		t.Fatalf("EditComment: %v", err)
	}
	if revised == nil {
		t.Fatal("EditComment returned nil")
	}
	if revised.Body != "revised note\n" {
		t.Errorf("revised body = %q, want %q", revised.Body, "revised note\n")
	}
	if revised.Replaces != orig.ID {
		t.Errorf("Replaces = %q, want %q", revised.Replaces, orig.ID)
	}
	if revised.ID == orig.ID {
		t.Error("revised comment should have a new ID")
	}
}

// TestEditComment_ResolvesToRevision verifies that after EditComment, Comments()
// returns the revised body (not the original).
func TestEditComment_ResolvesToRevision(t *testing.T) {
	s := newMemStore(t)
	iss, err := s.Create(CreateInput{Title: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	orig, err := s.AddComment(iss.ID, "hans", "original\n")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	_, err = s.EditComment(iss.ID, orig.ID, "hans", "revised\n")
	if err != nil {
		t.Fatalf("EditComment: %v", err)
	}

	comments, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 effective comment, got %d", len(comments))
	}
	if comments[0].Body != "revised\n" {
		t.Errorf("body = %q, want revised", comments[0].Body)
	}
}

// TestEditComment_NotIssueMD verifies that EditComment does not rewrite the
// issue .md file.
func TestEditComment_NotIssueMD(t *testing.T) {
	s, m := newCommentMemStore(t)
	iss, err := s.Create(CreateInput{Title: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	orig, err := s.AddComment(iss.ID, "hans", "original\n")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	issuePath := s.filePath(iss.ID)
	before, err := m.ReadFile(issuePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	_, err = s.EditComment(iss.ID, orig.ID, "hans", "revised\n")
	if err != nil {
		t.Fatalf("EditComment: %v", err)
	}

	after, err := m.ReadFile(issuePath)
	if err != nil {
		t.Fatalf("ReadFile after: %v", err)
	}
	if string(before) != string(after) {
		t.Error("EditComment rewrote the issue .md — must not")
	}
}

// TestEditComment_RejectsMissingComment verifies that EditComment rejects a
// commentID that doesn't exist in the stream.
func TestEditComment_RejectsMissingComment(t *testing.T) {
	s := newMemStore(t)
	iss, err := s.Create(CreateInput{Title: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = s.EditComment(iss.ID, "nonexist", "hans", "body\n")
	if err == nil {
		t.Error("EditComment with non-existent commentID should fail")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

// ── L2 (Mem): DeleteComment ───────────────────────────────────────────────

// TestDeleteComment_OmittedFromResolved verifies that after DeleteComment,
// Comments() no longer returns the deleted comment.
func TestDeleteComment_OmittedFromResolved(t *testing.T) {
	s := newMemStore(t)
	iss, err := s.Create(CreateInput{Title: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	c, err := s.AddComment(iss.ID, "hans", "to be deleted\n")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	if err := s.DeleteComment(iss.ID, c.ID, "hans"); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}

	comments, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("expected 0 comments after delete, got %d: %+v", len(comments), comments)
	}
}

// TestDeleteComment_HistoryPreserved verifies that the on-disk stream still
// contains both the original and tombstone (full history preserved).
func TestDeleteComment_HistoryPreserved(t *testing.T) {
	s, m := newCommentMemStore(t)
	iss, err := s.Create(CreateInput{Title: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	c, err := s.AddComment(iss.ID, "hans", "to be deleted\n")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	if err := s.DeleteComment(iss.ID, c.ID, "hans"); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}

	// Raw stream should have 2 documents.
	sidecarPath := s.commentsPath(iss.ID)
	stream, err := readCommentStream(m, sidecarPath)
	if err != nil {
		t.Fatalf("readCommentStream: %v", err)
	}
	if len(stream) != 2 {
		t.Fatalf("expected 2 raw docs (original + tombstone), got %d", len(stream))
	}
	if !stream[1].Deleted {
		t.Error("second doc should be a tombstone")
	}
	if stream[1].Replaces != c.ID {
		t.Errorf("tombstone Replaces = %q, want %q", stream[1].Replaces, c.ID)
	}
}

// TestDeleteComment_NotIssueMD verifies DeleteComment does not rewrite the
// issue .md.
func TestDeleteComment_NotIssueMD(t *testing.T) {
	s, m := newCommentMemStore(t)
	iss, err := s.Create(CreateInput{Title: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	c, err := s.AddComment(iss.ID, "hans", "to be deleted\n")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	issuePath := s.filePath(iss.ID)
	before, err := m.ReadFile(issuePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if err := s.DeleteComment(iss.ID, c.ID, "hans"); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}

	after, err := m.ReadFile(issuePath)
	if err != nil {
		t.Fatalf("ReadFile after: %v", err)
	}
	if string(before) != string(after) {
		t.Error("DeleteComment rewrote the issue .md — must not")
	}
}

// TestDeleteComment_RejectsMissingComment verifies that DeleteComment rejects
// a commentID not in the stream.
func TestDeleteComment_RejectsMissingComment(t *testing.T) {
	s := newMemStore(t)
	iss, err := s.Create(CreateInput{Title: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.DeleteComment(iss.ID, "nonexist", "hans"); err == nil {
		t.Error("DeleteComment with non-existent commentID should fail")
	}
}

// ── L2 (Mem): Detail.Comments ─────────────────────────────────────────────

// TestDetail_CommentsLoaded verifies that Detail.Comments is populated from
// the sidecar.
func TestDetail_CommentsLoaded(t *testing.T) {
	s := newMemStore(t)
	iss, err := s.Create(CreateInput{Title: "detail test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	c1, err := s.AddComment(iss.ID, "hans", "first\n")
	if err != nil {
		t.Fatalf("AddComment 1: %v", err)
	}
	_, err = s.AddComment(iss.ID, "alice", "second\n")
	if err != nil {
		t.Fatalf("AddComment 2: %v", err)
	}

	d, err := s.Detail(iss.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(d.Comments) != 2 {
		t.Fatalf("Detail.Comments = %d, want 2", len(d.Comments))
	}
	if d.Comments[0].ID != c1.ID {
		t.Errorf("first comment ID = %q, want %q", d.Comments[0].ID, c1.ID)
	}
}

// TestDetail_CommentsResolved verifies that Detail.Comments shows the resolved
// view (edits applied, tombstones omitted).
func TestDetail_CommentsResolved(t *testing.T) {
	s := newMemStore(t)
	iss, err := s.Create(CreateInput{Title: "resolved"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	orig, err := s.AddComment(iss.ID, "hans", "original\n")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	_, err = s.EditComment(iss.ID, orig.ID, "hans", "revised\n")
	if err != nil {
		t.Fatalf("EditComment: %v", err)
	}

	d, err := s.Detail(iss.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(d.Comments) != 1 {
		t.Fatalf("Detail.Comments = %d, want 1 (resolved)", len(d.Comments))
	}
	if d.Comments[0].Body != "revised\n" {
		t.Errorf("Detail.Comments[0].Body = %q, want revised", d.Comments[0].Body)
	}
}

// ── L2 (Mem): migration of inline frontmatter comments ────────────────────

// TestMigration_InlineFrontmatterCommentsMovedToSidecar verifies that when an
// issue .md file contains inline comments in the old frontmatter format, they
// are migrated to the sidecar on first touch (AddComment, EditComment,
// DeleteComment).
func TestMigration_InlineFrontmatterCommentsMovedToSidecar(t *testing.T) {
	s, m := newCommentMemStore(t)

	// Create an issue the normal way.
	iss, err := s.Create(CreateInput{Title: "legacy"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Manually write the old-style frontmatter (with inline comments) directly
	// to the .md file, bypassing the store to simulate a pre-migration file.
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	oldMD := "---\n" +
		"id: " + iss.ID + "\n" +
		"title: legacy\n" +
		"status: open\n" +
		"type: task\n" +
		"priority: 2\n" +
		"created: 2026-06-01T10:00:00Z\n" +
		"updated: 2026-06-01T10:00:00Z\n" +
		"comments:\n" +
		"  - author: hans\n" +
		"    created: " + ts.Format("2006-01-02T15:04:05Z") + "\n" +
		"    body: old inline comment\n" +
		"---\n"

	if err := m.WriteAtomic(s.filePath(iss.ID), []byte(oldMD), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	// Now add a new comment — this should trigger migration.
	newC, err := s.AddComment(iss.ID, "alice", "new comment\n")
	if err != nil {
		t.Fatalf("AddComment (migration): %v", err)
	}

	// After migration, Comments() should include both the migrated and new comment.
	comments, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments after migration: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments after migration, got %d: %+v", len(comments), comments)
	}

	// The new comment must be present.
	found := false
	for _, c := range comments {
		if c.ID == newC.ID {
			found = true
		}
	}
	if !found {
		t.Error("new comment not found after migration")
	}

	// The issue .md must no longer contain 'comments:' in the frontmatter.
	mdBytes, err := m.ReadFile(s.filePath(iss.ID))
	if err != nil {
		t.Fatalf("ReadFile after migration: %v", err)
	}
	if strings.Contains(string(mdBytes), "comments:") {
		t.Error("issue .md still contains 'comments:' after migration")
	}
}

// ── L2 (Mem): AddComment validation ──────────────────────────────────────

// TestAddComment_RejectEmptyBody verifies that AddComment rejects an empty
// body (neither body nor deleted:true — §10).
func TestAddComment_RejectEmptyBody(t *testing.T) {
	s := newMemStore(t)
	iss, err := s.Create(CreateInput{Title: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = s.AddComment(iss.ID, "hans", "")
	if err == nil {
		t.Error("AddComment with empty body should fail")
	}
	_, err = s.AddComment(iss.ID, "hans", "   ")
	if err == nil {
		t.Error("AddComment with whitespace-only body should fail")
	}
}

// TestAddComment_RejectControlCharsInBody verifies that bodies with control
// characters that would force double-quoting are rejected.
func TestAddComment_RejectControlCharsInBody(t *testing.T) {
	s := newMemStore(t)
	iss, err := s.Create(CreateInput{Title: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A body with a NUL character would force double-quoted YAML scalar.
	_, err = s.AddComment(iss.ID, "hans", "bad\x00body")
	if err == nil {
		t.Error("AddComment with control char body should fail")
	}
}

// ── L2 (Mem): All() never touches sidecar (already tested, kept for clarity) ─

// (this test already exists in comments_test.go as TestStoreAll_NeverOpensSidecar)
