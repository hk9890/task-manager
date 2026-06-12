package tasks

// L1 pure tests for resolveComments, sanitizeCommentBody, and byte-golden
// round-trips (no FS, no Store).
//
// L2 tests (Mem FS) for appendCommentDoc, readCommentStream, and the
// store.All() no-sidecar guard are in the same file to keep comment
// primitives co-located.

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// ── L1: sanitizeCommentBody ────────────────────────────────────────────────

func TestSanitizeCommentBody_LFPassthrough(t *testing.T) {
	in := "line1\nline2\n"
	got := sanitizeCommentBody(in)
	if got != in {
		t.Errorf("LF-only body should be unchanged; got %q", got)
	}
}

func TestSanitizeCommentBody_CRLFConverted(t *testing.T) {
	in := "line1\r\nline2\r\n"
	want := "line1\nline2\n"
	if got := sanitizeCommentBody(in); got != want {
		t.Errorf("CRLF -> LF: got %q want %q", got, want)
	}
}

func TestSanitizeCommentBody_CRConverted(t *testing.T) {
	in := "line1\rline2\r"
	want := "line1\nline2\n"
	if got := sanitizeCommentBody(in); got != want {
		t.Errorf("CR -> LF: got %q want %q", got, want)
	}
}

func TestSanitizeCommentBody_TrailingWhitespaceStripped(t *testing.T) {
	in := "line1   \nline2\t \nline3\n"
	want := "line1\nline2\nline3\n"
	if got := sanitizeCommentBody(in); got != want {
		t.Errorf("trailing ws stripped: got %q want %q", got, want)
	}
}

func TestSanitizeCommentBody_MixedLineEndings(t *testing.T) {
	in := "a\r\nb\rc\n"
	want := "a\nb\nc\n"
	if got := sanitizeCommentBody(in); got != want {
		t.Errorf("mixed endings: got %q want %q", got, want)
	}
}

// ── L1: byte-golden — no double-quoted scalars ─────────────────────────────

// TestGolden_NoDoubleQuotedScalar verifies that bodies containing code
// fences, trailing whitespace (before sanitization), CRLF, and literal
// "---" lines never produce a double-quoted YAML scalar.
func TestGolden_NoDoubleQuotedScalar(t *testing.T) {
	ts := time.Date(2026, 6, 4, 15, 22, 37, 0, time.UTC)

	cases := []struct {
		name string
		body string
	}{
		{
			name: "code fence",
			body: "## Title\n\n```\n$ cmd output\n```\n",
		},
		{
			name: "trailing whitespace (sanitized)",
			body: "line1   \nline2\t\n",
		},
		{
			name: "CRLF (sanitized)",
			body: "line1\r\nline2\r\n",
		},
		{
			name: "literal --- inside body",
			body: "above\n---\nbelow\n",
		},
		{
			name: "multi-line with colon",
			body: "key: value\nanother: line\n",
		},
		{
			name: "single line no newline",
			body: "short note",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Comment{
				ID:      "abcd1234",
				Author:  "hans",
				Created: ts,
				Body:    sanitizeCommentBody(tc.body),
			}
			data := marshalCommentDoc(c)
			if bytes.Contains(data, []byte(`body: "`)) {
				t.Errorf("body was serialized as double-quoted scalar:\n%s", data)
			}
		})
	}
}

// TestGolden_RoundTripBodyExact verifies that marshal+parse round-trips
// arbitrary bodies byte-exact (after sanitization).
func TestGolden_RoundTripBodyExact(t *testing.T) {
	ts := time.Date(2026, 6, 4, 15, 22, 37, 0, time.UTC)

	bodies := []string{
		"simple one-liner\n",
		"## Title\n\nBody text.\n",
		"line with: colons\n",
		"---\nin a body\n---\n",
		"```go\nfmt.Println(\"hi\")\n```\n",
	}

	for _, body := range bodies {
		body = sanitizeCommentBody(body)
		c := Comment{
			ID:      "abcd1234",
			Author:  "hans",
			Created: ts,
			Body:    body,
		}

		// Marshal then parse back.
		data := marshalCommentDoc(c)
		docs, err := parseCommentStream(data)
		if err != nil {
			t.Fatalf("parseCommentStream(%q): %v", body, err)
		}
		if len(docs) != 1 {
			t.Fatalf("expected 1 doc, got %d", len(docs))
		}
		if docs[0].Body != body {
			t.Errorf("body mismatch:\nwant: %q\ngot:  %q", body, docs[0].Body)
		}
	}
}

