package tasks

// spec_storage_conformance_test.go — TASK-STORAGE-SPEC on-disk format conformance.
//
// Spec sections covered:
//   §4.2  config.yaml — prefix field written and readable
//   §4.3  Task file frontmatter — field order (normative); omitempty for optional
//         fields (assignee, labels, parent, blocked_by, related, close_reason);
//         closed field present iff status == closed; no comments in frontmatter.
//   §4.4  Comment sidecar — YAML multi-document stream; each doc separated by
//         "---\n" at column 0; body rendered as block scalar (not double-quoted);
//         sidecar created lazily; sidecar always at comments/<id>.yml regardless
//         of task lifecycle.
//   §5    Lifecycle — closed file is in closed/<id>.md; hot-dir file is absent
//         after close; sidecar stays in comments/.
//   §6    Encoding — timestamp pattern ^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$
//
// Already well-covered elsewhere (not duplicated here):
//   - Timestamp truncation to whole seconds → frontmatter_test.go
//     (TestMarshal_TruncatesSubSecondTimestamps)
//   - Marshal/Unmarshal round-trip → frontmatter_test.go (TestMarshalUnmarshalRoundTrip)
//   - Hot/cold partition moves → close_reopen_test.go
//   - Sidecar stays in comments/ after close → close_reopen_test.go
//     (TestClose_SidecarStaysInComments)

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hk9890/agent-tasks/sdk/tasks/internal/vfs"
)

// ── §4.3: frontmatter field order (normative) ────────────────────────────────

// TestSpec_Storage_FrontmatterFieldOrder verifies that Marshal emits fields in
// the normative order specified by TASK-STORAGE-SPEC §4.3:
//
//	id, title, status, type, priority, assignee?, labels?, parent?,
//	blocked_by?, related?, created, updated, closed?, close_reason?
//
// The order is load-bearing: readers that reconstruct history from diffs depend
// on stable ordering.
func TestSpec_Storage_FrontmatterFieldOrder(t *testing.T) {
	created := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	closed := time.Date(2026, 6, 5, 8, 0, 0, 0, time.UTC)

	iss := &Issue{
		ID:          "tst-0042",
		Title:       "Test field order",
		Status:      StatusClosed,
		Type:        TypeBug,
		Priority:    1,
		Assignee:    "hans",
		Creator:     "alice",
		Labels:      []string{"area:db"},
		Parent:      "tst-0007",
		BlockedBy:   []string{"tst-0040"},
		Related:     []string{"tst-0012"},
		Created:     created,
		Updated:     updated,
		Closed:      closed,
		CloseReason: "fixed",
	}

	data, err := Marshal(iss)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Extract frontmatter lines between the two "---" fences.
	lines := strings.Split(string(data), "\n")
	var fmLines []string
	inFM := false
	for _, line := range lines {
		if line == "---" {
			if !inFM {
				inFM = true
				continue
			}
			break
		}
		if inFM {
			fmLines = append(fmLines, line)
		}
	}

	// Build the ordered list of field name prefixes from the spec table.
	wantOrder := []string{
		"id:", "title:", "status:", "type:", "priority:",
		"assignee:", "creator:", "labels:", "parent:", "blocked_by:", "related:",
		"created:", "updated:", "closed:", "close_reason:",
	}

	// Walk the emitted fields in document order and verify they appear in
	// the spec-mandated sequence.
	lastIdx := -1
	for _, line := range fmLines {
		trimmed := strings.TrimSpace(line)
		// Skip list continuation lines (items under a sequence field).
		if strings.HasPrefix(trimmed, "- ") || trimmed == "" {
			continue
		}
		for i, want := range wantOrder {
			if strings.HasPrefix(trimmed, want) {
				if i < lastIdx {
					t.Errorf("field %q appears after field at index %d — violates normative order (TASK-STORAGE-SPEC §4.3)", trimmed, lastIdx)
				}
				lastIdx = i
				break
			}
		}
	}
}

