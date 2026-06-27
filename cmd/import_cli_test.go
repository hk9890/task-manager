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

// L4 CLI tests for `taskmgr import` — the external-system import primitive.
//
// Coverage:
//   - single envelope on stdin → a closed issue is materialized end-to-end.
//   - --batch NDJSON with edges imports in dependency order.
//   - --batch is best-effort: a bad record is reported, good records still land,
//     and the exit code is non-zero.
package cmd_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// taskmgrStdin runs the built binary with stdin piped.
func taskmgrStdin(t *testing.T, storeDir, stdin string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	bin := taskmgrBin(t)
	cmd := exec.Command(bin, append([]string{"--dir", storeDir}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func initImportStore(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, err := tasks.Init(root, "imp"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return root
}

type cliImportResult struct {
	SourceID string `json:"source_id"`
	ID       string `json:"id"`
	Error    string `json:"error"`
}

func TestL4_Import_SingleClosedWithComment(t *testing.T) {
	root := initImportStore(t)
	env := `{"source_id":"ext-1","title":"old closed task","type":"bug","priority":1,
	  "status":"closed","created_at":"2025-01-02T10:00:00Z","updated_at":"2025-03-01T09:00:00Z",
	  "closed_at":"2025-03-01T09:00:00Z","close_reason":"fixed","labels":["ext:ext-1"],
	  "comments":[{"author":"alice","created_at":"2025-02-01T12:00:00Z","body":"a note"}]}`

	stdout, stderr, code := taskmgrStdin(t, root, env, "import", "--json")
	if code != 0 {
		t.Fatalf("import exit=%d stderr=%q", code, stderr)
	}
	var res cliImportResult
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("bad result json %q: %v", stdout, err)
	}
	if res.SourceID != "ext-1" || res.ID == "" {
		t.Fatalf("result = %+v", res)
	}

	// The issue is present and closed.
	out, errs, code := taskmgr(t, root, "list", "--all", "--json")
	if code != 0 {
		t.Fatalf("list exit=%d stderr=%q", code, errs)
	}
	var issues []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		t.Fatalf("bad list json: %v", err)
	}
	if len(issues) != 1 || issues[0].Status != "closed" {
		t.Fatalf("want 1 closed issue, got %+v", issues)
	}
}

func TestL4_Import_BatchWithEdges(t *testing.T) {
	root := initImportStore(t)
	// Dependency order: epic first (with a known ID), then a child referencing it.
	ndjson := strings.Join([]string{
		`{"source_id":"ext-epic","id":"imp-epic1","title":"epic","type":"epic","status":"open","created_at":"2025-01-01T00:00:00Z"}`,
		`{"source_id":"ext-child","title":"child","parent":"imp-epic1","blocked_by":["imp-epic1"],"status":"in_progress","created_at":"2025-01-05T00:00:00Z"}`,
	}, "\n")

	stdout, stderr, code := taskmgrStdin(t, root, ndjson, "import", "--batch", "--json")
	if code != 0 {
		t.Fatalf("batch import exit=%d stderr=%q", code, stderr)
	}
	var results []cliImportResult
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		t.Fatalf("bad batch json %q: %v", stdout, err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Error != "" || r.ID == "" {
			t.Errorf("record %s failed: %+v", r.SourceID, r)
		}
	}
	if results[0].ID != "imp-epic1" {
		t.Errorf("caller-supplied id not honored: %+v", results[0])
	}
}

func TestL4_Import_BatchBestEffort(t *testing.T) {
	root := initImportStore(t)
	// First record is good; second references a non-existent parent and must fail
	// without taking down the first.
	ndjson := strings.Join([]string{
		`{"source_id":"ok","title":"good one","status":"open"}`,
		`{"source_id":"bad","title":"dangling","parent":"imp-nope","status":"open"}`,
	}, "\n")

	stdout, _, code := taskmgrStdin(t, root, ndjson, "import", "--batch", "--json")
	if code == 0 {
		t.Fatalf("expected non-zero exit when a record fails")
	}
	var results []cliImportResult
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		t.Fatalf("bad batch json %q: %v", stdout, err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	var okCount, errCount int
	for _, r := range results {
		if r.Error == "" && r.ID != "" {
			okCount++
		}
		if r.Error != "" {
			errCount++
		}
	}
	if okCount != 1 || errCount != 1 {
		t.Fatalf("want 1 ok + 1 error, got ok=%d err=%d (%+v)", okCount, errCount, results)
	}
	// The good record really landed.
	out, _, _ := taskmgr(t, root, "list", "--all", "--json")
	var issues []struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(out), &issues)
	if len(issues) != 1 {
		t.Errorf("want 1 surviving issue, got %d", len(issues))
	}
}
