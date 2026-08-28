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

// hook_cli_test.go — L4 CLI tests for lifecycle hooks (HOOK-SPEC §6.2): the
// hook_denied JSON, hints/warnings surfacing, fail-closed config, and the
// import --run-hooks flag. Hooks are real `sh -c` scripts inside a package the
// store uses, executed by the actual taskmgr binary.
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

// initStoreWithPackage creates a store, writes a package holding the given
// manifest hooks inside it, and points the store's `use:` list at that package —
// the shape a repository ships (HOOK-SPEC §3.6).
func initStoreWithPackage(t *testing.T, prefix, hooksYAML string) string {
	t.Helper()
	root := t.TempDir()
	if _, err := tasks.Init(root, prefix); err != nil {
		t.Fatalf("Init: %v", err)
	}
	pkg := filepath.Join(root, ".tasks", "packages", "test")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatalf("mkdir package: %v", err)
	}
	manifest := "version: 1\n" + hooksYAML
	if err := os.WriteFile(filepath.Join(pkg, tasks.PackageManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	cfg := filepath.Join(root, ".tasks", "config.yaml")
	body := fmt.Sprintf("prefix: %s\nuse:\n    - path: packages/test\n", prefix)
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root
}

func TestL4_PreCloseGate_DeniedJSON(t *testing.T) {
	root := initStoreWithPackage(t, "hk", `hooks:
  - id: tests
    event: pre-close
    run: ["sh", "-c", "echo '3 tests failing' >&2; exit 1"]
`)
	id := mkIssue(t, root, "to close")

	stdout, stderr, code := taskmgr(t, root, "--json", "close", id)
	if code != 1 {
		t.Fatalf("denied close: exit %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(stdout), &dto); err != nil {
		t.Fatalf("stdout is not the hook_denied JSON: %v\n%s", err, stdout)
	}
	// The denial names the hook by its effective id, so the reader knows which
	// package to open (HOOK-SPEC §3.5).
	if dto["error"] != "hook_denied" || dto["event"] != "pre-close" || dto["hook"] != "pkg:test:tests" {
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
	root := initStoreWithPackage(t, "hk", `hooks:
  - id: tests
    event: pre-close
    run: ["sh", "-c", "echo 'not green' >&2; exit 1"]
`)
	id := mkIssue(t, root, "to close")

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
	root := initStoreWithPackage(t, "hk", `hooks:
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
	root := initStoreWithPackage(t, "hk", `hooks:
  - id: notify
    event: post-close
    run: ["sh", "-c", "echo 'notify failed' >&2; exit 1"]
`)
	id := mkIssue(t, root, "to close")

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

func TestL4_MalformedPackage_FailsClosedButReadsWork(t *testing.T) {
	root := initStoreWithPackage(t, "hk", `hooks:
  - id: g
    event: not-a-real-event
    run: ["true"]
`)
	// A mutation fails closed with a config error.
	_, stderr, code := taskmgr(t, root, "create", "--title", "x")
	if code == 0 {
		t.Fatal("a malformed package must fail create closed")
	}
	if !strings.Contains(stderr, "taskmgr:") {
		t.Errorf("expected a taskmgr: config error, got %q", stderr)
	}
	// Reads are unaffected.
	_, _, lcode := taskmgr(t, root, "list")
	if lcode != 0 {
		t.Errorf("list must work despite a malformed package, exit %d", lcode)
	}
}

// A `use:` entry naming a package that is not there fails every mutation and
// names what is missing, while reads keep working (HOOK-SPEC §1 principle 4).
func TestL4_MissingPackage_FailsClosedButReadsWork(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "hk"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg := filepath.Join(root, ".tasks", "config.yaml")
	if err := os.WriteFile(cfg, []byte("prefix: hk\nuse:\n    - name: never-installed\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, stderr, code := taskmgr(t, root, "create", "--title", "x")
	if code == 0 {
		t.Fatal("a use entry that does not resolve must fail create closed")
	}
	if !strings.Contains(stderr, "never-installed") {
		t.Errorf("stderr = %q, want it to name the missing package", stderr)
	}
	if _, _, lcode := taskmgr(t, root, "list"); lcode != 0 {
		t.Errorf("list must work despite a missing package, exit %d", lcode)
	}
}

// TestL4_HookPayload_MatchesCLIIssueShape captures the real stdin payload from a
// hook and verifies its `new` object agrees field-for-field with the CLI's
// issueDTO (`show --json`) — the engine-owned serializer and the CLI DTO are
// contractually the same shape (HOOK-SPEC §5.1).
func TestL4_HookPayload_MatchesCLIIssueShape(t *testing.T) {
	root := initStoreWithPackage(t, "hk", `hooks:
  - id: capture
    event: post-create
    run: ["sh", "-c", "cat > payload.json"]
`)
	out, _, code := taskmgr(t, root, "create", "--title", "shape", "--label", "area:x", "--priority", "1", "--type", "bug")
	if code != 0 {
		t.Fatalf("create: exit %d, out %q", code, out)
	}
	id := strings.Fields(out)[len(strings.Fields(out))-1]

	// The working directory is the project root, not the package: only argv[0]
	// is resolved inside a package (HOOK-SPEC §3.6).
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

// A relative argv[0] is found inside the package, so a package ships its own
// script and works wherever it was put (HOOK-SPEC §3.6).
func TestL4_PackageScriptIsFoundInsideThePackage(t *testing.T) {
	root := initStoreWithPackage(t, "hk", `hooks:
  - id: gate
    event: pre-create
    run: ["./deny.sh"]
`)
	script := filepath.Join(root, ".tasks", "packages", "test", "deny.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'package said no' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	_, stderr, code := taskmgr(t, root, "create", "--title", "x")
	if code == 0 {
		t.Fatal("the package's own script must run and deny")
	}
	if !strings.Contains(stderr, "package said no") {
		t.Errorf("stderr = %q, want the script's reason", stderr)
	}
}

func TestL4_ImportRunHooksFlag(t *testing.T) {
	hooks := `hooks:
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
	root := initStoreWithPackage(t, "hk", hooks)
	f := write(root, "env.json", envelope)
	_, stderr, code := taskmgr(t, root, "import", "--file", f)
	if code != 0 {
		t.Fatalf("default import must omit hooks: exit %d, stderr %q", code, stderr)
	}

	// --run-hooks -> the gate applies and the import is denied.
	root = initStoreWithPackage(t, "hk", hooks)
	f = write(root, "env.json", envelope)
	_, _, code = taskmgr(t, root, "import", "--run-hooks", "--file", f)
	if code == 0 {
		t.Fatal("import --run-hooks must be gated by the pre-create hook")
	}
}

// TestL4_ImportRunHooks_HintSurfacedInJSON: `import --run-hooks --json` must
// carry hook hints and warnings like every other mutation (CLI-SPEC §6). It did
// not, and the caller of this command is by definition a migration adapter
// reading JSON — so a hook that had something to say said it to nobody.
func TestL4_ImportRunHooks_HintSurfacedInJSON(t *testing.T) {
	hooks := `hooks:
  - id: remind
    event: pre-create
    run: ["sh", "-c", "echo 'imported via adapter'; exit 0"]
`
	envelope := `{"source_id": "SRC-1", "title": "imported"}`
	writeEnvelope := func(root string) string {
		p := filepath.Join(root, "env.json")
		if err := os.WriteFile(p, []byte(envelope), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Single-envelope mode: one object carrying hints.
	root := initStoreWithPackage(t, "hk", hooks)
	f := writeEnvelope(root)
	stdout, _, code := taskmgr(t, root, "--json", "import", "--run-hooks", "--file", f)
	if code != 0 {
		t.Fatalf("allowed import: exit %d\n%s", code, stdout)
	}
	var one struct {
		ID    string   `json:"id"`
		Hints []string `json:"hints"`
	}
	if err := json.Unmarshal([]byte(stdout), &one); err != nil {
		t.Fatalf("parse import JSON: %v\n%s", err, stdout)
	}
	if one.ID == "" {
		t.Error("import JSON must carry the new id")
	}
	if len(one.Hints) != 1 || one.Hints[0] != "imported via adapter" {
		t.Errorf("hints = %v, want [imported via adapter]", one.Hints)
	}

	// Batch mode: the same hints, per record. This half has no human branch to
	// fall back on, so it is the one where dropping them lost everything.
	root = initStoreWithPackage(t, "hk", hooks)
	f = writeEnvelope(root)
	stdout, _, code = taskmgr(t, root, "--json", "import", "--batch", "--run-hooks", "--file", f)
	if code != 0 {
		t.Fatalf("allowed batch import: exit %d\n%s", code, stdout)
	}
	var batch []struct {
		SourceID string   `json:"source_id"`
		ID       string   `json:"id"`
		Hints    []string `json:"hints"`
	}
	if err := json.Unmarshal([]byte(stdout), &batch); err != nil {
		t.Fatalf("parse batch import JSON: %v\n%s", err, stdout)
	}
	if len(batch) != 1 {
		t.Fatalf("batch results = %d, want 1\n%s", len(batch), stdout)
	}
	if batch[0].SourceID != "SRC-1" || batch[0].ID == "" {
		t.Errorf("batch result = %+v, want the source id mapped to a new id", batch[0])
	}
	if len(batch[0].Hints) != 1 || batch[0].Hints[0] != "imported via adapter" {
		t.Errorf("batch hints = %v, want [imported via adapter]", batch[0].Hints)
	}
}
