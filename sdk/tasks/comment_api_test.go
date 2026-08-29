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

// L1 + L2 tests for the new comment API:
//   AddComment(*Comment,error), EditComment, DeleteComment, Comments, Detail.Comments
//   Comment validation (§10)
//   Migration of inline frontmatter comments to sidecar

import (
	"errors"
	"regexp"
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
		"line1\x00line2",              // NUL character
		"line with \x01 control char", // SOH
		"text\x1b[0mescaped",          // ESC sequence
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

// TestValidateReplaces_EarlierInStream verifies that validateReplaces enforces
// the TASK-STORAGE-SPEC §4.4 / §10 "earlier in the stream" rule:
//   - An ID found in the pre-append stream is earlier → accepted.
//   - An ID NOT found in the pre-append stream is not earlier → rejected.
//
// The pre-append stream is passed as-is by the callers (EditComment /
// DeleteComment), so every document in it is by definition earlier than the
// document about to be appended.
func TestValidateReplaces_EarlierInStream(t *testing.T) {
	stream := []Comment{
		{ID: "11111111", Body: "first\n"},
		{ID: "22222222", Body: "second\n"},
	}

	// Both IDs are in the pre-append stream → earlier → accepted.
	if err := validateReplaces("11111111", stream); err != nil {
		t.Errorf("replaces first comment: want nil, got %v", err)
	}
	if err := validateReplaces("22222222", stream); err != nil {
		t.Errorf("replaces second comment: want nil, got %v", err)
	}

	// An ID not in the stream (e.g. the ID of the doc being appended, or an
	// entirely made-up one) is not earlier → must be rejected.
	if err := validateReplaces("33333333", stream); err == nil {
		t.Error("replaces ID not in stream: want error, got nil")
	}
}

// ── L2 (Mem): AddComment returns (*Comment, error) ────────────────────────

// TestAddComment_ReturnsSelf verifies that AddComment returns the new comment
// with its ID populated.
func TestAddComment_ReturnsSelf(t *testing.T) {
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
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
	s, m := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "watch"}))
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
	s, m := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "sidecar test"}))
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
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
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
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "no comments"}))
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
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
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
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
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
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
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
	s, m := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
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
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
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

// TestEditDeleteComment_RejectEmptyCommentID pins that the comment id is
// required, not merely checked when present.
//
// The existence check runs through validateReplaces, whose contract is "is this
// document's optional replaces field valid?" — and for that question an empty
// value correctly means "not a revision". Edit and Delete borrow the helper for
// the opposite question, so before this was fixed EditComment(id, "", …) passed
// straight through and appended a plain comment, and DeleteComment(id, "", …)
// wrote a tombstone that retracted nothing. Both reported success.
func TestEditDeleteComment_RejectEmptyCommentID(t *testing.T) {
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.AddComment(iss.ID, "hans", "original\n"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	var ve *ValidationError

	_, err = s.EditComment(iss.ID, "", "hans", "revision\n")
	if err == nil {
		t.Error("EditComment with an empty commentID must fail")
	} else if !errors.As(err, &ve) {
		t.Errorf("EditComment: expected *ValidationError, got %T: %v", err, err)
	}

	err = s.DeleteComment(iss.ID, "", "hans")
	if err == nil {
		t.Error("DeleteComment with an empty commentID must fail")
	} else if !errors.As(err, &ve) {
		t.Errorf("DeleteComment: expected *ValidationError, got %T: %v", err, err)
	}

	// Neither call may have appended anything.
	got, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("comment stream has %d entries, want 1 — a rejected call still wrote", len(got))
	}
}

// ── L2 (Mem): DeleteComment ───────────────────────────────────────────────

// TestDeleteComment_OmittedFromResolved verifies that after DeleteComment,
// Comments() no longer returns the deleted comment.
func TestDeleteComment_OmittedFromResolved(t *testing.T) {
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
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
	s, m := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
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
	s, m := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
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
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
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
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "detail test"}))
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
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "resolved"}))
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
	s, m := newMemStore(t)

	// Create an issue the normal way.
	iss, err := unwrap(s.Create(CreateInput{Title: "legacy"}))
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

// legacyMD renders an issue .md in the pre-sidecar frontmatter format, with
// one inline comment per (author, body) pair. Every comment shares a
// timestamp, so a repeated pair differs from its twin only by position.
func legacyMD(id string, pairs [][2]string) string {
	md := "---\n" +
		"id: " + id + "\n" +
		"title: legacy\n" +
		"status: open\n" +
		"type: task\n" +
		"priority: 2\n" +
		"created: 2026-06-01T10:00:00Z\n" +
		"updated: 2026-06-01T10:00:00Z\n" +
		"comments:\n"
	for _, p := range pairs {
		md += "  - author: " + p[0] + "\n" +
			"    created: 2026-06-01T10:00:00Z\n" +
			"    body: " + p[1] + "\n"
	}
	return md + "---\n"
}

// sidecarIDs returns the ids of every document in an issue's sidecar, in
// append order — the raw stream, before replaces-chain resolution.
func sidecarIDs(t *testing.T, s *Store, id string) []string {
	t.Helper()
	stream, err := readCommentStream(s.fs, s.commentsPath(id))
	if err != nil {
		t.Fatalf("readCommentStream: %v", err)
	}
	ids := make([]string, len(stream))
	for i, c := range stream {
		ids[i] = c.ID
	}
	return ids
}

