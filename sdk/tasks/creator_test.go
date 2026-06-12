package tasks_test

// creator_test.go — focused tests for the creator field (at-dny.2).
//
// Acceptance criteria covered:
//   AC1  Create with Creator persists it; round-trips through Marshal/Unmarshal.
//   AC2  Empty CreateInput.Creator stays empty (no default applied by the engine).
//   AC3  Files lacking creator parse unchanged (back-compat); empty creator omitted on write.
//   AC4  Invalid creator (>128 chars, newline, control char) rejected with *ValidationError.
//   AC5  Query("creator == \"x\"") and creator ~ "x" select correctly.
//   AC6  creator is NOT mutable via Update.
//   AC7  storetest builder can set creator (and assignee) on a fixture issue.

import (
	"errors"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
	"github.com/hk9890/task-manager/sdk/tasks/internal/storetest"
)

// ── AC1: persist + round-trip ─────────────────────────────────────────────────

// TestCreator_PersistAndRoundTrip verifies that a creator set at Create time is
// stored on disk and returned unchanged by Get.
func TestCreator_PersistAndRoundTrip(t *testing.T) {
	s := storetest.New(t).Mem()

	iss, err := s.Create(tasks.CreateInput{
		Title:   "has creator",
		Creator: "alice",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if iss.Creator != "alice" {
		t.Errorf("Create returned Creator = %q, want %q", iss.Creator, "alice")
	}

	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Creator != "alice" {
		t.Errorf("Get returned Creator = %q, want %q", got.Creator, "alice")
	}
}

// TestCreator_MarshalUnmarshalRoundTrip verifies that the creator field
// survives Marshal → Unmarshal intact (L1, pure).
func TestCreator_MarshalUnmarshalRoundTrip(t *testing.T) {
	// Use the Store to create a proper issue so Marshal gets a fully-populated Issue.
	s := storetest.New(t).Mem()
	iss, err := s.Create(tasks.CreateInput{
		Title:   "creator round-trip",
		Creator: "bob",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	data, err := tasks.Marshal(iss)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), "creator: bob") {
		t.Errorf("serialized frontmatter does not contain 'creator: bob':\n%s", data)
	}

	got, err := tasks.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Creator != "bob" {
		t.Errorf("Unmarshal returned Creator = %q, want %q", got.Creator, "bob")
	}
}

// ── AC2: empty stays empty ────────────────────────────────────────────────────

// TestCreator_EmptyStaysEmpty verifies that an empty CreateInput.Creator
// is stored as empty — no default is applied by the SDK engine.
func TestCreator_EmptyStaysEmpty(t *testing.T) {
	s := storetest.New(t).Mem()

	iss, err := s.Create(tasks.CreateInput{Title: "no creator"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if iss.Creator != "" {
		t.Errorf("Create with empty Creator: got %q, want empty", iss.Creator)
	}

	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Creator != "" {
		t.Errorf("Get after Create with empty Creator: got %q, want empty", got.Creator)
	}
}

// ── AC3: back-compat parse of files without creator ───────────────────────────

// TestCreator_BackCompatParse verifies that a file without a creator field
// parses successfully with an empty Creator (back-compat).
func TestCreator_BackCompatParse(t *testing.T) {
	// A minimal frontmatter without creator — simulates a pre-creator file.
	raw := []byte(`---
id: tst-0001
title: legacy issue
status: open
type: task
priority: 2
created: 2026-01-01T00:00:00Z
updated: 2026-01-01T00:00:00Z
---
`)
	iss, err := tasks.Unmarshal(raw)
	if err != nil {
		t.Fatalf("Unmarshal of legacy file: %v", err)
	}
	if iss.Creator != "" {
		t.Errorf("legacy file: Creator = %q, want empty", iss.Creator)
	}
	if iss.ID != "tst-0001" {
		t.Errorf("ID = %q, want tst-0001", iss.ID)
	}
}

// TestCreator_OmitEmptyOnWrite verifies that a zero-value Creator field is
// absent from the serialized frontmatter (omitempty).
func TestCreator_OmitEmptyOnWrite(t *testing.T) {
	iss := &tasks.Issue{
		ID:       "tst-0001",
		Title:    "no creator",
		Status:   tasks.StatusOpen,
		Type:     tasks.TypeTask,
		Priority: 2,
	}
	data, err := tasks.Marshal(iss)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "creator:") {
		t.Errorf("empty creator must be omitted from frontmatter:\n%s", data)
	}
}

// ── AC4: validation rejects bad creator ──────────────────────────────────────

// TestCreator_ValidationRejectsInvalid verifies that invalid creator values
// are rejected with *ValidationError carrying Field == "creator".
func TestCreator_ValidationRejectsInvalid(t *testing.T) {
	s := storetest.New(t).Mem()

	cases := []struct {
		name    string
		creator string
	}{
		{"too long (129 chars)", strings.Repeat("a", 129)},
		{"contains newline", "alice\nbob"},
		{"contains control char (tab)", "alice\tbob"},
		{"contains NUL", "alice\x00bob"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Create(tasks.CreateInput{
				Title:   "validation test",
				Creator: tc.creator,
			})
			if err == nil {
				t.Fatalf("expected ValidationError for creator %q, got nil", tc.creator)
			}
			var ve *tasks.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("expected *tasks.ValidationError, got %T: %v", err, err)
			}
			if ve.Field != "creator" {
				t.Errorf("ValidationError.Field = %q, want %q", ve.Field, "creator")
			}
		})
	}
}

// TestCreator_MaxLenValid verifies that exactly 128 characters is accepted.
func TestCreator_MaxLenValid(t *testing.T) {
	s := storetest.New(t).Mem()

	iss, err := s.Create(tasks.CreateInput{
		Title:   "max len creator",
		Creator: strings.Repeat("a", 128),
	})
	if err != nil {
		t.Fatalf("Create with 128-char creator: %v", err)
	}
	if len([]rune(iss.Creator)) != 128 {
		t.Errorf("Creator length = %d, want 128", len([]rune(iss.Creator)))
	}
}

// ── AC5: query by creator ─────────────────────────────────────────────────────

// TestCreator_QueryEquality verifies that creator == "x" selects only
// issues whose Creator matches exactly.
func TestCreator_QueryEquality(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Creator("alice")).
		Issue("tst-0002", storetest.Creator("bob")).
		Issue("tst-0003").
		Mem()

	results, err := s.Query(`creator == "alice"`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Query creator == alice: got %d results, want 1", len(results))
	}
	if results[0].ID != "tst-0001" {
		t.Errorf("Query creator == alice: got ID %q, want tst-0001", results[0].ID)
	}
}

