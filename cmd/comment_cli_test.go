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

//go:build integration

// L4 CLI tests for the comment commands:
//
//	taskmgr comment add   → prints commentDTO (with id field)
//	taskmgr comment edit  → EditComment
//	taskmgr comment rm    → DeleteComment (idempotent)
//	taskmgr show --json   → detailDTO.comments is resolved commentDTO[]
//	taskmgr show (human)  → renders the resolved comment log
package cmd_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// taskmgr runs the taskmgr binary (built from this module) with the given arguments
// from the specified working directory. It returns stdout, stderr, and exit code.
func taskmgr(t *testing.T, storeDir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	bin := taskmgrBin(t)
	cmd := exec.Command(bin, append([]string{"--dir", storeDir}, args...)...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// taskmgrBin returns the path to the taskmgr binary, building it once per test run.
// The binary is placed in os.TempDir() so it survives individual test teardowns.
var (
	_taskmgrBinPath string
	_taskmgrBinErr  error
	_taskmgrBinOnce sync.Once
)

func taskmgrBin(t *testing.T) string {
	t.Helper()
	_taskmgrBinOnce.Do(func() {
		bin := filepath.Join(os.TempDir(), "taskmgr-test-bin")
		out, err := exec.Command("go", "build", "-o", bin,
			"github.com/hk9890/task-manager").CombinedOutput()
		if err != nil {
			_taskmgrBinErr = fmt.Errorf("go build failed: %v\n%s", err, out)
			return
		}
		_taskmgrBinPath = bin
	})
	if _taskmgrBinErr != nil {
		t.Fatalf("failed to build taskmgr: %v", _taskmgrBinErr)
	}
	return _taskmgrBinPath
}

// newTestStoreDir creates a temporary directory, initialises a store, and
// creates one issue. Returns (storeRoot, issueID).
func newTestStoreDir(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.SetNow(func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	})
	iss, err := unwrap(s.Create(tasks.CreateInput{Title: "cli comment test"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return root, iss.ID
}

// ── comment add ──────────────────────────────────────────────────────────────

// TestL4_CommentAdd_PrintsCommentID verifies that `comment add --json` returns
// a commentDTO with an `id` field.
func TestL4_CommentAdd_PrintsCommentID(t *testing.T) {
	root, issID := newTestStoreDir(t)

	out, _, code := taskmgr(t, root, "--json", "comment", "add", issID, "a test note")
	if code != 0 {
		t.Fatalf("comment add failed (exit %d), stderr: %s", code, out)
	}

	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse JSON: %v\noutput: %s", err, out)
	}

	id, ok := dto["id"]
	if !ok {
		t.Fatalf("commentDTO missing 'id' field; got: %v", dto)
	}
	idStr, _ := id.(string)
	if len(idStr) != 8 {
		t.Errorf("comment id length = %d, want 8; got %q", len(idStr), idStr)
	}
}

// TestL4_CommentAdd_CommentDTOShape verifies the full commentDTO shape returned
// by `comment add --json`: id, created fields present.
func TestL4_CommentAdd_CommentDTOShape(t *testing.T) {
	root, issID := newTestStoreDir(t)

	out, _, code := taskmgr(t, root, "--json", "comment", "add", issID, "some body text")
	if code != 0 {
		t.Fatalf("comment add failed (exit %d): %s", code, out)
	}

	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse JSON: %v\noutput: %s", err, out)
	}

	for _, field := range []string{"id", "created"} {
		if _, ok := dto[field]; !ok {
			t.Errorf("commentDTO missing field %q; got keys: %v", field, mapKeys(dto))
		}
	}
}

// TestL4_CommentAdd_HumanOutput verifies that human-readable output mentions
// the issue ID and comment ID.
func TestL4_CommentAdd_HumanOutput(t *testing.T) {
	root, issID := newTestStoreDir(t)

	out, _, code := taskmgr(t, root, "comment", "add", issID, "human note")
	if code != 0 {
		t.Fatalf("comment add failed (exit %d): %s", code, out)
	}
	if !strings.Contains(out, issID) {
		t.Errorf("human output should mention issue ID %q; got: %s", issID, out)
	}
}

// TestL4_CommentAdd_EmptyBodyRejected verifies that an empty body returns
// a non-zero exit code.
func TestL4_CommentAdd_EmptyBodyRejected(t *testing.T) {
	root, issID := newTestStoreDir(t)

	_, _, code := taskmgr(t, root, "comment", "add", issID, "")
	if code == 0 {
		t.Error("expected non-zero exit for empty comment body")
	}
}

// TestL4_CommentAdd_FileFlag verifies that --file reads the body from a file.
func TestL4_CommentAdd_FileFlag(t *testing.T) {
	root, issID := newTestStoreDir(t)

	// Write body to a temp file.
	bodyFile := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(bodyFile, []byte("body from file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, code := taskmgr(t, root, "--json", "comment", "add", issID, "--file", bodyFile)
	if code != 0 {
		t.Fatalf("comment add --file failed (exit %d): %s", code, out)
	}

	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse JSON: %v\noutput: %s", err, out)
	}
	if dto["body"] != "body from file\n" {
		t.Errorf("body = %v, want %q", dto["body"], "body from file\n")
	}
}

// ── comment edit ─────────────────────────────────────────────────────────────

// TestL4_CommentEdit_UpdatesComment verifies that `comment edit` supersedes the
// original and that `show --json` reflects the revision.
func TestL4_CommentEdit_UpdatesComment(t *testing.T) {
	root, issID := newTestStoreDir(t)

	// Add an original comment.
	out, _, code := taskmgr(t, root, "--json", "comment", "add", issID, "original body")
	if code != 0 {
		t.Fatalf("comment add failed (exit %d): %s", code, out)
	}
	var addDTO map[string]interface{}
	if err := json.Unmarshal([]byte(out), &addDTO); err != nil {
		t.Fatalf("parse add DTO: %v\noutput: %s", err, out)
	}
	commentID, _ := addDTO["id"].(string)

	// Edit the comment.
	_, stderr, code := taskmgr(t, root, "comment", "edit", issID, commentID, "revised body")
	if code != 0 {
		t.Fatalf("comment edit failed (exit %d): %s", code, stderr)
	}

	// show --json should have 1 comment with revised body.
	out, _, code = taskmgr(t, root, "--json", "show", issID)
	if code != 0 {
		t.Fatalf("show failed: %s", out)
	}

	var detail map[string]interface{}
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("parse show: %v", err)
	}

	comments := extractComments(t, detail)
	if len(comments) != 1 {
		t.Fatalf("want 1 resolved comment after edit, got %d", len(comments))
	}
	body, _ := comments[0]["body"].(string)
	if !strings.Contains(body, "revised body") {
		t.Errorf("resolved comment body = %q, want to contain 'revised body'", body)
	}
}

// TestL4_CommentEdit_EmptyBodyRejected verifies that editing with an empty
// body is rejected (non-zero exit).
func TestL4_CommentEdit_EmptyBodyRejected(t *testing.T) {
	root, issID := newTestStoreDir(t)

	out, _, code := taskmgr(t, root, "--json", "comment", "add", issID, "original")
	if code != 0 {
		t.Fatalf("add failed (exit %d): %s", code, out)
	}
	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse: %v", err)
	}
	commentID, _ := dto["id"].(string)

	_, _, code = taskmgr(t, root, "comment", "edit", issID, commentID, "")
	if code == 0 {
		t.Error("expected non-zero exit for empty edit body")
	}
}

// TestL4_CommentEdit_AuthorFlag verifies that --author is passed through.
func TestL4_CommentEdit_AuthorFlag(t *testing.T) {
	root, issID := newTestStoreDir(t)

	out, _, code := taskmgr(t, root, "--json", "comment", "add", issID, "original", "--author", "alice")
	if code != 0 {
		t.Fatalf("add failed (exit %d): %s", code, out)
	}
	var addDTO map[string]interface{}
	if err := json.Unmarshal([]byte(out), &addDTO); err != nil {
		t.Fatalf("parse add DTO: %v", err)
	}
	commentID, _ := addDTO["id"].(string)

	// Edit with a different author.
	out, stderr, code := taskmgr(t, root, "--json", "comment", "edit", issID, commentID, "revised", "--author", "bob")
	if code != 0 {
		t.Fatalf("edit failed (exit %d): %s", code, stderr)
	}

	var editDTO map[string]interface{}
	if err := json.Unmarshal([]byte(out), &editDTO); err != nil {
		t.Fatalf("parse edit DTO: %v", err)
	}
	if editDTO["author"] != "bob" {
		t.Errorf("edit author = %v, want bob", editDTO["author"])
	}
}

// TestL4_CommentEdit_FileFlag verifies that --file reads the body from a file.
func TestL4_CommentEdit_FileFlag(t *testing.T) {
	root, issID := newTestStoreDir(t)

	out, _, code := taskmgr(t, root, "--json", "comment", "add", issID, "original")
	if code != 0 {
		t.Fatalf("add failed (exit %d): %s", code, out)
	}
	var addDTO map[string]interface{}
	if err := json.Unmarshal([]byte(out), &addDTO); err != nil {
		t.Fatalf("parse: %v", err)
	}
	commentID, _ := addDTO["id"].(string)

	bodyFile := filepath.Join(t.TempDir(), "edit.txt")
	if err := os.WriteFile(bodyFile, []byte("edited from file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, code = taskmgr(t, root, "--json", "comment", "edit", issID, commentID, "--file", bodyFile)
	if code != 0 {
		t.Fatalf("edit --file failed (exit %d): %s", code, out)
	}
	var editDTO map[string]interface{}
	if err := json.Unmarshal([]byte(out), &editDTO); err != nil {
		t.Fatalf("parse edit DTO: %v", err)
	}
	if editDTO["body"] != "edited from file\n" {
		t.Errorf("body = %v, want %q", editDTO["body"], "edited from file\n")
	}
}

// ── comment rm ───────────────────────────────────────────────────────────────

// TestL4_CommentRm_RemovesFromResolved verifies that `comment rm` removes the
// comment from the resolved view.
func TestL4_CommentRm_RemovesFromResolved(t *testing.T) {
	root, issID := newTestStoreDir(t)

	out, _, code := taskmgr(t, root, "--json", "comment", "add", issID, "to be deleted")
	if code != 0 {
		t.Fatalf("add failed (exit %d): %s", code, out)
	}
	var addDTO map[string]interface{}
	if err := json.Unmarshal([]byte(out), &addDTO); err != nil {
		t.Fatalf("parse: %v", err)
	}
	commentID, _ := addDTO["id"].(string)

	// Delete it.
	_, stderr, code := taskmgr(t, root, "comment", "rm", issID, commentID)
	if code != 0 {
		t.Fatalf("comment rm failed (exit %d): %s", code, stderr)
	}

	// show should have 0 comments.
	out, _, code = taskmgr(t, root, "--json", "show", issID)
	if code != 0 {
		t.Fatalf("show failed: %s", out)
	}
	var detail map[string]interface{}
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("parse show: %v", err)
	}
	comments := extractComments(t, detail)
	if len(comments) != 0 {
		t.Errorf("want 0 resolved comments after rm, got %d", len(comments))
	}
}

// TestL4_CommentRm_Idempotent verifies that running `comment rm` twice on the
// same comment ID succeeds (idempotent — the tombstone is already present so
// the resolved view stays empty regardless).
func TestL4_CommentRm_Idempotent(t *testing.T) {
	root, issID := newTestStoreDir(t)

	out, _, code := taskmgr(t, root, "--json", "comment", "add", issID, "note")
	if code != 0 {
		t.Fatalf("add failed (exit %d): %s", code, out)
	}
	var addDTO map[string]interface{}
	if err := json.Unmarshal([]byte(out), &addDTO); err != nil {
		t.Fatalf("parse: %v", err)
	}
	commentID, _ := addDTO["id"].(string)

	// First delete.
	_, stderr, code := taskmgr(t, root, "comment", "rm", issID, commentID)
	if code != 0 {
		t.Fatalf("first rm failed (exit %d): %s", code, stderr)
	}

	// Second delete. Per spec: idempotent. Accept any exit code but verify the
	// resolved view still has 0 comments.
	taskmgr(t, root, "comment", "rm", issID, commentID)

	out, _, code = taskmgr(t, root, "--json", "show", issID)
	if code != 0 {
		t.Fatalf("show after second rm failed: %s", out)
	}
	var detail map[string]interface{}
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("parse show: %v", err)
	}
	comments := extractComments(t, detail)
	if len(comments) != 0 {
		t.Errorf("want 0 resolved comments after idempotent rm, got %d", len(comments))
	}
}

// ── show --json: detailDTO.comments ──────────────────────────────────────────

// TestL4_ShowJSON_CommentsResolved verifies that `show --json` returns the
// resolved commentDTO[] with id, author, created, body fields.
func TestL4_ShowJSON_CommentsResolved(t *testing.T) {
	root, issID := newTestStoreDir(t)

	// Add two comments; edit the first one.
	out, _, code := taskmgr(t, root, "--json", "comment", "add", issID, "first comment", "--author", "alice")
	if code != 0 {
		t.Fatalf("add 1 failed (exit %d): %s", code, out)
	}
	var a map[string]interface{}
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		t.Fatalf("parse first add: %v\noutput: %s", err, out)
	}
	firstID, _ := a["id"].(string)

	taskmgr(t, root, "comment", "add", issID, "second comment", "--author", "bob")
	taskmgr(t, root, "comment", "edit", issID, firstID, "revised first", "--author", "alice")

	out, _, code = taskmgr(t, root, "--json", "show", issID)
	if code != 0 {
		t.Fatalf("show failed: %s", out)
	}

	var detail map[string]interface{}
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("parse show: %v", err)
	}

	comments := extractComments(t, detail)
	// Resolved: first was edited (still 1 slot), second unedited → 2 total.
	if len(comments) != 2 {
		t.Fatalf("want 2 resolved comments, got %d: %v", len(comments), comments)
	}

	// Each commentDTO must have an "id" field.
	for i, c := range comments {
		if _, ok := c["id"]; !ok {
			t.Errorf("comments[%d] missing 'id' field; got: %v", i, c)
		}
		if _, ok := c["created"]; !ok {
			t.Errorf("comments[%d] missing 'created' field", i)
		}
	}

	// First comment should now show the revised body.
	body0, _ := comments[0]["body"].(string)
	if !strings.Contains(body0, "revised first") {
		t.Errorf("comments[0].body = %q, want to contain 'revised first'", body0)
	}
}