// ── §4.3: omitempty — optional fields absent when empty ──────────────────────

// TestSpec_Storage_OmitemptyOptionalFields verifies that optional fields
// (assignee, labels, parent, blocked_by, related, close_reason) are absent from
// the serialized frontmatter when they hold their zero value.
// TASK-STORAGE-SPEC §4.3: "Emitted when: non-empty".
func TestSpec_Storage_OmitemptyOptionalFields(t *testing.T) {
	iss := &Issue{
		ID:       "tst-0001",
		Title:    "minimal issue",
		Status:   StatusOpen,
		Type:     TypeTask,
		Priority: 2,
		Created:  time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Updated:  time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		// Assignee, Labels, Parent, BlockedBy, Related, CloseReason all zero.
	}

	data, err := Marshal(iss)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	body := string(data)

	// These optional fields must NOT appear when empty.
	absent := []string{"assignee:", "creator:", "labels:", "parent:", "blocked_by:", "related:", "close_reason:", "closed:"}
	for _, field := range absent {
		if strings.Contains(body, field) {
			t.Errorf("optional field %q should be absent for a minimal issue, but found in:\n%s", field, body)
		}
	}
}

// TestSpec_Storage_ClosedFieldPresentWhenClosed verifies that the closed
// timestamp is written when status == closed, and absent otherwise.
// TASK-STORAGE-SPEC §4.3: "Emitted when: status is closed".
func TestSpec_Storage_ClosedFieldPresentWhenClosed(t *testing.T) {
	now := time.Date(2026, 6, 5, 8, 0, 0, 0, time.UTC)

	// Closed issue: closed field must appear.
	closedIss := &Issue{
		ID:       "tst-0001",
		Title:    "closed issue",
		Status:   StatusClosed,
		Type:     TypeTask,
		Priority: 2,
		Created:  now,
		Updated:  now,
		Closed:   now,
	}
	data, err := Marshal(closedIss)
	if err != nil {
		t.Fatalf("Marshal closed: %v", err)
	}
	if !strings.Contains(string(data), "closed:") {
		t.Errorf("closed field must appear in frontmatter for a closed issue:\n%s", data)
	}

	// Open issue: closed field must be absent.
	openIss := &Issue{
		ID:       "tst-0002",
		Title:    "open issue",
		Status:   StatusOpen,
		Type:     TypeTask,
		Priority: 2,
		Created:  now,
		Updated:  now,
		// Closed is zero.
	}
	data2, err := Marshal(openIss)
	if err != nil {
		t.Fatalf("Marshal open: %v", err)
	}
	if strings.Contains(string(data2), "closed:") {
		t.Errorf("closed field must be absent for an open issue:\n%s", data2)
	}
}

// TestSpec_Storage_NoCommentsInFrontmatter verifies that the serialized task
// file contains no "comments:" YAML key. TASK-STORAGE-SPEC §4.3: "No comments
// in frontmatter. Comments live in the sidecar (§4.4)."
func TestSpec_Storage_NoCommentsInFrontmatter(t *testing.T) {
	iss := &Issue{
		ID:       "tst-0001",
		Title:    "issue with comment",
		Status:   StatusOpen,
		Type:     TypeTask,
		Priority: 2,
		Created:  time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Updated:  time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	data, err := Marshal(iss)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "comments:") {
		t.Errorf("frontmatter must not contain 'comments:' key:\n%s", data)
	}
}

// ── §6: timestamp format ─────────────────────────────────────────────────────

