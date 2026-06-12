package tasks

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	created := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	closed := time.Date(2026, 6, 5, 8, 0, 0, 0, time.UTC)

	in := &Issue{
		ID:          "agt-0042",
		Title:       "Fix drill nav",
		Status:      StatusClosed,
		Type:        TypeBug,
		Priority:    1,
		Assignee:    "hans",
		Labels:      []string{"area:details", "risk:low"},
		Parent:      "agt-0007",
		BlockedBy:   []string{"agt-0040"},
		Related:     []string{"agt-0012"},
		Created:     created,
		Updated:     updated,
		Closed:      closed,
		CloseReason: "shipped",
		Description: "## Description\nDrilling a related issue should navigate fully.",
	}

	data, err := Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.HasPrefix(string(data), "---\n") {
		t.Fatalf("expected leading fence, got:\n%s", data)
	}

	out, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if out.ID != in.ID || out.Title != in.Title || out.Status != in.Status || out.Type != in.Type {
		t.Errorf("scalar mismatch: %+v", out)
	}
	if out.Priority != 1 || out.Assignee != "hans" {
		t.Errorf("priority/assignee mismatch: %+v", out)
	}
	if !out.Closed.Equal(closed) || out.CloseReason != "shipped" {
		t.Errorf("closed mismatch: %v / %q", out.Closed, out.CloseReason)
	}
	if len(out.Labels) != 2 || out.Labels[0] != "area:details" {
		t.Errorf("labels mismatch: %v", out.Labels)
	}
	if len(out.BlockedBy) != 1 || out.BlockedBy[0] != "agt-0040" {
		t.Errorf("blocked_by mismatch: %v", out.BlockedBy)
	}
	if out.Parent != "agt-0007" || len(out.Related) != 1 {
		t.Errorf("parent/related mismatch: %q / %v", out.Parent, out.Related)
	}
	// Comments are no longer in frontmatter; they live in the sidecar.
	// Just verify description round-trips correctly.
	if out.Description != in.Description {
		t.Errorf("description mismatch:\nwant %q\ngot  %q", in.Description, out.Description)
	}
}

func TestUnmarshalOpenIssueNoClosed(t *testing.T) {
	data, err := Marshal(&Issue{
		ID: "agt-0001", Title: "open one", Status: StatusOpen, Type: TypeTask, Priority: 2,
		Created: time.Now(), Updated: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "closed:") {
		t.Errorf("open issue should not serialize a closed field:\n%s", data)
	}
	out, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Closed.IsZero() {
		t.Errorf("expected zero closed time, got %v", out.Closed)
	}
}

// TestMarshal_TruncatesSubSecondTimestamps verifies that Marshal truncates
// Created/Updated/Closed to whole seconds in UTC before serialization
// (TASK-STORAGE-SPEC §6: "truncated to whole seconds").
// SDK callers that build an Issue from time.Now() (which has nanosecond
// precision) must not produce non-conforming timestamps in the output.
func TestMarshal_TruncatesSubSecondTimestamps(t *testing.T) {
	// Construct a time with sub-second precision and a non-UTC location.
	subSecond := time.Date(2026, 6, 5, 14, 30, 45, 123456789, time.UTC)
	closedSubSecond := time.Date(2026, 6, 6, 9, 0, 0, 999999999, time.UTC)

	iss := &Issue{
		ID:       "tst-0001",
		Title:    "ts truncation test",
		Status:   StatusClosed,
		Type:     TypeTask,
		Priority: 2,
		Created:  subSecond,
		Updated:  subSecond,
		Closed:   closedSubSecond,
	}

	data, err := Marshal(iss)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Each timestamp line must match the whole-second UTC pattern
	// (TASK-STORAGE-SPEC §4.3: "^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$").
	tsPattern := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)

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
					t.Errorf("field %s timestamp %q does not match whole-second UTC pattern", field, tsStr)
				}
			}
		}
	}

	// Round-trip: parsed timestamps must equal whole-second values.
	out, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	wantCreated := time.Date(2026, 6, 5, 14, 30, 45, 0, time.UTC)
	wantClosed := time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC)
	if !out.Created.Equal(wantCreated) {
		t.Errorf("Created = %v, want %v (truncated)", out.Created, wantCreated)
	}
	if !out.Updated.Equal(wantCreated) {
		t.Errorf("Updated = %v, want %v (truncated)", out.Updated, wantCreated)
	}
	if !out.Closed.Equal(wantClosed) {
		t.Errorf("Closed = %v, want %v (truncated)", out.Closed, wantClosed)
	}
}

func TestUnmarshalErrors(t *testing.T) {
	cases := map[string]string{
		"no frontmatter": "just some text",
		"unterminated":   "---\nid: x\ntitle: y",
		"bad yaml":       "---\nid: [unclosed\n---\n",
	}
	for name, in := range cases {
		if _, err := Unmarshal([]byte(in)); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}
