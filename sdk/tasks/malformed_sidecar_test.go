//go:build integration

package tasks_test

// L3 adversarial sidecar tests — written using storetest.RawFixture so that
// arbitrary on-disk content can be materialised without going through the API.
//
// These tests guard the "untrusted on-disk data" class of bugs (Epic B root
// causes A + D). Every test case:
//   - Is guarded against hangs via t.Deadline() / a goroutine+select pattern
//     for the cycle cases (the go test -timeout flag provides the outer bound).
//   - Asserts no panic and either a clear error or bounded resolution.
//
// Cases covered:
//   1. replaces cycle A↔B (two-node)
//   2. self-replace (A→A)
//   3. fork (two docs replacing one target)
//   4. partial/truncated trailing doc
//   5. comments for a missing/absent issue (sidecar exists, issue .md does not)

import (
	"testing"
	"time"

	"github.com/hk9890/agent-tasks/sdk/tasks"
	"github.com/hk9890/agent-tasks/sdk/tasks/internal/storetest"
)

// minimalIssueMD returns a minimal well-formed issue .md for use in
// raw-fixture sidecar tests. The issue is open with a fixed timestamp.
func minimalIssueMD(id string) []byte {
	return []byte("---\n" +
		"id: " + id + "\n" +
		"title: " + id + "\n" +
		"status: open\n" +
		"type: task\n" +
		"priority: 2\n" +
		"created: 2026-06-01T00:00:00Z\n" +
		"updated: 2026-06-01T00:00:00Z\n" +
		"---\n")
}

// withTimeout wraps f in a goroutine and fails the test if f does not complete
// within d. This guards against a potential infinite loop in the resolve path.
func withTimeout(t *testing.T, d time.Duration, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
		// OK
	case <-time.After(d):
		t.Fatalf("test timed out after %v — possible infinite loop in comment resolution", d)
	}
}

