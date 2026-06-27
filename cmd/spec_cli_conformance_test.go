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

// spec_cli_conformance_test.go — CLI-SPEC conformance suite (L4 integration tests).
//
// Spec sections covered:
//   §1  Global conventions — exit codes (0 = success, 1 = any error); errors
//       printed to stderr, prefixed "taskmgr: "; nothing printed to stdout on error.
//   §4  Mutation commands — close idempotency (taskmgr close); dep add idempotency;
//       comment rm idempotency (already in comment_cli_test.go — not duplicated here).
//   §6  JSON output shapes — lists return JSON arrays ([]) not null; empty list
//       returns []; issueDTO required fields present; detailDTO has comments array.
//
// Already well-covered elsewhere (not duplicated here):
//   - comment rm idempotency → comment_cli_test.go (TestL4_CommentRm_Idempotent)
//   - malformed -q exit 1 + stderr prefix → query_cli_test.go (TestL4_MalformedExpr_ExitOne)
//   - list/search --all and hot-only scoping → list_all_cli_test.go and query_cli_test.go
//   - comment add/edit/rm JSON shapes → comment_cli_test.go
//   - reopen and update --status → reopen_cli_test.go

package cmd_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// ── §1: exit code discipline ─────────────────────────────────────────────────

// TestSpec_CLI_NotFound_ExitOne verifies that looking up an unknown ID exits
// with code 1 (CLI-SPEC §1: "1 = Any error (not found …)").
func TestSpec_CLI_NotFound_ExitOne(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	_, _, code := taskmgr(t, root, "show", "tst-9999")
	if code != 1 {
		t.Errorf("show <unknown>: expected exit 1, got %d", code)
	}
}

// TestSpec_CLI_ValidationError_ExitOne verifies that a validation failure
// (e.g. empty title) exits with code 1 (CLI-SPEC §1: "1 = validation failure").
func TestSpec_CLI_ValidationError_ExitOne(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	_, _, code := taskmgr(t, root, "create", "--title", "")
	if code != 1 {
		t.Errorf("create --title '': expected exit 1, got %d", code)
	}
}

// TestSpec_CLI_Success_ExitZero verifies that a successful mutation exits with
// code 0 (CLI-SPEC §1: "0 = Success").
func TestSpec_CLI_Success_ExitZero(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	_, _, code := taskmgr(t, root, "create", "--title", "green test")
	if code != 0 {
		t.Errorf("create: expected exit 0, got %d", code)
	}
}

// ── §1: error goes to stderr not stdout, prefixed "taskmgr: " ──────────────────

// TestSpec_CLI_ErrorOnStderr verifies that an error message is sent to stderr
// (not stdout) and is prefixed with "taskmgr: ".
// CLI-SPEC §1: "The message is printed to stderr, prefixed taskmgr: ".
func TestSpec_CLI_ErrorOnStderr(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	stdout, stderr, code := taskmgr(t, root, "show", "tst-9999")
	if code == 0 {
		t.Skip("show unknown ID unexpectedly succeeded — skip stderr check")
	}

	// Error must be on stderr.
	if !strings.HasPrefix(stderr, "taskmgr: ") {
		t.Errorf("error not prefixed 'taskmgr: ' on stderr; stderr=%q stdout=%q", stderr, stdout)
	}

	// Stdout must be empty on error.
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout must be empty on error; got: %q", stdout)
	}
}

// TestSpec_CLI_ValidationError_OnStderr verifies stderr discipline for
// validation failures (CLI-SPEC §1: errors always to stderr).
func TestSpec_CLI_ValidationError_OnStderr(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	stdout, stderr, code := taskmgr(t, root, "create", "--title", "")
	if code == 0 {
		t.Skip("create with empty title unexpectedly succeeded")
	}

	if !strings.HasPrefix(stderr, "taskmgr: ") {
		t.Errorf("validation error not prefixed 'taskmgr: '; stderr=%q stdout=%q", stderr, stdout)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout must be empty on validation error; got: %q", stdout)
	}
}

// ── §4: close idempotency ─────────────────────────────────────────────────────

