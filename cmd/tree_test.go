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

// In-process tests for `taskmgr tree` (CLI-SPEC §3).
package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// treeStore builds the store every tree test reads:
//
//	tst-0001  epic   P0  Export "v2"
//	  tst-0002  task  P1  Define schema
//	  tst-0003  task  P2  Wire up export     blocked by tst-0002 and tst-0004
//	tst-0004  task   P3  Outside blocker
//
// Priorities are distinct so the work order, and with it the output, is fixed.
func treeStore(t *testing.T) (string, *tasks.Store) {
	t.Helper()
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	prio := func(p int) *int { return &p }
	for _, in := range []tasks.CreateInput{
		{ID: "tst-0001", Title: `Export "v2"`, Type: tasks.TypeEpic, Priority: prio(0)},
		{ID: "tst-0002", Title: "Define schema", Priority: prio(1), Parent: "tst-0001"},
		{ID: "tst-0004", Title: "Outside blocker", Priority: prio(3)},
		{ID: "tst-0003", Title: "Wire up export", Priority: prio(2), Parent: "tst-0001", BlockedBy: []string{"tst-0002", "tst-0004"}},
	} {
		if _, err := s.Create(in); err != nil {
			t.Fatalf("Create %s: %v", in.ID, err)
		}
	}
	return root, s
}

func tree(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, stderr, code := run(t, append([]string{"--dir", root, "tree"}, args...)...)
	if code != 0 {
		t.Fatalf("tree %v: exit %d, stderr %s", args, code, stderr)
	}
	return out
}

func TestTree_NestsChildrenAndMarksReadyAndBlocked(t *testing.T) {
	root, _ := treeStore(t)

	want := `tst-0001  open  P0  epic  Export "v2"  [ready]
  tst-0002  open  P1  task  Define schema  [ready]
  tst-0003  open  P2  task  Wire up export  [blocked by tst-0002, tst-0004]
tst-0004  open  P3  task  Outside blocker  [ready]
`
	if got := tree(t, root); got != want {
		t.Errorf("tree printed:\n%s\nwant:\n%s", got, want)
	}
}

func TestTree_WithID_PrintsOnlyThatSubtree(t *testing.T) {
	root, _ := treeStore(t)

	want := `tst-0001  open  P0  epic  Export "v2"  [ready]
  tst-0002  open  P1  task  Define schema  [ready]
  tst-0003  open  P2  task  Wire up export  [blocked by tst-0002, tst-0004]
`
	if got := tree(t, root, "tst-0001"); got != want {
		t.Errorf("tree tst-0001 printed:\n%s\nwant:\n%s", got, want)
	}
}

// A blocker outside the subtree still gets a node, and a title's quotes cannot
// end the label they sit in.
func TestTree_Mermaid_DeclaresEveryNodeAnEdgeNames(t *testing.T) {
	root, _ := treeStore(t)

	want := `graph TD
  tst_0001["tst-0001: Export #quot;v2#quot; (epic, open, ready)"]
  tst_0002["tst-0002: Define schema (task, open, ready)"]
  tst_0003["tst-0003: Wire up export (task, open, blocked by tst-0002, tst-0004)"]
  tst_0004["tst-0004: Outside blocker (task, open)"]
  tst_0001 --> tst_0002
  tst_0001 --> tst_0003
  tst_0002 -.->|blocks| tst_0003
  tst_0004 -.->|blocks| tst_0003
`
	if got := tree(t, root, "tst-0001", "--format", "mermaid"); got != want {
		t.Errorf("tree --format mermaid printed:\n%s\nwant:\n%s", got, want)
	}
}