// TestL4_ShowHuman_CommentsRendered verifies that `show` (human) prints
// comments in the resolved log.
func TestL4_ShowHuman_CommentsRendered(t *testing.T) {
	root, issID := newTestStoreDir(t)

	_, _, code := taskmgr(t, root, "comment", "add", issID, "a human note", "--author", "alice")
	if code != 0 {
		t.Fatal("comment add failed")
	}

	out, _, code := taskmgr(t, root, "show", issID)
	if code != 0 {
		t.Fatalf("show failed (exit %d): %s", code, out)
	}
	if !strings.Contains(out, "human note") {
		t.Errorf("human show should include comment body; got:\n%s", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("human show should include comment author; got:\n%s", out)
	}
}

// TestL4_ShowJSON_CommentReplaces verifies that an edit comment in the resolved
// view carries a 'replaces' field.
func TestL4_ShowJSON_CommentReplaces(t *testing.T) {
	root, issID := newTestStoreDir(t)

	out, _, code := taskmgr(t, root, "--json", "comment", "add", issID, "original", "--author", "alice")
	if code != 0 {
		t.Fatalf("add failed (exit %d): %s", code, out)
	}
	var a map[string]interface{}
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		t.Fatalf("parse add: %v\noutput: %s", err, out)
	}
	firstID, _ := a["id"].(string)

	taskmgr(t, root, "comment", "edit", issID, firstID, "revised", "--author", "alice")

	out, _, code = taskmgr(t, root, "--json", "show", issID)
	if code != 0 {
		t.Fatalf("show failed: %s", out)
	}
	var detail map[string]interface{}
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("parse show: %v", err)
	}
	comments := extractComments(t, detail)
	if len(comments) != 1 {
		t.Fatalf("want 1 resolved comment, got %d", len(comments))
	}
	// The resolved comment is the revision, so it should have a 'replaces' field.
	replaces, ok := comments[0]["replaces"]
	if !ok || replaces != firstID {
		t.Errorf("resolved comment replaces = %v, want %q", replaces, firstID)
	}
}