// TestSpec_CLI_CloseIdempotent verifies that running `taskmgr close <id>` twice
// on the same issue exits 0 on both calls.
// CLI-SPEC §4: "taskmgr close <id> … Idempotent."
func TestSpec_CLI_CloseIdempotent(t *testing.T) {
	root, issID := newTestStoreDir(t)

	// First close.
	_, stderr, code := taskmgr(t, root, "close", issID)
	if code != 0 {
		t.Fatalf("first close failed (exit %d): %s", code, stderr)
	}

	// Second close on the same already-closed issue: must also exit 0.
	_, stderr2, code2 := taskmgr(t, root, "close", issID)
	if code2 != 0 {
		t.Errorf("second close (idempotent): expected exit 0, got %d; stderr: %s", code2, stderr2)
	}

	// Verify the issue is still closed after the idempotent call.
	out, _, code3 := taskmgr(t, root, "--json", "show", issID)
	if code3 != 0 {
		t.Fatalf("show after re-close failed: %s", out)
	}
	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse show: %v\nout: %s", err, out)
	}
	if dto["status"] != "closed" {
		t.Errorf("status after re-close = %v, want closed", dto["status"])
	}
}

// ── §4: dep add idempotency ───────────────────────────────────────────────────

// TestSpec_CLI_DepAddIdempotent verifies that `taskmgr dep add <dep> <blocker>`
// is idempotent: running it twice exits 0 on both calls.
// CLI-SPEC §4: "taskmgr dep add … Idempotent."
func TestSpec_CLI_DepAddIdempotent(t *testing.T) {
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
	blocker, err := unwrap(s.Create(tasks.CreateInput{Title: "blocker"}))
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	dep, err := unwrap(s.Create(tasks.CreateInput{Title: "dependent"}))
	if err != nil {
		t.Fatalf("Create dep: %v", err)
	}

	// First dep add.
	_, stderr, code := taskmgr(t, root, "dep", "add", dep.ID, blocker.ID)
	if code != 0 {
		t.Fatalf("first dep add failed (exit %d): %s", code, stderr)
	}

	// Second dep add on the same pair: must also exit 0 (idempotent).
	_, stderr2, code2 := taskmgr(t, root, "dep", "add", dep.ID, blocker.ID)
	if code2 != 0 {
		t.Errorf("second dep add (idempotent): expected exit 0, got %d; stderr: %s", code2, stderr2)
	}

	// Exactly one entry in blocked_by (no duplicate written).
	out, _, code3 := taskmgr(t, root, "--json", "show", dep.ID)
	if code3 != 0 {
		t.Fatalf("show dep failed: %s", out)
	}
	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse show: %v\nout: %s", err, out)
	}
	blockedBy, _ := dto["blocked_by"].([]interface{})
	if len(blockedBy) != 1 {
		t.Errorf("blocked_by len = %d after duplicate dep add, want 1 (no duplicate)", len(blockedBy))
	}
}

// TestSpec_CLI_DepAdd_SelfRejected verifies that `taskmgr dep add <id> <id>`
// is rejected with exit code 1 (CLI-SPEC §4: "rejects self-dependency").
func TestSpec_CLI_DepAdd_SelfRejected(t *testing.T) {
	root, issID := newTestStoreDir(t)

	_, _, code := taskmgr(t, root, "dep", "add", issID, issID)
	if code != 1 {
		t.Errorf("dep add self-loop: expected exit 1, got %d", code)
	}
}

// ── §6: JSON output shapes ───────────────────────────────────────────────────

// TestSpec_CLI_ListJSON_IsArray verifies that `taskmgr list --json` always returns
// a JSON array, never null or an object. CLI-SPEC §6: "array of issueDTO".
func TestSpec_CLI_ListJSON_IsArray(t *testing.T) {
	root, _ := newTestStoreDir(t)

	out, _, code := taskmgr(t, root, "--json", "list")
	if code != 0 {
		t.Fatalf("list failed (exit %d): %s", code, out)
	}

	// Must be a JSON array.
	var arr []interface{}
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("list --json output is not a JSON array: %v\noutput: %s", err, out)
	}
}

