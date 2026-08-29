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

// L3 tests for the shared invariant checker itself.
//
// Every SDK test that asserts cross-issue consistency delegates to
// AssertStoreInvariants, so a checker that stopped detecting anything would turn
// all of them into no-ops at once and none of them would notice. These tests
// give it one deliberately broken store per invariant and require it to say so.
//
// They are L3 because the checker reads the closed partition through the real
// filesystem, and because a store broken in these ways can only be built by
// writing the bytes directly — which is what RawFixture is for.
package storetest

import (
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// issueMD returns a well-formed issue .md. extra lines are appended into the
// frontmatter verbatim, so a test can add a reference the store would refuse.
func issueMD(id, status string, extra ...string) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("id: " + id + "\n")
	b.WriteString("title: " + id + "\n")
	b.WriteString("status: " + status + "\n")
	b.WriteString("type: task\n")
	b.WriteString("priority: 2\n")
	b.WriteString("created: 2026-06-01T00:00:00Z\n")
	b.WriteString("updated: 2026-06-01T00:00:00Z\n")
	if status == "closed" {
		b.WriteString("closed: 2026-06-02T00:00:00Z\n")
	}
	for _, line := range extra {
		b.WriteString(line + "\n")
	}
	b.WriteString("---\n")
	return []byte(b.String())
}

// openStore materialises a raw fixture and opens it.
func openStore(t *testing.T, write func(rf *RawFixture)) *tasks.Store {
	t.Helper()
	rf := NewRawFixture(t, t.TempDir())
	write(rf)
	s, err := tasks.Open(rf.Dir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

// check runs the checker and fails the test if it could not read the store.
func check(t *testing.T, s *tasks.Store) []string {
	t.Helper()
	got, err := storeInvariantViolations(s)
	if err != nil {
		t.Fatalf("storeInvariantViolations: %v", err)
	}
	return got
}

// wantViolation fails unless exactly one violation was reported and it contains
// substr. Requiring exactly one is what keeps a checker that reports everything
// from passing every case in this file.
func wantViolation(t *testing.T, got []string, substr string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("got %d violations %q, want exactly 1 containing %q", len(got), got, substr)
	}
	if !strings.Contains(got[0], substr) {
		t.Errorf("violation = %q, want one containing %q", got[0], substr)
	}
}

// A healthy store built through the API reports nothing. Without this case a
// checker that reported a violation unconditionally would pass every other test
// in this file.
func TestL3_Invariants_HealthyStoreIsQuiet(t *testing.T) {
	s := New(t).
		Issue("tst-0001").
		Issue("tst-0002", Parent("tst-0001")).
		Closed("tst-0003").
		TempDir(t)

	if got := check(t, s); len(got) != 0 {
		t.Errorf("a healthy store reported %q, want no violations", got)
	}
}

// A store that has never closed anything has no closed/ directory at all. The
// checker must read that as an empty closed set, not as a failure — otherwise it
// errors out before checking anything on the majority of fixtures.
func TestL3_Invariants_MissingClosedDirIsNotAViolation(t *testing.T) {
	s := openStore(t, func(rf *RawFixture) {
		rf.WriteIssue("tst-0001.md", issueMD("tst-0001", "open"))
	})

	got, err := storeInvariantViolations(s)
	if err != nil {
		t.Fatalf("a store with no closed/ directory must not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %q, want no violations", got)
	}
}

// Invariant 1 (root cause C1): the same ID in both partitions. This is the state
// an interrupted close leaves, and the one the checker exists for.
func TestL3_Invariants_DetectsSplitBrain(t *testing.T) {
	s := openStore(t, func(rf *RawFixture) {
		rf.WriteIssue("tst-0001.md", issueMD("tst-0001", "open"))
		rf.WriteIssue("closed/tst-0001.md", issueMD("tst-0001", "closed"))
	})

	wantViolation(t, check(t, s), "split-brain")
}

// Invariant 2: two files in one partition carrying the same ID. The scan keys on
// the frontmatter, not the filename, so this is what a copied file looks like.
func TestL3_Invariants_DetectsDuplicateIDsInAPartition(t *testing.T) {
	s := openStore(t, func(rf *RawFixture) {
		rf.WriteIssue("tst-0001.md", issueMD("tst-0001", "open"))
		rf.WriteIssue("tst-0002.md", issueMD("tst-0001", "open"))
	})

	wantViolation(t, check(t, s), "duplicate ID")
}

// Invariant 3: a closed-status issue sitting in the hot partition. The partition
// and the status field must agree; List carries its own guard for the same state.
func TestL3_Invariants_DetectsClosedStatusInTheHotPartition(t *testing.T) {
	s := openStore(t, func(rf *RawFixture) {
		rf.WriteIssue("tst-0001.md", issueMD("tst-0001", "closed"))
	})

	wantViolation(t, check(t, s), "closed issues must live in closed/")
}

// Invariant 4: a reference to an ID that is in neither partition. Each of the
// three edge kinds is checked separately — they are three loops, and one of them
// could stop working on its own.
func TestL3_Invariants_DetectsDanglingRefs(t *testing.T) {
	cases := []struct {
		name  string
		extra string
		want  string
	}{
		{"parent", "parent: tst-9999", "dangling parent ref"},
		{"blocked_by", "blocked_by: [tst-9999]", "dangling blocked_by ref"},
		{"related", "related: [tst-9999]", "dangling related ref"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openStore(t, func(rf *RawFixture) {
				rf.WriteIssue("tst-0001.md", issueMD("tst-0001", "open", tc.extra))
			})
			wantViolation(t, check(t, s), tc.want)
		})
	}
}

// A reference that resolves into closed/ is legitimate: work is routinely
// blocked by something already finished. Without this case a checker that
// ignored the closed partition when resolving refs would pass every test above.
func TestL3_Invariants_ARefIntoClosedResolves(t *testing.T) {
	s := openStore(t, func(rf *RawFixture) {
		rf.WriteIssue("tst-0001.md", issueMD("tst-0001", "open", "blocked_by: [tst-0002]"))
		rf.WriteIssue("closed/tst-0002.md", issueMD("tst-0002", "closed"))
	})

	if got := check(t, s); len(got) != 0 {
		t.Errorf("a reference into closed/ reported %q, want no violations", got)
	}
}