// ── full lifecycle ────────────────────────────────────────────────────────────

// TestL4_CommentLifecycle_EndToEnd is a full lifecycle test:
//
//	add → edit → rm, verified via show --json at each step.
func TestL4_CommentLifecycle_EndToEnd(t *testing.T) {
	root, issID := newTestStoreDir(t)

	// Step 1: add a comment.
	out, _, code := taskmgr(t, root, "--json", "comment", "add", issID, "initial note", "--author", "alice")
	if code != 0 {
		t.Fatalf("add failed (exit %d): %s", code, out)
	}
	var addDTO map[string]interface{}
	if err := json.Unmarshal([]byte(out), &addDTO); err != nil {
		t.Fatalf("parse add: %v\noutput: %s", err, out)
	}
	cid, _ := addDTO["id"].(string)
	if len(cid) != 8 {
		t.Fatalf("bad comment id %q", cid)
	}

	// Step 2: show --json should have 1 comment.
	showAndExpectCount(t, root, issID, 1)

	// Step 3: edit the comment.
	_, stderr, code := taskmgr(t, root, "comment", "edit", issID, cid, "revised note", "--author", "alice")
	if code != 0 {
		t.Fatalf("edit failed (exit %d): %s", code, stderr)
	}
	showAndExpectCount(t, root, issID, 1) // still 1 (chain collapses)

	// Step 4: delete the comment.
	_, stderr, code = taskmgr(t, root, "comment", "rm", issID, cid)
	if code != 0 {
		t.Fatalf("rm failed (exit %d): %s", code, stderr)
	}
	showAndExpectCount(t, root, issID, 0) // deleted → 0
}

func showAndExpectCount(t *testing.T, root, issID string, want int) {
	t.Helper()
	out, _, code := taskmgr(t, root, "--json", "show", issID)
	if code != 0 {
		t.Fatalf("show failed (exit %d): %s", code, out)
	}
	var detail map[string]interface{}
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("parse show: %v", err)
	}
	comments := extractComments(t, detail)
	if len(comments) != want {
		t.Errorf("show: want %d resolved comments, got %d", want, len(comments))
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// extractComments pulls the comments array from a detailDTO map.
func extractComments(t *testing.T, detail map[string]interface{}) []map[string]interface{} {
	t.Helper()
	raw, ok := detail["comments"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("comments field is not an array: %T", raw)
	}
	out := make([]map[string]interface{}, len(arr))
	for i, v := range arr {
		m, ok := v.(map[string]interface{})
		if !ok {
			t.Fatalf("comments[%d] is not a map: %T", i, v)
		}
		out[i] = m
	}
	return out
}

// mapKeys returns the keys of a map for error messages.
func mapKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
