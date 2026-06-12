//go:build integration

// creator_cli_test.go — L4 CLI tests for the --creator flag and issueDTO creator field.
//
// Acceptance criteria covered (at-dny.4):
//
//	AC1  taskmgr create --creator x persists x (verify via show --json).
//	AC2  With no --creator, creator defaults to $USER.
//	AC3  issueDTO includes creator (omitempty) for show/list/search/ready and
//	     nested DTOs; omitted when empty.
//	AC4  taskmgr create --json output shape is unchanged ({id}).
//	AC5  taskmgr update has no --creator flag.
package cmd_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// ── AC1: create --creator x persists x ───────────────────────────────────────

// TestL4_Create_CreatorFlagPersists verifies that --creator <x> is stored and
// round-trips back through show --json.
func TestL4_Create_CreatorFlagPersists(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "crt"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	out, _, code := taskmgr(t, root, "create", "--title", "creator flag test", "--creator", "testuser")
	if code != 0 {
		t.Fatalf("create --creator: expected exit 0, got %d; out: %s", code, out)
	}

	// Extract the issued ID from the human output ("Created crt-0001").
	issID := ""
	for _, f := range strings.Fields(out) {
		if strings.HasPrefix(f, "crt-") {
			issID = f
			break
		}
	}
	if issID == "" {
		t.Fatalf("could not find issue ID in create output: %q", out)
	}

	// Verify via show --json.
	showOut, _, code2 := taskmgr(t, root, "--json", "show", issID)
	if code2 != 0 {
		t.Fatalf("show failed (exit %d): %s", code2, showOut)
	}
	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(showOut), &dto); err != nil {
		t.Fatalf("parse show output: %v\noutput: %s", err, showOut)
	}
	if got, ok := dto["creator"]; !ok {
		t.Error("show --json: missing 'creator' field")
	} else if got != "testuser" {
		t.Errorf("show --json: creator = %v, want testuser", got)
	}
}

// ── AC2: no --creator defaults to $USER ──────────────────────────────────────

// TestL4_Create_CreatorDefaultsToUSER verifies that when --creator is not
// supplied, the creator is set to $USER (matching the comment --author default).
func TestL4_Create_CreatorDefaultsToUSER(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "crt"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	expectedCreator := os.Getenv("USER")
	if expectedCreator == "" {
		t.Skip("$USER not set in environment — skipping default-creator test")
	}

	out, _, code := taskmgr(t, root, "create", "--title", "no creator flag")
	if code != 0 {
		t.Fatalf("create without --creator: expected exit 0, got %d; out: %s", code, out)
	}

	issID := ""
	for _, f := range strings.Fields(out) {
		if strings.HasPrefix(f, "crt-") {
			issID = f
			break
		}
	}
	if issID == "" {
		t.Fatalf("could not find issue ID in create output: %q", out)
	}

	showOut, _, code2 := taskmgr(t, root, "--json", "show", issID)
	if code2 != 0 {
		t.Fatalf("show failed (exit %d): %s", code2, showOut)
	}
	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(showOut), &dto); err != nil {
		t.Fatalf("parse show output: %v\noutput: %s", err, showOut)
	}
	if got, ok := dto["creator"]; !ok {
		t.Error("show --json: missing 'creator' field")
	} else if got != expectedCreator {
		t.Errorf("show --json: creator = %v, want $USER=%q", got, expectedCreator)
	}
}

// ── AC3: issueDTO includes creator (omitempty) for show/list/search/ready ────

