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

// L4 CLI tests for mutation-command guardrails introduced by at-3v9:
//
//   - create: --description and --description-file are mutually exclusive
//   - update: no-op (no flags) exits with error
//   - update: --description and --description-file are mutually exclusive
//   - comment add:  body argument and --file are mutually exclusive
//   - comment edit: body argument and --file are mutually exclusive
package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// writeBodyFile writes content to a temp file and returns its path.
func writeBodyFile(t *testing.T, content string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

// ── create mutually-exclusive description flags ───────────────────────────────

// TestL4_Create_DescAndFileAreMutuallyExclusive verifies that passing both
// --description and --description-file to `create` yields exit 1.
func TestL4_Create_DescAndFileAreMutuallyExclusive(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "mut"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	bodyFile := writeBodyFile(t, "from file")

	_, stderr, code := taskmgr(t, root, "create", "--title", "test issue",
		"--description", "inline",
		"--description-file", bodyFile)
	if code == 0 {
		t.Errorf("create with both --description and --description-file: expected exit 1, got 0")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d; stderr: %s", code, stderr)
	}
}

// ── update: no-op exits with error ────────────────────────────────────────────

// TestL4_Update_NoFlagsIsError verifies that `taskmgr update <id>` with no
// mutating flags exits with code 1 rather than silently bumping updated.
func TestL4_Update_NoFlagsIsError(t *testing.T) {
	root, issID := newTestStoreDir(t)

	_, stderr, code := taskmgr(t, root, "update", issID)
	if code == 0 {
		t.Errorf("update with no flags: expected exit 1, got 0")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d; stderr: %s", code, stderr)
	}
}

// ── update: mutually-exclusive description flags ──────────────────────────────

// TestL4_Update_DescAndFileAreMutuallyExclusive verifies that passing both
// --description and --description-file to `update` yields exit 1.
func TestL4_Update_DescAndFileAreMutuallyExclusive(t *testing.T) {
	root, issID := newTestStoreDir(t)

	bodyFile := writeBodyFile(t, "from file")

	_, stderr, code := taskmgr(t, root, "update", issID,
		"--description", "inline",
		"--description-file", bodyFile)
	if code == 0 {
		t.Errorf("update with both --description and --description-file: expected exit 1, got 0")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d; stderr: %s", code, stderr)
	}
}

// ── comment add: body arg and --file are mutually exclusive ───────────────────

// TestL4_CommentAdd_BodyAndFileAreMutuallyExclusive verifies that passing both
// a positional body and --file to `comment add` yields exit 1.
func TestL4_CommentAdd_BodyAndFileAreMutuallyExclusive(t *testing.T) {
	root, issID := newTestStoreDir(t)

	bodyFile := writeBodyFile(t, "from file")

	_, stderr, code := taskmgr(t, root, "comment", "add", issID,
		"inline body",
		"--file", bodyFile)
	if code == 0 {
		t.Errorf("comment add with both body and --file: expected exit 1, got 0")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d; stderr: %s", code, stderr)
	}
}

// ── comment edit: body arg and --file are mutually exclusive ──────────────────

// TestL4_CommentEdit_BodyAndFileAreMutuallyExclusive verifies that passing both
// a positional body and --file to `comment edit` yields exit 1.
func TestL4_CommentEdit_BodyAndFileAreMutuallyExclusive(t *testing.T) {
	root, issID := newTestStoreDir(t)

	// First add a comment to get a valid comment ID.
	out, _, code := taskmgr(t, root, "--json", "comment", "add", issID, "original")
	if code != 0 {
		t.Fatalf("comment add failed (exit %d): %s", code, out)
	}
	var dto map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse DTO: %v\noutput: %s", err, out)
	}
	commentID, _ := dto["id"].(string)

	bodyFile := writeBodyFile(t, "from file")

	_, stderr, code := taskmgr(t, root, "comment", "edit", issID, commentID,
		"inline body",
		"--file", bodyFile)
	if code == 0 {
		t.Errorf("comment edit with both body and --file: expected exit 1, got 0")
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d; stderr: %s", code, stderr)
	}
}