// ── L1: resolveComments ────────────────────────────────────────────────────

func TestResolveComments_EmptyStream(t *testing.T) {
	if got := resolveComments(nil); len(got) != 0 {
		t.Errorf("empty stream: got %d comments", len(got))
	}
}

func TestResolveComments_NoReplaces(t *testing.T) {
	ts := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	stream := []Comment{
		{ID: "aaaaaaaa", Author: "hans", Created: ts, Body: "first\n"},
		{ID: "bbbbbbbb", Author: "hans", Created: ts.Add(time.Second), Body: "second\n"},
	}
	got := resolveComments(stream)
	if len(got) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(got))
	}
	if got[0].ID != "aaaaaaaa" || got[1].ID != "bbbbbbbb" {
		t.Errorf("wrong order: %v", got)
	}
}

func TestResolveComments_SimpleEdit(t *testing.T) {
	ts := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	stream := []Comment{
		{ID: "aaaaaaaa", Author: "hans", Created: ts, Body: "original\n"},
		{ID: "bbbbbbbb", Author: "hans", Created: ts.Add(time.Second), Replaces: "aaaaaaaa", Body: "revised\n"},
	}
	got := resolveComments(stream)
	// The original is replaced; the resolved list should have 1 comment.
	if len(got) != 1 {
		t.Fatalf("expected 1 resolved comment, got %d: %+v", len(got), got)
	}
	if got[0].ID != "bbbbbbbb" || got[0].Body != "revised\n" {
		t.Errorf("resolved wrong: %+v", got[0])
	}
}

func TestResolveComments_Tombstone(t *testing.T) {
	ts := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	stream := []Comment{
		{ID: "aaaaaaaa", Author: "hans", Created: ts, Body: "to be deleted\n"},
		{ID: "bbbbbbbb", Author: "hans", Created: ts.Add(time.Second), Replaces: "aaaaaaaa", Deleted: true},
	}
	got := resolveComments(stream)
	if len(got) != 0 {
		t.Errorf("tombstone: expected 0 resolved, got %d: %+v", len(got), got)
	}
}

func TestResolveComments_Chain(t *testing.T) {
	// edit of an edit: v1 → v2 → v3; only v3 should survive.
	ts := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	stream := []Comment{
		{ID: "v1aaaaaa", Created: ts, Body: "v1\n"},
		{ID: "v2bbbbbb", Created: ts.Add(time.Second), Replaces: "v1aaaaaa", Body: "v2\n"},
		{ID: "v3cccccc", Created: ts.Add(2 * time.Second), Replaces: "v2bbbbbb", Body: "v3\n"},
	}
	got := resolveComments(stream)
	if len(got) != 1 {
		t.Fatalf("chain: expected 1, got %d: %+v", len(got), got)
	}
	if got[0].ID != "v3cccccc" {
		t.Errorf("chain: expected v3, got %q", got[0].ID)
	}
}

func TestResolveComments_DuplicateReplaces_LaterWins(t *testing.T) {
	// Two documents both replace "aaaaaaaa"; the one appearing later in the
	// stream wins (position gives sequence, per spec §4.4 rule 3).
	ts := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	stream := []Comment{
		{ID: "aaaaaaaa", Created: ts, Body: "original\n"},
		{ID: "editone0", Created: ts.Add(time.Second), Replaces: "aaaaaaaa", Body: "edit1\n"},
		{ID: "edittwo0", Created: ts.Add(2 * time.Second), Replaces: "aaaaaaaa", Body: "edit2\n"},
	}
	got := resolveComments(stream)
	if len(got) != 1 {
		t.Fatalf("dup-replaces: expected 1, got %d: %+v", len(got), got)
	}
	if got[0].ID != "edittwo0" {
		t.Errorf("dup-replaces: later winner should be edittwo0, got %q", got[0].ID)
	}
}