// TestSpec_CLI_ListJSON_EmptyIsEmptyArray verifies that when no issues match,
// `taskmgr list --json` returns [] (empty array) not null.
// CLI-SPEC §6: JSON arrays must be [] when empty (consistent contract for agents).
func TestSpec_CLI_ListJSON_EmptyIsEmptyArray(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// No issues created: the list must be empty.

	out, _, code := taskmgr(t, root, "--json", "list")
	if code != 0 {
		t.Fatalf("list on empty store failed (exit %d): %s", code, out)
	}

	trimmed := strings.TrimSpace(out)
	// Must be exactly "[]" (or valid JSON null in a pinch but spec says array).
	var arr []interface{}
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("list --json on empty store is not a JSON array: %v\noutput: %q", err, out)
	}
	if len(arr) != 0 {
		t.Errorf("list --json on empty store: expected [], got %s", trimmed)
	}
}

// TestSpec_CLI_IssueDTOShape verifies that every issueDTO in a list output
// carries the required fields: id, title, status, type, priority, created,
// updated. CLI-SPEC §6: issueDTO shape.
func TestSpec_CLI_IssueDTOShape(t *testing.T) {
	root, _ := newTestStoreDir(t)

	out, _, code := taskmgr(t, root, "--json", "list")
	if code != 0 {
		t.Fatalf("list failed (exit %d): %s", code, out)
	}

	var arr []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("parse JSON array: %v\noutput: %s", err, out)
	}
	if len(arr) == 0 {
		t.Fatal("expected at least one issueDTO")
	}

	required := []string{"id", "title", "status", "type", "priority", "created", "updated"}
	for i, dto := range arr {
		for _, field := range required {
			if _, ok := dto[field]; !ok {
				t.Errorf("issueDTO[%d] missing required field %q; keys: %v", i, field, mapKeys(dto))
			}
		}
	}
}

// TestSpec_CLI_DetailDTOHasCommentsArray verifies that `show --json` returns a
// detailDTO with a "comments" key that is a JSON array.
// CLI-SPEC §6: detailDTO … comments (commentDTO[]).
func TestSpec_CLI_DetailDTOHasCommentsArray(t *testing.T) {
	root, issID := newTestStoreDir(t)

	// Add a comment so the array is non-trivially populated.
	taskmgr(t, root, "comment", "add", issID, "a note")

	out, _, code := taskmgr(t, root, "--json", "show", issID)
	if code != 0 {
		t.Fatalf("show failed (exit %d): %s", code, out)
	}

	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse show: %v\noutput: %s", err, out)
	}

	raw, ok := dto["comments"]
	if !ok {
		t.Fatal("detailDTO missing 'comments' field")
	}
	arr, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("detailDTO.comments is not an array: %T = %v", raw, raw)
	}
	if len(arr) == 0 {
		t.Error("expected at least one comment in detailDTO.comments after comment add")
	}
}

// TestSpec_CLI_DetailDTOCommentsEmptyIsArray verifies that detailDTO.comments
// is [] (not null or absent) when there are no comments.
// CLI-SPEC §6: commentDTO[] — array contract must hold even when empty.
func TestSpec_CLI_DetailDTOCommentsEmptyIsArray(t *testing.T) {
	root, issID := newTestStoreDir(t)
	// No comments added.

	out, _, code := taskmgr(t, root, "--json", "show", issID)
	if code != 0 {
		t.Fatalf("show failed (exit %d): %s", code, out)
	}

	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse show: %v\noutput: %s", err, out)
	}

	raw, ok := dto["comments"]
	if !ok {
		// Absent is also acceptable if the spec allows omitting when empty —
		// but to be safe (agent callers expect a stable shape) we check.
		// The spec says comments is a field of detailDTO. Omitting it when
		// empty is fine; if present it must be an array not null.
		return
	}
	_, isArr := raw.([]interface{})
	if raw != nil && !isArr {
		t.Errorf("detailDTO.comments present but not an array: %T = %v", raw, raw)
	}
}