// TestMigrateInlineComments_InterruptedRetry_NoDuplicates covers the crash
// window inside migrateInlineComments. The migration appends every legacy
// comment to the sidecar and only then rewrites the .md without the inline
// list; the two halves are separately durable, so a failure between them
// leaves the sidecar migrated and the .md unchanged. The next comment
// mutation migrates again — and while the ids were random, that second pass
// appended every comment a second time under fresh ids, which no reader could
// collapse, so every comment came back twice.
func TestMigrateInlineComments_InterruptedRetry_NoDuplicates(t *testing.T) {
	s, m := newMemStore(t)

	iss, err := unwrap(s.Create(CreateInput{Title: "legacy"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	mdPath := s.filePath(iss.ID)
	legacy := [][2]string{{"hans", "first"}, {"alice", "second"}}
	if err := m.WriteAtomic(mdPath, []byte(legacyMD(iss.ID, legacy)), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	// Interrupt the migration after the appends, before the .md rewrite.
	diskFull := errors.New("no space left on device")
	m.FailOn("WriteAtomic", mdPath, diskFull)

	if _, err := s.AddComment(iss.ID, "bob", "new comment\n"); !errors.Is(err, diskFull) {
		t.Fatalf("AddComment during interrupted migration: got %v, want %v", err, diskFull)
	}

	// The half-done state the retry has to cope with: sidecar written, .md
	// still carrying the inline list. If either half ever changes, this test
	// stops exercising the window it was written for, so assert both.
	if got := len(sidecarIDs(t, s, iss.ID)); got != len(legacy) {
		t.Fatalf("sidecar after interrupted migration = %d docs, want %d", got, len(legacy))
	}
	mdBytes, err := m.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(mdBytes), "comments:") {
		t.Fatal("interrupted migration rewrote the .md; the crash window is not being exercised")
	}

	// The fault is consumed once it fires, so this retry reaches the .md.
	if _, err := s.AddComment(iss.ID, "bob", "new comment\n"); err != nil {
		t.Fatalf("AddComment after interrupted migration: %v", err)
	}

	// Each legacy comment must be readable exactly once, alongside the new one.
	comments, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	bodies := map[string]int{}
	for _, c := range comments {
		bodies[strings.TrimSpace(c.Body)]++
	}
	for _, want := range []string{"first", "second", "new comment"} {
		if bodies[want] != 1 {
			t.Errorf("body %q appears %d times, want 1 (all: %v)", want, bodies[want], bodies)
		}
	}
	if len(comments) != 3 {
		t.Errorf("Comments = %d, want 3: %+v", len(comments), comments)
	}

	// The sidecar itself must hold no duplicate id: the retry has to skip what
	// the interrupted attempt wrote, not merely resolve down to one comment.
	ids := sidecarIDs(t, s, iss.ID)
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("sidecar holds duplicate comment id %q: %v", id, ids)
		}
		seen[id] = true
	}
	if len(ids) != 3 {
		t.Errorf("sidecar = %d docs, want 3: %v", len(ids), ids)
	}

	// The migration completed on the retry.
	mdBytes, err = m.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(mdBytes), "comments:") {
		t.Error("issue .md still contains 'comments:' after the retry")
	}
}

// TestMigrateInlineComments_IdenticalComments_BothSurvive verifies that two
// legacy comments identical in author, timestamp and body still migrate to
// distinct ids. Position is part of the derivation for exactly this case:
// without it the second would collide with the first and be skipped as
// already-migrated, silently dropping a comment.
func TestMigrateInlineComments_IdenticalComments_BothSurvive(t *testing.T) {
	s, m := newMemStore(t)

	iss, err := unwrap(s.Create(CreateInput{Title: "legacy"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	twins := [][2]string{{"hans", "same text"}, {"hans", "same text"}}
	if err := m.WriteAtomic(s.filePath(iss.ID), []byte(legacyMD(iss.ID, twins)), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	if _, err := s.AddComment(iss.ID, "bob", "new comment\n"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	comments, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	var same int
	for _, c := range comments {
		if strings.TrimSpace(c.Body) == "same text" {
			same++
		}
	}
	if same != 2 {
		t.Errorf("identical legacy comments = %d after migration, want 2: %+v", same, comments)
	}
}

// TestMigratedCommentID_StableAndWellFormed checks the two properties the
// retry skip rests on — the id is a pure function of the legacy comment and
// its position, and distinct inputs give distinct ids — plus the shape
// TASK-STORAGE-SPEC §4.4 requires of every comment id.
func TestMigratedCommentID_StableAndWellFormed(t *testing.T) {
	lc := legacyComment{Author: "hans", Created: "2026-06-01T10:00:00Z", Body: "hello"}

	first := migratedCommentID(lc, 0)
	if again := migratedCommentID(lc, 0); again != first {
		t.Errorf("migratedCommentID is not stable: %q then %q", first, again)
	}
	if next := migratedCommentID(lc, 1); next == first {
		t.Errorf("index 0 and 1 produced the same id %q", first)
	}
	if want := regexp.MustCompile(`^[0-9a-z]{8}$`); !want.MatchString(first) {
		t.Errorf("migratedCommentID = %q, want ^[0-9a-z]{8}$", first)
	}

	// Length-prefixing keeps a field boundary unambiguous: moving a character
	// from the author into the body must change the id.
	shifted := legacyComment{Author: "han", Created: lc.Created, Body: "shello"}
	if migratedCommentID(shifted, 0) == first {
		t.Error("a field-boundary shift produced the same id")
	}
}

// ── L2 (Mem): AddComment validation ──────────────────────────────────────

// TestAddComment_RejectEmptyBody verifies that AddComment rejects an empty
// body (neither body nor deleted:true — §10).
func TestAddComment_RejectEmptyBody(t *testing.T) {
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
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
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
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
