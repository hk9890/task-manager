//go:build integration

// hook_cli_test.go — L4 CLI tests for lifecycle hooks (HOOK-SPEC §6.2): the
// hook_denied JSON, hints/warnings surfacing, fail-closed config, and the
// import --run-hooks flag. Hooks are real `sh -c` scripts in config.yaml,
// executed by the actual taskmgr binary.
package cmd_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// initStoreWithConfig creates a store and overwrites its config.yaml with the
// given content (so a test can declare hooks).
func initStoreWithConfig(t *testing.T, prefix, configYAML string) string {
	t.Helper()
	root := t.TempDir()
	if _, err := tasks.Init(root, prefix); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg := filepath.Join(root, ".tasks", "config.yaml")
	if err := os.WriteFile(cfg, []byte(configYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root
}

// createIssue creates an issue via the CLI and returns its id (last token of
// "Created <id>" on stdout).
func createIssue(t *testing.T, root, title string) string {
	t.Helper()
	out, _, code := taskmgr(t, root, "create", "--title", title)
	if code != 0 {
		t.Fatalf("create: exit %d, out %q", code, out)
	}
	fields := strings.Fields(out)
	return fields[len(fields)-1]
}

func TestL4_PreCloseGate_DeniedJSON(t *testing.T) {
	root := initStoreWithConfig(t, "hk", `prefix: hk
hooks:
  - id: tests
    event: pre-close
    run: ["sh", "-c", "echo '3 tests failing' >&2; exit 1"]
`)
	id := createIssue(t, root, "to close")

	stdout, stderr, code := taskmgr(t, root, "--json", "close", id)
	if code != 1 {
		t.Fatalf("denied close: exit %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(stdout), &dto); err != nil {
		t.Fatalf("stdout is not the hook_denied JSON: %v\n%s", err, stdout)
	}
	if dto["error"] != "hook_denied" || dto["event"] != "pre-close" || dto["hook"] != "tests" {
		t.Errorf("hook_denied JSON = %v", dto)
	}
	if r, _ := dto["reason"].(string); !strings.Contains(r, "3 tests failing") {
		t.Errorf("reason = %q, want the hook's stderr message", r)
	}
	// The issue stayed open (nothing written).
	showOut, _, _ := taskmgr(t, root, "--json", "show", id)
	var iss map[string]any
	_ = json.Unmarshal([]byte(showOut), &iss)
	if iss["status"] == "closed" {
		t.Error("denied close must leave the issue open")
	}
}

func TestL4_PreCloseGate_DeniedTextMode(t *testing.T) {
	root := initStoreWithConfig(t, "hk", `prefix: hk
hooks:
  - id: tests
    event: pre-close
    run: ["sh", "-c", "echo 'not green' >&2; exit 1"]
`)
	id := createIssue(t, root, "to close")

	stdout, stderr, code := taskmgr(t, root, "close", id)
	if code != 1 {
		t.Fatalf("denied close: exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "taskmgr:") || !strings.Contains(stderr, "not green") {
		t.Errorf("stderr = %q, want a taskmgr: message with the reason", stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("text-mode denial must not write to stdout, got %q", stdout)
	}
}

func TestL4_AllowHint_SurfacedInJSON(t *testing.T) {
	root := initStoreWithConfig(t, "hk", `prefix: hk
hooks:
  - id: remind
    event: pre-create
    run: ["sh", "-c", "echo 'remember CHANGELOG'; exit 0"]
`)
	stdout, _, code := taskmgr(t, root, "--json", "create", "--title", "feature")
	if code != 0 {
		t.Fatalf("allowed create: exit %d", code)
	}
	var dto struct {
		ID    string   `json:"id"`
		Hints []string `json:"hints"`
	}
	if err := json.Unmarshal([]byte(stdout), &dto); err != nil {
		t.Fatalf("parse create JSON: %v\n%s", err, stdout)
	}
	if dto.ID == "" {
		t.Error("create JSON must carry the new id")
	}
	if len(dto.Hints) != 1 || dto.Hints[0] != "remember CHANGELOG" {
		t.Errorf("hints = %v, want [remember CHANGELOG]", dto.Hints)
	}
}

func TestL4_PostCloseWarning_DoesNotFailClose(t *testing.T) {
	root := initStoreWithConfig(t, "hk", `prefix: hk
hooks:
  - id: notify
    event: post-close
    run: ["sh", "-c", "echo 'notify failed' >&2; exit 1"]
`)
	id := createIssue(t, root, "to close")

	stdout, _, code := taskmgr(t, root, "--json", "close", id)
	if code != 0 {
		t.Fatalf("post-hook failure must not fail the close: exit %d\n%s", code, stdout)
	}
	var dto struct {
		Status   string   `json:"status"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(stdout), &dto); err != nil {
		t.Fatalf("parse close JSON: %v\n%s", err, stdout)
	}
	if dto.Status != "closed" {
		t.Errorf("issue must be closed, got status %q", dto.Status)
	}
	if len(dto.Warnings) != 1 || !strings.Contains(dto.Warnings[0], "notify") {
		t.Errorf("warnings = %v, want the post-hook failure", dto.Warnings)
	}
}

func TestL4_MalformedHooksConfig_FailsClosedButReadsWork(t *testing.T) {
	root := initStoreWithConfig(t, "hk", `prefix: hk
hooks:
  - event: not-a-real-event
    run: ["true"]
`)
	// A mutation fails closed with a config error.
	_, stderr, code := taskmgr(t, root, "create", "--title", "x")
	if code == 0 {
		t.Fatal("malformed hooks config must fail create closed")
	}
	if !strings.Contains(stderr, "taskmgr:") {
		t.Errorf("expected a taskmgr: config error, got %q", stderr)
	}
	// Reads are unaffected.
	_, _, lcode := taskmgr(t, root, "list")
	if lcode != 0 {
		t.Errorf("list must work despite a malformed hooks config, exit %d", lcode)
	}
}

// TestL4_HookPayload_MatchesCLIIssueShape captures the real stdin payload from a
// hook and verifies its `new` object agrees field-for-field with the CLI's
// issueDTO (`show --json`) — the engine-owned serializer and the CLI DTO are
// contractually the same shape (HOOK-SPEC §5.1).
func TestL4_HookPayload_MatchesCLIIssueShape(t *testing.T) {
	root := initStoreWithConfig(t, "hk", `prefix: hk
hooks:
  - id: capture
    event: post-create
    run: ["sh", "-c", "cat > payload.json"]
`)
	out, _, code := taskmgr(t, root, "create", "--title", "shape", "--label", "area:x", "--priority", "1", "--type", "bug")
	if code != 0 {
		t.Fatalf("create: exit %d, out %q", code, out)
	}
	id := strings.Fields(out)[len(strings.Fields(out))-1]

	data, err := os.ReadFile(filepath.Join(root, "payload.json"))
	if err != nil {
		t.Fatalf("hook did not capture payload: %v", err)
	}
	var pay struct {
		Schema  int            `json:"schema"`
		Event   string         `json:"event"`
		IssueID string         `json:"issue_id"`
		Old     any            `json:"old"`
		New     map[string]any `json:"new"`
	}
	if err := json.Unmarshal(data, &pay); err != nil {
		t.Fatalf("payload not JSON: %v\n%s", err, data)
	}
	if pay.Schema != 1 || pay.Event != "post-create" || pay.IssueID != id || pay.Old != nil {
		t.Errorf("envelope = %+v (want schema 1, post-create, issue_id %s, old null)", pay, id)
	}

	showOut, _, _ := taskmgr(t, root, "--json", "show", id)
	var dto map[string]any
	if err := json.Unmarshal([]byte(showOut), &dto); err != nil {
		t.Fatalf("show JSON: %v", err)
	}
	// The stable issueDTO fields must be identical between the hook payload's
	// `new` and the CLI's rendering.
	for _, k := range []string{"id", "title", "status", "type", "priority", "labels", "created", "updated"} {
		if fmt.Sprint(pay.New[k]) != fmt.Sprint(dto[k]) {
			t.Errorf("field %q diverges: hook payload %v vs CLI issueDTO %v", k, pay.New[k], dto[k])
		}
	}
}

func TestL4_ImportRunHooksFlag(t *testing.T) {
	cfg := `prefix: hk
hooks:
  - id: gate
    event: pre-create
    run: ["sh", "-c", "exit 1"]
`
	envelope := `{"title": "imported"}`
	write := func(root, name, content string) string {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Default: hooks omitted -> import succeeds despite the pre-create deny.
	root := initStoreWithConfig(t, "hk", cfg)
	f := write(root, "env.json", envelope)
	_, stderr, code := taskmgr(t, root, "import", "--file", f)
	if code != 0 {
		t.Fatalf("default import must omit hooks: exit %d, stderr %q", code, stderr)
	}

	// --run-hooks -> the gate applies and the import is denied.
	root = initStoreWithConfig(t, "hk", cfg)
	f = write(root, "env.json", envelope)
	_, _, code = taskmgr(t, root, "import", "--run-hooks", "--file", f)
	if code == 0 {
		t.Fatal("import --run-hooks must be gated by the pre-create hook")
	}
}