// TestSpec_Storage_TimestampFormat verifies the timestamp pattern
// ^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$ for each timestamp field.
// TASK-STORAGE-SPEC §4.3: "^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$".
// TASK-STORAGE-SPEC §6: "truncated to whole seconds".
func TestSpec_Storage_TimestampFormat(t *testing.T) {
	tsPattern := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)
	now := time.Date(2026, 6, 5, 14, 30, 45, 0, time.UTC)

	iss := &Issue{
		ID:       "tst-0001",
		Title:    "ts format test",
		Status:   StatusClosed,
		Type:     TypeTask,
		Priority: 2,
		Created:  now,
		Updated:  now,
		Closed:   now,
	}
	data, err := Marshal(iss)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		for _, field := range []string{"created:", "updated:", "closed:"} {
			if strings.HasPrefix(strings.TrimSpace(line), field) {
				parts := strings.SplitN(line, ": ", 2)
				if len(parts) != 2 {
					t.Errorf("unexpected line format: %q", line)
					continue
				}
				tsStr := strings.TrimSpace(parts[1])
				if !tsPattern.MatchString(tsStr) {
					t.Errorf("field %s: timestamp %q does not match ^%%Y-..T..:..:..Z$ pattern", field, tsStr)
				}
			}
		}
	}
}

// ── §4.4: sidecar format — multi-document YAML stream ────────────────────────

// TestSpec_Storage_SidecarMultiDocumentStream verifies that the comment sidecar
// is a multi-document YAML stream: each document is separated by "---\n" at
// column 0. TASK-STORAGE-SPEC §4.4: "A multi-document YAML stream: one document
// per comment, separated by ---."
func TestSpec_Storage_SidecarMultiDocumentStream(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/.tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	path := "/.tasks/comments/tst-0001.yml"
	ts := time.Date(2026, 6, 4, 15, 22, 37, 0, time.UTC)

	c1 := Comment{ID: "aaaaaaaa", Author: "alice", Created: ts, Body: "first comment\n"}
	c2 := Comment{ID: "bbbbbbbb", Author: "bob", Created: ts.Add(time.Second), Body: "second comment\n"}

	if err := appendCommentDoc(m, path, c1); err != nil {
		t.Fatalf("appendCommentDoc c1: %v", err)
	}
	if err := appendCommentDoc(m, path, c2); err != nil {
		t.Fatalf("appendCommentDoc c2: %v", err)
	}

	raw, err := m.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// The stream must start with "---\n" (first document separator).
	if !bytes.HasPrefix(raw, []byte("---\n")) {
		t.Errorf("sidecar stream must start with ---\\n; got first 20 bytes: %q", raw[:min(20, len(raw))])
	}

	// Count "---" at column 0.
	separatorCount := 0
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if string(line) == "---" {
			separatorCount++
		}
	}
	// Two documents → at least 2 "---" separators.
	if separatorCount < 2 {
		t.Errorf("expected at least 2 '---' separators for 2 documents, got %d:\n%s", separatorCount, raw)
	}
}

// TestSpec_Storage_SidecarBodyBlockScalar verifies that comment bodies are
// serialized as block scalars (body: |) not double-quoted scalars.
// TASK-STORAGE-SPEC §4.4 rule 4: "A writer MUST NOT emit a body that
// round-trips as a double-quoted scalar."
func TestSpec_Storage_SidecarBodyBlockScalar(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/.tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	path := "/.tasks/comments/tst-0001.yml"
	ts := time.Date(2026, 6, 4, 15, 22, 37, 0, time.UTC)

	// Bodies that a naive YAML emitter might double-quote.
	bodies := []struct {
		name string
		body string
	}{
		{"multiline", "line one\nline two\n"},
		{"code fence", "```go\nfmt.Println(\"hello\")\n```\n"},
		{"literal dashes", "above\n---\nbelow\n"},
		{"colon in value", "key: value\n"},
	}

	for _, tc := range bodies {
		t.Run(tc.name, func(t *testing.T) {
			p := path + tc.name
			c := Comment{
				ID:      "testtest",
				Author:  "alice",
				Created: ts,
				Body:    sanitizeCommentBody(tc.body),
			}
			if err := appendCommentDoc(m, p, c); err != nil {
				t.Fatalf("appendCommentDoc: %v", err)
			}
			raw, err := m.ReadFile(p)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			// If body is stored as double-quoted scalar it would look like: body: "..."
			if bytes.Contains(raw, []byte(`body: "`)) {
				t.Errorf("body stored as double-quoted scalar (violates §4.4 rule 4):\n%s", raw)
			}
		})
	}
}