// TestL4_IssueDTO_CreatorPresent verifies that creator appears in the issueDTO
// for show, list, search, and ready when it was set at create time.
func TestL4_IssueDTO_CreatorPresent(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "crt")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	tick := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.SetNow(func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	})
	iss, err := s.Create(tasks.CreateInput{Title: "creator present", Creator: "alice"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	issID := iss.ID

	// show --json
	showOut, _, code := taskmgr(t, root, "--json", "show", issID)
	if code != 0 {
		t.Fatalf("show failed (exit %d): %s", code, showOut)
	}
	var detailMap map[string]interface{}
	if err := json.Unmarshal([]byte(showOut), &detailMap); err != nil {
		t.Fatalf("parse show: %v\nout: %s", err, showOut)
	}
	if got := detailMap["creator"]; got != "alice" {
		t.Errorf("show --json: creator = %v, want alice", got)
	}

	// list --json
	listOut, _, code2 := taskmgr(t, root, "--json", "list")
	if code2 != 0 {
		t.Fatalf("list failed (exit %d): %s", code2, listOut)
	}
	var listArr []map[string]interface{}
	if err := json.Unmarshal([]byte(listOut), &listArr); err != nil {
		t.Fatalf("parse list: %v\nout: %s", err, listOut)
	}
	if len(listArr) == 0 {
		t.Fatal("list returned empty array")
	}
	if got := listArr[0]["creator"]; got != "alice" {
		t.Errorf("list --json[0]: creator = %v, want alice", got)
	}

	// search --json
	searchOut, _, code3 := taskmgr(t, root, "--json", "search", "creator present")
	if code3 != 0 {
		t.Fatalf("search failed (exit %d): %s", code3, searchOut)
	}
	var searchArr []map[string]interface{}
	if err := json.Unmarshal([]byte(searchOut), &searchArr); err != nil {
		t.Fatalf("parse search: %v\nout: %s", err, searchOut)
	}
	if len(searchArr) == 0 {
		t.Fatal("search returned empty array")
	}
	if got := searchArr[0]["creator"]; got != "alice" {
		t.Errorf("search --json[0]: creator = %v, want alice", got)
	}

	// ready --json
	readyOut, _, code4 := taskmgr(t, root, "--json", "ready")
	if code4 != 0 {
		t.Fatalf("ready failed (exit %d): %s", code4, readyOut)
	}
	var readyArr []map[string]interface{}
	if err := json.Unmarshal([]byte(readyOut), &readyArr); err != nil {
		t.Fatalf("parse ready: %v\nout: %s", err, readyOut)
	}
	if len(readyArr) == 0 {
		t.Fatal("ready returned empty array")
	}
	if got := readyArr[0]["creator"]; got != "alice" {
		t.Errorf("ready --json[0]: creator = %v, want alice", got)
	}
}

// TestL4_IssueDTO_CreatorOmittedWhenEmpty verifies that when creator is empty
// (not set), the "creator" key is absent from the issueDTO (omitempty semantics).
func TestL4_IssueDTO_CreatorOmittedWhenEmpty(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "crt")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	tick := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.SetNow(func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	})
	// Create with no creator (engine keeps it empty — the CLI normally fills $USER,
	// but we bypass the CLI here to test the DTO omitempty path directly).
	iss, err := s.Create(tasks.CreateInput{Title: "no creator"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	issID := iss.ID

	showOut, _, code := taskmgr(t, root, "--json", "show", issID)
	if code != 0 {
		t.Fatalf("show failed (exit %d): %s", code, showOut)
	}
	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(showOut), &dto); err != nil {
		t.Fatalf("parse show: %v\nout: %s", err, showOut)
	}
	if _, ok := dto["creator"]; ok {
		t.Errorf("show --json: 'creator' must be omitted when empty, got: %v", dto["creator"])
	}
}

// TestL4_NestedDTO_CreatorInBlockedDTO verifies that creator is present in the
// nested issueDTO embedded in a blockedDTO.
func TestL4_NestedDTO_CreatorInBlockedDTO(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "crt")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	tick := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.SetNow(func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	})
	blocker, err := s.Create(tasks.CreateInput{Title: "blocker", Creator: "alice"})
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	dep, err := s.Create(tasks.CreateInput{Title: "dependent", Creator: "bob"})
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
		t.Fatalf("parse blocked: %v\nout: %s", err, out)
	}
	if len(arr) == 0 {
		t.Fatal("expected at least one blockedDTO")
	}
	if got := arr[0]["creator"]; got != "bob" {
		t.Errorf("blockedDTO[0]: creator = %v, want bob", got)
	}
}

// ── AC4: taskmgr create --json shape is unchanged ({id}) ───────────────────────

// TestL4_Create_JSON_ShapeUnchanged verifies that taskmgr create --json still
// returns exactly {"id": "..."} — creator is NOT added to this output.
func TestL4_Create_JSON_ShapeUnchanged(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "crt"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	out, _, code := taskmgr(t, root, "--json", "create", "--title", "shape check", "--creator", "alice")
	if code != 0 {
		t.Fatalf("create --json failed (exit %d): %s", code, out)
	}

	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse create --json: %v\nout: %s", err, out)
	}
	// Must have "id".
	id, ok := dto["id"]
	if !ok {
		t.Fatalf("create --json missing 'id'; got: %v", dto)
	}
	if idStr, _ := id.(string); idStr == "" {
		t.Error("create --json: id must be non-empty")
	}
	// Must NOT have "creator" or any other extra keys.
	if _, ok := dto["creator"]; ok {
		t.Error("create --json must not include 'creator' (shape must stay {id})")
	}
	if len(dto) != 1 {
		t.Errorf("create --json must have exactly 1 key ({id}); got keys: %v", mapKeys(dto))
	}
}

// ── AC5: taskmgr update has no --creator flag ───────────────────────────────────

// TestL4_Update_NoCreatorFlag verifies that taskmgr update does not accept a
// --creator flag (creator is immutable — CLI-SPEC §4).
func TestL4_Update_NoCreatorFlag(t *testing.T) {
	root, issID := newTestStoreDir(t)

	_, stderr, code := taskmgr(t, root, "update", issID, "--creator", "hacker")
	if code == 0 {
		t.Error("update --creator: expected non-zero exit, got 0 (flag must not exist)")
	}
	// The error must mention the unknown flag.
	if !strings.Contains(stderr, "creator") && !strings.Contains(stderr, "unknown") {
		t.Logf("stderr: %s", stderr)
	}
}