func TestResolveComments_TombstoneOfEdit(t *testing.T) {
	// Delete an already-edited comment: v1 → v2 → tombstone(v2).
	ts := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	stream := []Comment{
		{ID: "v1aaaaaa", Created: ts, Body: "v1\n"},
		{ID: "v2bbbbbb", Created: ts.Add(time.Second), Replaces: "v1aaaaaa", Body: "v2\n"},
		{ID: "delccccc", Created: ts.Add(2 * time.Second), Replaces: "v2bbbbbb", Deleted: true},
	}
	got := resolveComments(stream)
	if len(got) != 0 {
		t.Errorf("tombstone-of-edit: expected 0, got %d: %+v", len(got), got)
	}
}

func TestResolveComments_MultipleIndependentComments(t *testing.T) {
	ts := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	stream := []Comment{
		{ID: "aaaaaaaa", Created: ts, Body: "a\n"},
		{ID: "bbbbbbbb", Created: ts.Add(time.Second), Body: "b\n"},
		{ID: "cccccccc", Created: ts.Add(2 * time.Second), Body: "c\n"},
		// edit of 'a'
		{ID: "dddddddd", Created: ts.Add(3 * time.Second), Replaces: "aaaaaaaa", Body: "a-edit\n"},
	}
	got := resolveComments(stream)
	if len(got) != 3 {
		t.Fatalf("multi: expected 3, got %d: %+v", len(got), got)
	}
	// 'b' and 'c' unchanged; 'a' replaced by 'd'.
	foundEdit := false
	for _, c := range got {
		if c.ID == "aaaaaaaa" {
			t.Error("original 'a' should not appear in resolved output")
		}
		if c.ID == "dddddddd" {
			foundEdit = true
		}
	}
	if !foundEdit {
		t.Error("edit of 'a' should appear in resolved output")
	}
}

// ── L1: newCommentID ───────────────────────────────────────────────────────

func TestNewCommentID_Format(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := newCommentID()
		if len(id) != 8 {
			t.Fatalf("id %q: want length 8, got %d", id, len(id))
		}
		for _, r := range id {
			isDigit := '0' <= r && r <= '9'
			isLower := 'a' <= r && r <= 'z'
			if !isDigit && !isLower {
				t.Fatalf("id %q: contains invalid rune %q (want [0-9a-z])", id, r)
			}
		}
	}
}