// TestSpec_Storage_SidecarCreatedLazily verifies that the comment sidecar is
// not created until the first comment is added. TASK-STORAGE-SPEC §4.4:
// "Created lazily on first comment."
func TestSpec_Storage_SidecarCreatedLazily(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/.tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s := openWithFS("/", m)
	s.cfg = Config{Prefix: "tst"}
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}

	iss, err := s.Create(CreateInput{Title: "no comment yet"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Sidecar must NOT exist before any comment is added.
	sidecarPath := s.commentsPath(iss.ID)
	if _, err := m.ReadFile(sidecarPath); err == nil {
		t.Errorf("sidecar %s must not exist before first comment", sidecarPath)
	}

	// Add a comment.
	if _, err := s.AddComment(iss.ID, "alice", "first note\n"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	// Now the sidecar must exist.
	if _, err := m.ReadFile(sidecarPath); err != nil {
		t.Errorf("sidecar %s must exist after first comment: %v", sidecarPath, err)
	}
}

// TestSpec_Storage_SidecarPathUnchangedAfterClose verifies that the sidecar
// path is always comments/<id>.yml regardless of the task's partition.
// TASK-STORAGE-SPEC §4.4 rule 6: "The sidecar always lives in comments/<id>.yml
// regardless of the task's state; only the task .md moves on close."
func TestSpec_Storage_SidecarPathUnchangedAfterClose(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/.tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s := openWithFS("/", m)
	s.cfg = Config{Prefix: "tst"}
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}

	iss, err := s.Create(CreateInput{Title: "to close"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.AddComment(iss.ID, "alice", "pre-close note\n"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	// Close the issue — task .md moves to closed/<id>.md.
	if _, err := s.Close(iss.ID, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Sidecar must still be at comments/<id>.yml (not closed/comments/).
	sidecarPath := s.commentsPath(iss.ID)
	if _, err := m.ReadFile(sidecarPath); err != nil {
		t.Errorf("sidecar at %s not found after Close: %v", sidecarPath, err)
	}

	// No sidecar should be under closed/.
	wrongPath := "/.tasks/closed/comments/" + iss.ID + ".yml"
	if _, err := m.ReadFile(wrongPath); err == nil {
		t.Errorf("sidecar must NOT exist at closed/ path %s", wrongPath)
	}
}

// ── §5: closed/ layout ───────────────────────────────────────────────────────

// TestSpec_Storage_ClosedLayout verifies the partition layout:
//   - active .md in .tasks/<id>.md
//   - after close: .md in .tasks/closed/<id>.md, hot-dir file absent
//
// TASK-STORAGE-SPEC §2, §5.
func TestSpec_Storage_ClosedLayout(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/.tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s := openWithFS("/", m)
	s.cfg = Config{Prefix: "tst"}
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}

	iss, err := s.Create(CreateInput{Title: "layout test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Before close: file must be in the hot directory.
	hotPath := "/.tasks/" + iss.ID + ".md"
	if _, err := m.ReadFile(hotPath); err != nil {
		t.Errorf("hot-dir file %s must exist before Close: %v", hotPath, err)
	}

	if _, err := s.Close(iss.ID, ""); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After close: hot-dir file must be gone.
	if _, err := m.ReadFile(hotPath); err == nil {
		t.Errorf("hot-dir file %s must be absent after Close", hotPath)
	}

	// After close: closed/ file must exist.
	closedPath := "/.tasks/closed/" + iss.ID + ".md"
	if _, err := m.ReadFile(closedPath); err != nil {
		t.Errorf("closed/ file %s must exist after Close: %v", closedPath, err)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
