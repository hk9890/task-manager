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

// L4 CLI tests for `taskmgr guide` — the binary-owned how-to.
//
// Coverage:
//   - Human output exits 0 and teaches the core loop and where to find more.
//   - --json wraps the same text as {"guide": "..."}.
//   - guide needs no store (it is reachable before `init`).
package cmd_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestL4_Guide_Human(t *testing.T) {
	root := t.TempDir() // no store required; guide must work anywhere.
	stdout, stderr, code := taskmgr(t, root, "guide")
	if code != 0 {
		t.Fatalf("guide exit=%d stderr=%q", code, stderr)
	}
	// It must teach the model, the loop, and — above all — how to get more info.
	for _, want := range []string{
		"The core loop",
		"taskmgr create --title",
		"Finding work with filters",
		"Get more information",
		"taskmgr commands",
		"taskmgr <command> --help",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("guide output missing %q\n---\n%s", want, stdout)
		}
	}
}

func TestL4_Guide_JSON(t *testing.T) {
	root := t.TempDir()
	stdout, stderr, code := taskmgr(t, root, "--json", "guide")
	if code != 0 {
		t.Fatalf("guide --json exit=%d stderr=%q", code, stderr)
	}
	var obj map[string]string
	if err := json.Unmarshal([]byte(stdout), &obj); err != nil {
		t.Fatalf("guide --json is not valid JSON: %v\n---\n%s", err, stdout)
	}
	if strings.TrimSpace(obj["guide"]) == "" {
		t.Errorf("guide --json: 'guide' field is empty; got keys %v", keysOf(obj))
	}
	if !strings.Contains(obj["guide"], "The core loop") {
		t.Errorf("guide --json: 'guide' text missing core content")
	}
}

func keysOf(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
