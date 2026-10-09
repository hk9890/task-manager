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

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const epicWithTwoChildren = `
- ref: e
  title: The epic
  type: epic
- ref: first
  title: First child
  parent: e
  labels: [area:x]
  description: |
    line one
    line two
- title: Second child
  priority: 1
  parent: e
  blocked_by: [first]
`

// The same set with every edge pointing at an entry further down the file.
const epicWithTwoChildrenReversed = `
- title: Second child
  priority: 1
  parent: e
  blocked_by: [first]
- ref: first
  title: First child
  parent: e
- ref: e
  title: The epic
  type: epic
`

func writeSetFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "set.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type createdEntry struct {
	Ref string `json:"ref"`
	ID  string `json:"id"`
}

// createFromFile runs `create --from` and returns the entries by title-independent
// position, as the command printed them.
func createFromFile(t *testing.T, root, content string, extra ...string) []createdEntry {
	t.Helper()
	args := append([]string{"--dir", root, "--json", "create", "--from", writeSetFile(t, content)}, extra...)
	out, errOut, code := run(t, args...)
	if code != 0 {
		t.Fatalf("create --from: exit %d, stderr %q", code, errOut)
	}
	var created []createdEntry
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	return created
}

type shownIssue struct {
	Title       string   `json:"title"`
	Type        string   `json:"type"`
	Priority    int      `json:"priority"`
	Parent      string   `json:"parent"`
	BlockedBy   []string `json:"blocked_by"`
	Labels      []string `json:"labels"`
	Description string   `json:"description"`
	Agent       string   `json:"agent"`
	Session     string   `json:"session"`
}

func showIssue(t *testing.T, root, id string) shownIssue {
	t.Helper()
	out, errOut, code := run(t, "--dir", root, "--json", "show", id)
	if code != 0 {
		t.Fatalf("show %s: exit %d, stderr %q", id, code, errOut)
	}
	var iss shownIssue
	if err := json.Unmarshal([]byte(out), &iss); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	return iss
}

func openIssueCount(t *testing.T, root string) int {
	t.Helper()
	out, _, code := run(t, "--dir", root, "--json", "list", "--all")
	if code != 0 {
		t.Fatalf("list: exit %d", code)
	}
	var issues []json.RawMessage
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	return len(issues)
}

func TestCreateFrom_EpicAndChildren_BuildsTheGraph(t *testing.T) {
	root := newStore(t)
	created := createFromFile(t, root, epicWithTwoChildren)
	if len(created) != 3 || created[0].Ref != "e" || created[1].Ref != "first" || created[2].Ref != "" {
		t.Fatalf("want three results in file order with their refs, got %+v", created)
	}

	first, second := showIssue(t, root, created[1].ID), showIssue(t, root, created[2].ID)
	if second.Parent != created[0].ID || len(second.BlockedBy) != 1 || second.BlockedBy[0] != created[1].ID || second.Priority != 1 {
		t.Errorf("second child = %+v, want parent %s, blocker %s, priority 1", second, created[0].ID, created[1].ID)
	}
	if first.Description != "line one\nline two" || len(first.Labels) != 1 || first.Labels[0] != "area:x" {
		t.Errorf("first child = %+v, want the two-line body and the label", first)
	}
	if epic := showIssue(t, root, created[0].ID); epic.Type != "epic" {
		t.Errorf("epic type = %q", epic.Type)
	}
}

func TestCreateFrom_EntriesInReverseOrder_GiveTheSameGraph(t *testing.T) {
	root := newStore(t)
	created := createFromFile(t, root, epicWithTwoChildrenReversed)

	second := showIssue(t, root, created[0].ID)
	if second.Parent != created[2].ID || len(second.BlockedBy) != 1 || second.BlockedBy[0] != created[1].ID {
		t.Errorf("second child = %+v, want parent %s and blocker %s", second, created[2].ID, created[1].ID)
	}
}