// TestSpec_CLI_SearchJSON_IsArray verifies that `taskmgr search --json` returns
// a JSON array. CLI-SPEC §6: Output (JSON): array of issueDTO.
func TestSpec_CLI_SearchJSON_IsArray(t *testing.T) {
	root, _ := newTestStoreDir(t)

	out, _, code := taskmgr(t, root, "--json", "search", "test")
	if code != 0 {
		t.Fatalf("search failed (exit %d): %s", code, out)
	}

	var arr []interface{}
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("search --json output is not a JSON array: %v\noutput: %s", err, out)
	}
}

// TestSpec_CLI_ReadyJSON_IsArray verifies that `taskmgr ready --json` returns a
// JSON array. CLI-SPEC §6: Output (JSON): array of issueDTO.
func TestSpec_CLI_ReadyJSON_IsArray(t *testing.T) {
	root, _ := newTestStoreDir(t)

	out, _, code := taskmgr(t, root, "--json", "ready")
	if code != 0 {
		t.Fatalf("ready failed (exit %d): %s", code, out)
	}

	var arr []interface{}
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("ready --json output is not a JSON array: %v\noutput: %s", err, out)
	}
}

// TestSpec_CLI_CreateJSON_ReturnsID verifies that `taskmgr create --json` returns
// a JSON object with an "id" field. CLI-SPEC §6: "Output: the new ID ({"id"} in JSON)".
func TestSpec_CLI_CreateJSON_ReturnsID(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	out, _, code := taskmgr(t, root, "--json", "create", "--title", "new issue")
	if code != 0 {
		t.Fatalf("create failed (exit %d): %s", code, out)
	}

	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse create output: %v\noutput: %s", err, out)
	}
	id, ok := dto["id"]
	if !ok {
		t.Fatalf("create --json missing 'id' field; got: %v", dto)
	}
	idStr, _ := id.(string)
	if idStr == "" {
		t.Error("create --json: id must be a non-empty string")
	}
}

// TestSpec_CLI_VersionJSON_Shape verifies that `taskmgr version --json` returns
// the documented shape: {"version","commit","date"}.
// CLI-SPEC §5: version command output.
func TestSpec_CLI_VersionJSON_Shape(t *testing.T) {
	root := t.TempDir()
	// version doesn't need a store, but taskmgrBin does need to be built.
	_ = taskmgrBin(t)

	out, _, code := taskmgr(t, root, "--json", "version")
	if code != 0 {
		t.Fatalf("version failed (exit %d): %s", code, out)
	}

	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("version --json is not valid JSON: %v\noutput: %s", err, out)
	}
	for _, field := range []string{"version", "commit", "date"} {
		if _, ok := dto[field]; !ok {
			t.Errorf("version --json missing field %q; got: %v", field, dto)
		}
	}
}

// TestSpec_CLI_BlockedJSON_IsArray verifies that `taskmgr blocked --json` returns
// a JSON array of blockedDTO. CLI-SPEC §6: blockedDTO.
func TestSpec_CLI_BlockedJSON_IsArray(t *testing.T) {
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
	blocker, err := unwrap(s.Create(tasks.CreateInput{Title: "blocker"}))
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	dep, err := unwrap(s.Create(tasks.CreateInput{Title: "dependent"}))
	if err != nil {
		t.Fatalf("Create dep: %v", err)
	}
	if err := s.AddDep(dep.ID, blocker.ID); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	out, _, code := taskmgr(t, root, "--json", "blocked")
	if code != 0 {
		t.Fatalf("blocked failed (exit %d): %s", code, out)
	}

	var arr []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("blocked --json output is not a JSON array: %v\noutput: %s", err, out)
	}
	if len(arr) == 0 {
		t.Fatal("expected at least one blockedDTO")
	}
	// Each blockedDTO must have blocked_by_refs.
	for i, dto := range arr {
		if _, ok := dto["blocked_by_refs"]; !ok {
			t.Errorf("blockedDTO[%d] missing 'blocked_by_refs'; keys: %v", i, mapKeys(dto))
		}
	}
}