func TestNewCommentID_Unique(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id := newCommentID()
		if _, dup := seen[id]; dup {
			t.Fatalf("collision after %d ids: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

// ── L2 (Mem): appendCommentDoc + readCommentStream ─────────────────────────

func newCommentMemStore(t *testing.T) (*Store, *vfs.Mem) {
	t.Helper()
	m := vfs.NewMem()
	if err := m.MkdirAll("/.tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s := openWithFS("/", m)
	s.cfg = Config{Prefix: "agt"}
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}
	return s, m
}

func TestAppendAndReadCommentStream(t *testing.T) {
	_, m := newCommentMemStore(t)

	path := "/.tasks/comments/agt-0001.yml"
	ts := time.Date(2026, 6, 4, 15, 22, 37, 0, time.UTC)

	c1 := Comment{ID: "abcd1234", Author: "hans", Created: ts, Body: "first note\n"}
	c2 := Comment{ID: "efgh5678", Author: "alice", Created: ts.Add(time.Second), Body: "second note\n"}

	if err := appendCommentDoc(m, path, c1); err != nil {
		t.Fatalf("appendCommentDoc c1: %v", err)
	}
	if err := appendCommentDoc(m, path, c2); err != nil {
		t.Fatalf("appendCommentDoc c2: %v", err)
	}

	got, err := readCommentStream(m, path)
	if err != nil {
		t.Fatalf("readCommentStream: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(got))
	}
	if got[0].ID != "abcd1234" || got[0].Body != "first note\n" {
		t.Errorf("c1 wrong: %+v", got[0])
	}
	if got[1].ID != "efgh5678" || got[1].Body != "second note\n" {
		t.Errorf("c2 wrong: %+v", got[1])
	}
}

func TestReadCommentStream_AbsentFile(t *testing.T) {
	_, m := newCommentMemStore(t)
	// File does not exist: should return empty slice, not an error.
	got, err := readCommentStream(m, "/.tasks/comments/agt-9999.yml")
	if err != nil {
		t.Fatalf("readCommentStream on absent file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("absent sidecar: expected 0, got %d", len(got))
	}
}

func TestAppendCommentDoc_BodyRoundTrip(t *testing.T) {
	_, m := newCommentMemStore(t)

	path := "/.tasks/comments/agt-0002.yml"
	ts := time.Date(2026, 6, 4, 15, 22, 37, 0, time.UTC)

	// A body that contains code fences and a literal "---" line.
	body := sanitizeCommentBody("## Title\n\n```\n$ cmd\n```\n---\nafter\n")
	c := Comment{ID: "testtest", Author: "hans", Created: ts, Body: body}

	if err := appendCommentDoc(m, path, c); err != nil {
		t.Fatalf("appendCommentDoc: %v", err)
	}

	got, err := readCommentStream(m, path)
	if err != nil {
		t.Fatalf("readCommentStream: %v", err)
	}
	if len(got) != 1 || got[0].Body != body {
		t.Errorf("body round-trip failed:\nwant: %q\ngot:  %q", body, got[0].Body)
	}
}

func TestAppendCommentDoc_TombstoneRoundTrip(t *testing.T) {
	_, m := newCommentMemStore(t)

	path := "/.tasks/comments/agt-0003.yml"
	ts := time.Date(2026, 6, 4, 15, 22, 37, 0, time.UTC)

	c1 := Comment{ID: "orig0001", Author: "hans", Created: ts, Body: "original\n"}
	c2 := Comment{
		ID:       "tomb0002",
		Author:   "hans",
		Created:  ts.Add(time.Second),
		Replaces: "orig0001",
		Deleted:  true,
	}

	if err := appendCommentDoc(m, path, c1); err != nil {
		t.Fatalf("appendCommentDoc c1: %v", err)
	}
	if err := appendCommentDoc(m, path, c2); err != nil {
		t.Fatalf("appendCommentDoc c2: %v", err)
	}

	got, err := readCommentStream(m, path)
	if err != nil {
		t.Fatalf("readCommentStream: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("raw stream: expected 2, got %d", len(got))
	}
	if !got[1].Deleted || got[1].Replaces != "orig0001" {
		t.Errorf("tombstone wrong: %+v", got[1])
	}
}

// ── L2 (Mem): store.All() must never open a sidecar ───────────────────────

// errSidecarPoisoned is a sentinel returned by a FailOn fault to detect
// whether store.All() tries to open a comment sidecar.
var errSidecarPoisoned = errors.New("sidecar was opened by All() — violation of the no-sidecar rule")

func TestStoreAll_NeverOpensSidecar(t *testing.T) {
	s, m := newCommentMemStore(t)

	// Create an issue.
	iss, err := s.Create(CreateInput{Title: "test issue"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Write a sidecar for that issue.
	sidecarPath := s.commentsPath(iss.ID)
	ts := time.Date(2026, 6, 4, 15, 22, 37, 0, time.UTC)
	c := Comment{ID: "sideside", Author: "hans", Created: ts, Body: "a note\n"}
	if err := appendCommentDoc(m, sidecarPath, c); err != nil {
		t.Fatalf("appendCommentDoc: %v", err)
	}

	// Poison the sidecar: if All() reads it, it will get an error.
	m.FailOn("ReadFile", sidecarPath, errSidecarPoisoned)

	// All() must succeed without triggering the poison fault.
	// If All() reads the sidecar it will get errSidecarPoisoned, which will
	// propagate out and be caught by the Fatalf below.
	all, err := s.All()
	if err != nil {
		t.Fatalf("All() returned error (opened sidecar?): %v", err)
	}
	if len(all) != 1 || all[0].ID != iss.ID {
		t.Errorf("All() = %v, want [%s]", all, iss.ID)
	}
}

// ── L2 (Mem): commentsPath ─────────────────────────────────────────────────

func TestCommentsPath(t *testing.T) {
	s, _ := newCommentMemStore(t)
	got := s.commentsPath("agt-0001")
	want := "/.tasks/comments/agt-0001.yml"
	if got != want {
		t.Errorf("commentsPath = %q, want %q", got, want)
	}
}