func TestTree_JSON_NestsChildrenWithTheDerivedMarks(t *testing.T) {
	root, _ := treeStore(t)

	type node struct {
		ID       string `json:"id"`
		Ready    bool   `json:"ready"`
		Blocked  bool   `json:"blocked"`
		Children []node `json:"children"`
	}
	var got []node
	if err := json.Unmarshal([]byte(tree(t, root, "--json")), &got); err != nil {
		t.Fatalf("parse tree JSON: %v", err)
	}
	if len(got) != 2 || got[0].ID != "tst-0001" || got[1].ID != "tst-0004" {
		t.Fatalf("roots = %+v, want tst-0001 then tst-0004", got)
	}
	kids := got[0].Children
	if len(kids) != 2 || kids[0].ID != "tst-0002" || kids[1].ID != "tst-0003" {
		t.Fatalf("children of tst-0001 = %+v, want tst-0002 then tst-0003", kids)
	}
	if !kids[0].Ready || kids[0].Blocked {
		t.Errorf("tst-0002 = %+v, want ready and not blocked", kids[0])
	}
	if kids[1].Ready || !kids[1].Blocked {
		t.Errorf("tst-0003 = %+v, want blocked and not ready", kids[1])
	}
}

func TestTree_EmptyStore(t *testing.T) {
	root := newStore(t)

	if got := tree(t, root); got != "(none)\n" {
		t.Errorf("text = %q, want %q", got, "(none)\n")
	}
	if got := strings.TrimSpace(tree(t, root, "--json")); got != "[]" {
		t.Errorf("JSON = %q, want []", got)
	}
	if got := tree(t, root, "--format", "mermaid"); got != "graph TD\n" {
		t.Errorf("mermaid = %q, want %q", got, "graph TD\n")
	}
}

// A closed parent is not in the open set, so its open children are roots; named
// as the root it still heads them.
func TestTree_ClosedParent(t *testing.T) {
	root, s := treeStore(t)
	if _, err := s.Close("tst-0001", "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}

	bare := tree(t, root)
	if strings.Contains(bare, "tst-0001") {
		t.Errorf("a closed epic was printed:\n%s", bare)
	}
	if !strings.HasPrefix(bare, "tst-0002  ") {
		t.Errorf("the child of a closed epic is not a root:\n%s", bare)
	}

	named := tree(t, root, "tst-0001")
	if !strings.HasPrefix(named, "tst-0001  closed  ") || !strings.Contains(named, "\n  tst-0002  ") {
		t.Errorf("tree tst-0001 does not head its open children with the closed epic:\n%s", named)
	}
}

// The engine accepts a parent cycle longer than one issue. Neither member has a
// root above it, and each must still print exactly once.
func TestTree_ParentCycle_PrintsEachIssueOnce(t *testing.T) {
	root, s := treeStore(t)
	parent := "tst-0002"
	if _, err := s.Update("tst-0001", tasks.UpdateInput{Parent: &parent}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	out := tree(t, root)
	for _, id := range []string{"tst-0001", "tst-0002", "tst-0003", "tst-0004"} {
		if n := strings.Count(out, id+"  "); n != 1 {
			t.Errorf("%s has %d rows, want 1:\n%s", id, n, out)
		}
	}
}

func TestTree_UnknownFormat_ExitsOne(t *testing.T) {
	root, _ := treeStore(t)

	out, stderr, code := run(t, "--dir", root, "tree", "--format", "dot")
	if code != 1 || out != "" {
		t.Fatalf("exit = %d, stdout = %q; want 1 and empty", code, out)
	}
	if !strings.Contains(stderr, "text, mermaid") {
		t.Errorf("stderr does not name the accepted values: %s", stderr)
	}
}

func TestTree_MermaidWithJSON_ExitsOne(t *testing.T) {
	root, _ := treeStore(t)

	out, stderr, code := run(t, "--dir", root, "--json", "tree", "--format", "mermaid")
	if code != 1 || out != "" {
		t.Fatalf("exit = %d, stdout = %q; want 1 and empty", code, out)
	}
	if !strings.Contains(stderr, "--json") {
		t.Errorf("stderr does not name the conflict: %s", stderr)
	}
}

func TestTree_UnknownID_ExitsOne(t *testing.T) {
	root, _ := treeStore(t)

	if _, _, code := run(t, "--dir", root, "tree", "tst-9999"); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}
