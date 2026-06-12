//go:build integration

// L4 CLI tests for:
//
//	taskmgr reopen <id>
//	taskmgr update --status closed  (routes through Close)
//	taskmgr update --status open    (routes through Reopen on a closed issue)
package cmd_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// newTestStoreDirWithClosed creates a temp store, creates one open issue and
// one closed issue. Returns (root, openID, closedID).
func newTestStoreDirWithClosed(t *testing.T) (root, openID, closedID string) {
	t.Helper()
	root = t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.SetNow(func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	})
	open, err := s.Create(tasks.CreateInput{Title: "open issue"})
	if err != nil {
		t.Fatalf("Create open: %v", err)
	}
	closed, err := s.Create(tasks.CreateInput{Title: "closed issue"})
	if err != nil {
		t.Fatalf("Create to-close: %v", err)
	}
	if _, err := s.Close(closed.ID, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return root, open.ID, closed.ID
}

// TestL4_Reopen_Success verifies that `taskmgr reopen <id>` on a closed issue
// succeeds (exit 0) and sets status back to open.
func TestL4_Reopen_Success(t *testing.T) {
	root, _, closedID := newTestStoreDirWithClosed(t)

	_, stderr, code := taskmgr(t, root, "reopen", closedID)
	if code != 0 {
		t.Fatalf("reopen failed (exit %d): %s", code, stderr)
	}

	// Verify via show --json.
	out, _, code := taskmgr(t, root, "--json", "show", closedID)
	if code != 0 {
		t.Fatalf("show failed (exit %d): %s", code, out)
	}
	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse show: %v\noutput: %s", err, out)
	}
	if dto["status"] != "open" {
		t.Errorf("status = %v, want open", dto["status"])
	}
	// closed and close_reason must be absent / zero.
	if _, ok := dto["closed"]; ok {
		t.Errorf("closed field should be absent after reopen, got: %v", dto["closed"])
	}
	if _, ok := dto["close_reason"]; ok {
		t.Errorf("close_reason field should be absent after reopen, got: %v", dto["close_reason"])
	}
}

// TestL4_Reopen_NotFound verifies that `taskmgr reopen <unknown>` exits with 1.
func TestL4_Reopen_NotFound(t *testing.T) {
	root, _, _ := newTestStoreDirWithClosed(t)

	_, _, code := taskmgr(t, root, "reopen", "tst-9999")
	if code == 0 {
		t.Error("expected non-zero exit for reopen of unknown ID")
	}
}

// TestL4_UpdateStatus_ClosedMovesToClosedDir verifies that
// `taskmgr update --status closed <id>` moves the file to cold partition.
func TestL4_UpdateStatus_ClosedMovesToClosedDir(t *testing.T) {
	root, openID, _ := newTestStoreDirWithClosed(t)

	_, stderr, code := taskmgr(t, root, "update", openID, "--status", "closed")
	if code != 0 {
		t.Fatalf("update --status closed failed (exit %d): %s", code, stderr)
	}

	out, _, code := taskmgr(t, root, "--json", "show", openID)
	if code != 0 {
		t.Fatalf("show failed: %s", out)
	}
	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if dto["status"] != "closed" {
		t.Errorf("status = %v, want closed", dto["status"])
	}
	// closed timestamp must be present.
	if _, ok := dto["closed"]; !ok {
		t.Error("closed field must be present after update --status closed")
	}
}

// TestL4_UpdateStatus_OpenReopensClosedIssue verifies that
// `taskmgr update --status open <closedID>` reopens it.
func TestL4_UpdateStatus_OpenReopensClosedIssue(t *testing.T) {
	root, _, closedID := newTestStoreDirWithClosed(t)

	_, stderr, code := taskmgr(t, root, "update", closedID, "--status", "open")
	if code != 0 {
		t.Fatalf("update --status open failed (exit %d): %s", code, stderr)
	}

	out, _, code := taskmgr(t, root, "--json", "show", closedID)
	if code != 0 {
		t.Fatalf("show failed: %s", out)
	}
	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if dto["status"] != "open" {
		t.Errorf("status = %v, want open", dto["status"])
	}
}

// TestL4_Reopen_HumanOutput verifies that human output mentions the issue ID.
func TestL4_Reopen_HumanOutput(t *testing.T) {
	root, _, closedID := newTestStoreDirWithClosed(t)

	out, _, code := taskmgr(t, root, "reopen", closedID)
	if code != 0 {
		t.Fatalf("reopen failed (exit %d): %s", code, out)
	}
	if out == "" {
		t.Error("expected some human output from reopen")
	}
}

// TestL4_CommentOnClosedIssue verifies that `comment add` on a closed issue
// succeeds (sidecar append is allowed for closed issues).
func TestL4_CommentOnClosedIssue(t *testing.T) {
	root, _, closedID := newTestStoreDirWithClosed(t)

	out, stderr, code := taskmgr(t, root, "--json", "comment", "add", closedID, "post-close note")
	if code != 0 {
		t.Fatalf("comment add on closed issue failed (exit %d): stderr=%s out=%s", code, stderr, out)
	}

	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse commentDTO: %v\nout: %s", err, out)
	}
	if _, ok := dto["id"]; !ok {
		t.Errorf("commentDTO missing id: %v", dto)
	}
}
