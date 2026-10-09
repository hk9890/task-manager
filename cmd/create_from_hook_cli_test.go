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

// L4: `create --from` against a real pre-create hook. The set must be refused
// as a whole when the gate denies one entry, which only a hook that reads its
// payload can show.
package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const setWithADeniedThirdEntry = `
- title: first
- title: second
- ref: t
  title: deny-me
`

func denyingStoreAndSetFile(t *testing.T) (root, setFile string) {
	t.Helper()
	root = initStoreWithPackage(t, "hk", `hooks:
  - id: gate
    event: pre-create
    run: ["sh", "-c", "if grep -q deny-me; then echo 'not this one' >&2; exit 1; fi"]
`)
	setFile = filepath.Join(t.TempDir(), "set.yaml")
	if err := os.WriteFile(setFile, []byte(setWithADeniedThirdEntry), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, setFile
}

func TestL4_CreateFrom_PreCreateDeniesOneEntry_WritesNothing(t *testing.T) {
	root, setFile := denyingStoreAndSetFile(t)

	stdout, stderr, code := taskmgr(t, root, "create", "--from", setFile)
	if code != 1 || stdout != "" {
		t.Fatalf("exit %d, stdout %q; want 1 and empty", code, stdout)
	}
	for _, want := range []string{`entry 3 (ref "t")`, "pkg:test:gate", "not this one"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if list, _, _ := taskmgr(t, root, "--json", "list", "--all"); strings.TrimSpace(list) != "[]" {
		t.Errorf("a denied set must write nothing; list --all printed:\n%s", list)
	}
}

func TestL4_CreateFrom_PreCreateDenies_JSONNamesTheEntry(t *testing.T) {
	root, setFile := denyingStoreAndSetFile(t)

	stdout, _, code := taskmgr(t, root, "--json", "create", "--from", setFile)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	var dto struct {
		Error   string `json:"error"`
		Entry   int    `json:"entry"`
		Ref     string `json:"ref"`
		IssueID string `json:"issue_id"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(stdout), &dto); err != nil {
		t.Fatalf("stdout is not the hook_denied JSON: %v\n%s", err, stdout)
	}
	if dto.Error != "hook_denied" || dto.Entry != 3 || dto.Ref != "t" || dto.IssueID != "" || dto.Reason != "not this one" {
		t.Errorf("hook_denied = %+v, want entry 3, ref t, no issue_id", dto)
	}
}