func TestCreateFrom_EdgeToAnExistingIssue_IsAccepted(t *testing.T) {
	root := newStore(t)
	existing := createIssue(t, root)
	created := createFromFile(t, root, "- title: depends on old work\n  blocked_by: ["+existing+"]\n")

	if got := showIssue(t, root, created[0].ID).BlockedBy; len(got) != 1 || got[0] != existing {
		t.Errorf("blocked_by = %v, want [%s]", got, existing)
	}
}

func TestCreateFrom_RefusedEntry_WritesNothingAndNamesIt(t *testing.T) {
	cases := map[string]struct{ file, want string }{
		"unknown edge": {"- title: ok\n- ref: b\n  title: dangling\n  blocked_by: [nope]\n", `entry 2 (ref "b")`},
		"cycle":        {"- ref: a\n  title: a\n  blocked_by: [b]\n- ref: b\n  title: b\n  blocked_by: [a]\n", "dependency cycle"},
		"no title":     {"- title: ok\n- type: bug\n", "entry 2"},
		"empty file":   {"", "the set is empty"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := newStore(t)
			out, errOut, code := run(t, "--dir", root, "create", "--from", writeSetFile(t, c.file))
			if code != 1 || out != "" {
				t.Errorf("exit %d, stdout %q; want 1 and empty", code, out)
			}
			if !strings.Contains(errOut, c.want) {
				t.Errorf("stderr %q does not contain %q", errOut, c.want)
			}
			if n := openIssueCount(t, root); n != 0 {
				t.Errorf("a refused set wrote %d issues", n)
			}
		})
	}
}

func TestCreateFrom_MisspeltKey_IsRefused(t *testing.T) {
	root := newStore(t)
	_, errOut, code := run(t, "--dir", root, "create", "--from", writeSetFile(t, "- title: a\n  blocked-by: [b]\n"))
	if code != 1 || !strings.Contains(errOut, "blocked-by") {
		t.Errorf("exit %d, stderr %q; a key the file format does not have must fail and be named", code, errOut)
	}
	if n := openIssueCount(t, root); n != 0 {
		t.Errorf("wrote %d issues", n)
	}
}

func TestCreateFrom_WithAPerIssueFlag_IsMisuse(t *testing.T) {
	root := newStore(t)
	out, errOut, code := run(t, "--dir", root, "create", "--from", writeSetFile(t, epicWithTwoChildren), "--title", "x")
	if code != 1 || out != "" {
		t.Fatalf("exit %d, stdout %q; want 1 and empty", code, out)
	}
	for _, want := range []string{"--from", "--title", "usage:"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
	if n := openIssueCount(t, root); n != 0 {
		t.Errorf("wrote %d issues", n)
	}
}

func TestCreateFrom_AgentAndSession_ApplyToEveryIssue(t *testing.T) {
	root := newStore(t)
	created := createFromFile(t, root, epicWithTwoChildren, "--agent", "test-agent", "--session", "s-1")
	for _, c := range created {
		if iss := showIssue(t, root, c.ID); iss.Agent != "test-agent" || iss.Session != "s-1" {
			t.Errorf("%s: agent %q session %q, want test-agent and s-1", c.ID, iss.Agent, iss.Session)
		}
	}
}

func TestCreateFrom_HumanOutput_OneLinePerIssueWithItsRef(t *testing.T) {
	root := newStore(t)
	out, _, code := run(t, "--dir", root, "create", "--from", writeSetFile(t, epicWithTwoChildren))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "Created tst-") || !strings.HasSuffix(lines[0], "(e)") || strings.Contains(lines[2], "(") {
		t.Errorf("want three 'Created <id>' lines, the ref in brackets where there is one; got:\n%s", out)
	}
}

// --title stays required for the single-issue form; --from only lifts it for itself.
func TestCreate_WithoutTitleOrFrom_StillNamesTheMissingFlag(t *testing.T) {
	root := newStore(t)
	_, errOut, code := run(t, "--dir", root, "create")
	if code != 1 || !strings.Contains(errOut, "--title") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}