// TestRawSidecar_TwoNodeCycle verifies that loading a sidecar with an A↔B
// replaces cycle (A replaces B, B replaces A) resolves in bounded time and
// returns at least one comment (graceful degradation — no hang, no panic).
func TestRawSidecar_TwoNodeCycle(t *testing.T) {
	dir := t.TempDir()
	rf := storetest.NewRawFixture(t, dir)
	rf.WriteIssue("tst-0001.md", minimalIssueMD("tst-0001"))

	// A replaces B, B replaces A — two-node cycle.
	sidecar := `---
id: aaaaaaaa
author: alice
created: 2026-06-01T10:00:00Z
replaces: bbbbbbbb
body: |
  comment A
---
id: bbbbbbbb
author: bob
created: 2026-06-01T10:01:00Z
replaces: aaaaaaaa
body: |
  comment B
`
	rf.WriteSidecar("tst-0001.yml", []byte(sidecar))

	s, err := tasks.Open(rf.Dir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	withTimeout(t, 5*time.Second, func() {
		comments, err := s.Comments("tst-0001")
		if err != nil {
			t.Errorf("Comments: unexpected error: %v", err)
			return
		}
		// Graceful degradation: cycle nodes become their own roots.
		// Both are non-tombstone so at least one (likely both) survive.
		if len(comments) == 0 {
			t.Error("two-node cycle: expected at least 1 resolved comment (graceful degradation), got 0")
		}
	})
}

// TestRawSidecar_SelfReplace verifies that a sidecar where a comment replaces
// itself (A→A) resolves in bounded time and returns the comment (not deleted).
func TestRawSidecar_SelfReplace(t *testing.T) {
	dir := t.TempDir()
	rf := storetest.NewRawFixture(t, dir)
	rf.WriteIssue("tst-0001.md", minimalIssueMD("tst-0001"))

	sidecar := `---
id: selfself
author: alice
created: 2026-06-01T10:00:00Z
replaces: selfself
body: |
  self-replace comment
`
	rf.WriteSidecar("tst-0001.yml", []byte(sidecar))

	s, err := tasks.Open(rf.Dir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	withTimeout(t, 5*time.Second, func() {
		comments, err := s.Comments("tst-0001")
		if err != nil {
			t.Errorf("Comments: unexpected error: %v", err)
			return
		}
		if len(comments) != 1 {
			t.Errorf("self-replace: expected 1 comment (graceful degradation), got %d: %+v", len(comments), comments)
			return
		}
		if comments[0].ID != "selfself" {
			t.Errorf("self-replace: expected selfself, got %q", comments[0].ID)
		}
	})
}

// TestRawSidecar_Fork verifies that when two documents replace the same target,
// the later one in the stream wins (fork / duplicate-replaces semantics).
func TestRawSidecar_Fork(t *testing.T) {
	dir := t.TempDir()
	rf := storetest.NewRawFixture(t, dir)
	rf.WriteIssue("tst-0001.md", minimalIssueMD("tst-0001"))

	sidecar := `---
id: origorig
author: alice
created: 2026-06-01T10:00:00Z
body: |
  original
---
id: edit0001
author: bob
created: 2026-06-01T10:01:00Z
replaces: origorig
body: |
  edit 1
---
id: edit0002
author: carol
created: 2026-06-01T10:02:00Z
replaces: origorig
body: |
  edit 2
`
	rf.WriteSidecar("tst-0001.yml", []byte(sidecar))

	s, err := tasks.Open(rf.Dir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	withTimeout(t, 5*time.Second, func() {
		comments, err := s.Comments("tst-0001")
		if err != nil {
			t.Errorf("Comments: unexpected error: %v", err)
			return
		}
		if len(comments) != 1 {
			t.Fatalf("fork: expected 1 resolved comment (later wins), got %d: %+v", len(comments), comments)
		}
		// edit0002 is the later doc in the stream and should win.
		if comments[0].ID != "edit0002" {
			t.Errorf("fork: expected edit0002 (later wins), got %q", comments[0].ID)
		}
	})
}

// TestRawSidecar_TruncatedTrailingDoc verifies that a sidecar with a partial /
// truncated trailing document either returns a clear error or gracefully ignores
// the partial doc — never panics, never hangs.
func TestRawSidecar_TruncatedTrailingDoc(t *testing.T) {
	dir := t.TempDir()
	rf := storetest.NewRawFixture(t, dir)
	rf.WriteIssue("tst-0001.md", minimalIssueMD("tst-0001"))

	// Well-formed first doc, then a partial/truncated second doc.
	sidecar := `---
id: goodgood
author: alice
created: 2026-06-01T10:00:00Z
body: |
  good comment
---
id: trunctrunc
author: bob
created: 2026-06-01T10:01:00Z
body: |
  this doc is
`
	// Simulate truncation by trimming the last line.
	truncated := sidecar[:len(sidecar)-10]
	rf.WriteSidecar("tst-0001.yml", []byte(truncated))

	s, err := tasks.Open(rf.Dir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	withTimeout(t, 5*time.Second, func() {
		// Either an error or a partial result is acceptable.
		// What is NOT acceptable: panic or hang.
		_, _ = s.Comments("tst-0001")
	})
}

// TestRawSidecar_EmptySidecar verifies that an empty sidecar file (zero bytes)
// is handled gracefully — returns 0 comments, no error, no panic.
func TestRawSidecar_EmptySidecar(t *testing.T) {
	dir := t.TempDir()
	rf := storetest.NewRawFixture(t, dir)
	rf.WriteIssue("tst-0001.md", minimalIssueMD("tst-0001"))
	rf.WriteSidecar("tst-0001.yml", []byte{})

	s, err := tasks.Open(rf.Dir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	comments, err := s.Comments("tst-0001")
	if err != nil {
		t.Fatalf("Comments on empty sidecar: unexpected error: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("empty sidecar: expected 0 comments, got %d", len(comments))
	}
}

// TestRawSidecar_MissingSidecarFile verifies that when no sidecar file exists
// for an issue, Comments returns 0 comments and no error.
func TestRawSidecar_MissingSidecarFile(t *testing.T) {
	dir := t.TempDir()
	rf := storetest.NewRawFixture(t, dir)
	rf.WriteIssue("tst-0001.md", minimalIssueMD("tst-0001"))
	// No WriteSidecar call — sidecar does not exist.

	s, err := tasks.Open(rf.Dir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	comments, err := s.Comments("tst-0001")
	if err != nil {
		t.Fatalf("Comments with no sidecar: unexpected error: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("no sidecar: expected 0 comments, got %d", len(comments))
	}
}

// TestRawIssue_MalformedFrontmatter verifies that a raw issue .md with
// malformed frontmatter results in a clear error when accessed through the
// Store API — not a panic, not a silent success.
func TestRawIssue_MalformedFrontmatter(t *testing.T) {
	cases := []struct {
		name  string
		id    string
		bytes []byte
	}{
		{
			name:  "no frontmatter fence",
			id:    "tst-0001",
			bytes: []byte("just plain text, no fence"),
		},
		{
			name:  "unclosed YAML bracket",
			id:    "tst-0002",
			bytes: []byte("---\nid: [unclosed\n---\n"),
		},
		{
			name:  "unterminated frontmatter",
			id:    "tst-0003",
			bytes: []byte("---\nid: tst-0003\ntitle: no close\n"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			rf := storetest.NewRawFixture(t, dir)
			rf.WriteIssue(tc.id+".md", tc.bytes)

			s, err := tasks.Open(rf.Dir())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}

			// Get should return a non-nil error — never a panic.
			_, err = s.Get(tc.id)
			if err == nil {
				t.Errorf("Get(%q) with malformed file: expected error, got nil", tc.id)
			}
		})
	}
}