// TestCreator_QueryContains verifies that creator ~ "x" performs a
// substring/contains match.
func TestCreator_QueryContains(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Creator("alice-dev")).
		Issue("tst-0002", storetest.Creator("bob")).
		Mem()

	results, err := s.Query(`creator ~ "alice"`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Query creator ~ alice: got %d results, want 1", len(results))
	}
	if results[0].ID != "tst-0001" {
		t.Errorf("Query creator ~ alice: got ID %q, want tst-0001", results[0].ID)
	}
}

// TestCreator_QueryNotEqual verifies that creator != "x" excludes the
// matching issue.
func TestCreator_QueryNotEqual(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Creator("alice")).
		Issue("tst-0002", storetest.Creator("bob")).
		Mem()

	results, err := s.Query(`creator != "alice"`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Query creator != alice: got %d results, want 1", len(results))
	}
	if results[0].ID != "tst-0002" {
		t.Errorf("Query creator != alice: got ID %q, want tst-0002", results[0].ID)
	}
}

// ── AC6: creator is NOT mutable via Update ────────────────────────────────────

// TestCreator_NotMutableViaUpdate verifies that UpdateInput has no Creator
// field (immutability enforced at the type level) and that an Update call
// does not change the creator on the stored issue.
func TestCreator_NotMutableViaUpdate(t *testing.T) {
	s := storetest.New(t).Mem()

	iss, err := s.Create(tasks.CreateInput{
		Title:   "immutable creator",
		Creator: "alice",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newTitle := "updated title"
	updated, err := s.Update(iss.ID, tasks.UpdateInput{Title: &newTitle})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Creator != "alice" {
		t.Errorf("after Update: Creator = %q, want %q (must be unchanged)", updated.Creator, "alice")
	}

	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got.Creator != "alice" {
		t.Errorf("Get after Update: Creator = %q, want %q (must be unchanged)", got.Creator, "alice")
	}
}

// ── AC7: storetest builder can set creator and assignee ──────────────────────

// TestCreator_BuilderCreatorAndAssignee verifies that the storetest builder
// Creator and Assignee opts are plumbed through to the stored issue.
func TestCreator_BuilderCreatorAndAssignee(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001",
			storetest.Creator("alice"),
			storetest.Assignee("bob"),
		).
		Mem()

	got, err := s.Get("tst-0001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Creator != "alice" {
		t.Errorf("Creator = %q, want %q", got.Creator, "alice")
	}
	if got.Assignee != "bob" {
		t.Errorf("Assignee = %q, want %q", got.Assignee, "bob")
	}
}

// TestCreator_BuilderCreatorPersistsTempDir verifies the builder creator opt
// works on a real temp-dir store (L3 durability).
func TestCreator_BuilderCreatorPersistsTempDir(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Creator("carol")).
		TempDir(t)

	got, err := s.Get("tst-0001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Creator != "carol" {
		t.Errorf("Creator = %q, want %q (L3 round-trip)", got.Creator, "carol")
	}
}
