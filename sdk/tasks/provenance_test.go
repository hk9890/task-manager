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

package tasks

// provenance_test.go — L1 + L2 for the agent/session provenance fields
// (TASK-STORAGE-SPEC §4.3, §4.4 rule 8).

import (
	"errors"
	"strings"
	"testing"
)

// ── L1: field constraints ────────────────────────────────────────────────────

func TestValidateFields_AgentAndSessionConstraints(t *testing.T) {
	base := func() *Issue {
		return &Issue{ID: "agt-0001", Title: "t", Status: StatusOpen, Type: TypeTask, Priority: 2}
	}
	tests := []struct {
		name      string
		mutate    func(*Issue)
		wantField string
	}{
		{"agent over length", func(i *Issue) { i.Agent = strings.Repeat("a", maxAgentLen+1) }, "agent"},
		{"agent newline", func(i *Issue) { i.Agent = "claude\ncode" }, "agent"},
		{"agent control char", func(i *Issue) { i.Agent = "claude\x01code" }, "agent"},
		{"session over length", func(i *Issue) { i.Session = strings.Repeat("s", maxSessionLen+1) }, "session"},
		{"session newline", func(i *Issue) { i.Session = "a\nb" }, "session"},
		{"session control char", func(i *Issue) { i.Session = "a\x01b" }, "session"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			iss := base()
			tc.mutate(iss)
			err := validateFields(iss)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want *ValidationError", err)
			}
			if ve.Field != tc.wantField {
				t.Errorf("Field = %q, want %q", ve.Field, tc.wantField)
			}
		})
	}
}

func TestValidateFields_AgentAndSessionAtBound_Accepted(t *testing.T) {
	iss := &Issue{
		ID: "agt-0001", Title: "t", Status: StatusOpen, Type: TypeTask, Priority: 2,
		Agent:   strings.Repeat("a", maxAgentLen),
		Session: strings.Repeat("s", maxSessionLen),
	}
	if err := validateFields(iss); err != nil {
		t.Errorf("validateFields at the length bound = %v, want nil", err)
	}
}

// A write is refused for a violation it introduces, not one it finds
// (TASK-STORAGE-SPEC §10). agent and session are never editable, so a
// hand-edited value that breaks the bound must not freeze the issue.
func TestFieldUnchanged_AgentAndSession(t *testing.T) {
	prev := &Issue{Agent: "claude-code", Session: "s-1"}
	same := &Issue{Agent: "claude-code", Session: "s-1"}
	other := &Issue{Agent: "cursor", Session: "s-2"}

	for _, field := range []string{"agent", "session"} {
		if !fieldUnchanged(field, prev, same) {
			t.Errorf("fieldUnchanged(%q) on identical issues = false, want true", field)
		}
		if fieldUnchanged(field, prev, other) {
			t.Errorf("fieldUnchanged(%q) on differing issues = true, want false", field)
		}
	}
}

// ── L1: serialization round-trip ─────────────────────────────────────────────

func TestMarshalUnmarshal_ProvenanceRoundTrips(t *testing.T) {
	iss := &Issue{
		ID: "agt-0001", Title: "t", Status: StatusOpen, Type: TypeTask, Priority: 2,
		Creator: "hans", Agent: "claude-code", Session: "81735307-43f7",
	}
	data, err := Marshal(iss)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), "agent: claude-code") {
		t.Errorf("frontmatter missing the agent field:\n%s", data)
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Agent != iss.Agent || got.Session != iss.Session {
		t.Errorf("round-trip = (%q, %q), want (%q, %q)", got.Agent, got.Session, iss.Agent, iss.Session)
	}
}

// ── L1: comment document constraints ─────────────────────────────────────────

func TestValidateCommentDoc_ProvenanceConstraints(t *testing.T) {
	tests := []struct {
		name      string
		doc       Comment
		wantField string
	}{
		{"agent newline", Comment{Body: "x", Agent: "a\nb"}, "agent"},
		{"session newline", Comment{Body: "x", Session: "a\nb"}, "session"},
		{"author over length", Comment{Body: "x", Author: strings.Repeat("a", maxActorFieldLen+1)}, "author"},
		// A tombstone carries provenance like any other document, so it is
		// checked on that path too — it has no body to fail on instead.
		{"tombstone agent newline", Comment{Deleted: true, Replaces: "abcdefgh", Agent: "a\nb"}, "agent"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCommentDoc(tc.doc)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want *ValidationError", err)
			}
			if ve.Field != tc.wantField {
				t.Errorf("Field = %q, want %q", ve.Field, tc.wantField)
			}
		})
	}
}

// ── L1: criteria compilation ─────────────────────────────────────────────────

func TestCriteriaBuild_AgentAndSession(t *testing.T) {
	got, err := Criteria{Agent: "claude-code", Session: "s-1"}.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := `agent == "claude-code" && session == "s-1"`
	if got != want {
		t.Errorf("Build() = %q, want %q", got, want)
	}
	// An empty value adds no clause: "filed by no agent" is agent == "" passed
	// to List directly, not something Criteria can express.
	empty, err := Criteria{}.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if empty != "" {
		t.Errorf("zero Criteria compiled to %q, want the empty expression", empty)
	}
}

// ── L2: the store carries provenance verbatim ────────────────────────────────

func TestCreate_RecordsAgentAndSession(t *testing.T) {
	s, _ := newMemStore(t)
	created, err := unwrap(s.Create(CreateInput{
		Title: "filed by an agent", Creator: "hans",
		Agent: "claude-code", Session: "s-1",
	}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Agent != "claude-code" || got.Session != "s-1" {
		t.Errorf("stored provenance = (%q, %q), want (claude-code, s-1)", got.Agent, got.Session)
	}
}

// Provenance is set once at creation. UpdateInput cannot name it, so an ordinary
// update must carry it through untouched rather than blanking it.
func TestUpdate_PreservesProvenance(t *testing.T) {
	s, _ := newMemStore(t)
	created, err := unwrap(s.Create(CreateInput{Title: "x", Agent: "claude-code", Session: "s-1"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	title := "renamed"
	if _, err := s.Update(created.ID, UpdateInput{Title: &title}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := s.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Agent != "claude-code" || got.Session != "s-1" {
		t.Errorf("after update, provenance = (%q, %q), want it unchanged", got.Agent, got.Session)
	}
}

func TestList_FiltersOnAgentAndSession(t *testing.T) {
	s, _ := newMemStore(t)
	byAgent, err := unwrap(s.Create(CreateInput{Title: "agent filed", Agent: "claude-code", Session: "s-1"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	byHand, err := unwrap(s.Create(CreateInput{Title: "hand filed"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, tc := range []struct {
		expr string
		want string
	}{
		{`agent == "claude-code"`, byAgent.ID},
		{`session == "s-1"`, byAgent.ID},
		{`agent == ""`, byHand.ID},
	} {
		got, err := s.List(Filter{Expr: tc.expr})
		if err != nil {
			t.Fatalf("List(%s): %v", tc.expr, err)
		}
		if len(got) != 1 || got[0].ID != tc.want {
			t.Errorf("List(%s) returned %d issues, want just %s", tc.expr, len(got), tc.want)
		}
	}
}

// ── L2: comments carry the actor of each document ────────────────────────────

func TestAddComment_RecordsActor(t *testing.T) {
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	by := Actor{Name: "hans", Agent: "claude-code", Session: "s-1"}
	if _, err := s.AddComment(iss.ID, by, "a note\n"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	got, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d comments, want 1", len(got))
	}
	if got[0].Author != "hans" || got[0].Agent != "claude-code" || got[0].Session != "s-1" {
		t.Errorf("comment provenance = (%q, %q, %q), want (hans, claude-code, s-1)",
			got[0].Author, got[0].Agent, got[0].Session)
	}
}

// A revision records who wrote *it*, not who wrote the comment it supersedes
// (TASK-STORAGE-SPEC §4.4 rule 8).
func TestEditComment_RecordsItsOwnActor(t *testing.T) {
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	orig, err := s.AddComment(iss.ID, Actor{Name: "hans"}, "original\n")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	editor := Actor{Name: "hans", Agent: "claude-code", Session: "s-1"}
	if _, err := s.EditComment(iss.ID, orig.ID, editor, "revised\n"); err != nil {
		t.Fatalf("EditComment: %v", err)
	}
	got, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d effective comments, want 1", len(got))
	}
	if got[0].Agent != "claude-code" {
		t.Errorf("revision agent = %q, want claude-code — the reviser's, not the original author's", got[0].Agent)
	}
}

// The tombstone is a document like any other, so it carries the deleter's
// provenance even though the resolved log no longer shows the comment.
func TestDeleteComment_TombstoneCarriesActor(t *testing.T) {
	s, m := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "x"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	c, err := s.AddComment(iss.ID, Actor{Name: "hans"}, "to be deleted\n")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	by := Actor{Name: "hans", Agent: "claude-code", Session: "s-1"}
	if err := s.DeleteComment(iss.ID, c.ID, by); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}

	raw, err := m.ReadFile(s.commentsPath(iss.ID))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	stream, err := parseCommentStream(raw)
	if err != nil {
		t.Fatalf("parseCommentStream: %v", err)
	}
	if len(stream) != 2 {
		t.Fatalf("stream has %d documents, want 2", len(stream))
	}
	tomb := stream[1]
	if !tomb.Deleted {
		t.Fatalf("second document is not a tombstone: %+v", tomb)
	}
	if tomb.Agent != "claude-code" || tomb.Session != "s-1" {
		t.Errorf("tombstone provenance = (%q, %q), want (claude-code, s-1)", tomb.Agent, tomb.Session)
	}
}

// ── L2: import records the provenance it is given ────────────────────────────

func TestImport_CarriesProvenanceVerbatim(t *testing.T) {
	s, _ := newMemStore(t)
	res, err := s.Import(ImportInput{
		Title: "imported", Creator: "alice", Agent: "gemini-cli", Session: "s-9",
		Comments: []ImportComment{{Author: "bob", Agent: "cursor", Session: "s-8", Body: "note"}},
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	got, err := s.Get(res.Issue.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Agent != "gemini-cli" || got.Session != "s-9" {
		t.Errorf("issue provenance = (%q, %q), want (gemini-cli, s-9)", got.Agent, got.Session)
	}
	cs, err := s.Comments(res.Issue.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(cs) != 1 {
		t.Fatalf("got %d comments, want 1", len(cs))
	}
	if cs[0].Agent != "cursor" || cs[0].Session != "s-8" {
		t.Errorf("comment provenance = (%q, %q), want (cursor, s-8)", cs[0].Agent, cs[0].Session)
	}
}

// ── L2: the hook payload exposes it ──────────────────────────────────────────

func TestBuildHookPayload_IncludesProvenance(t *testing.T) {
	iss := &Issue{
		ID: "agt-0001", Title: "t", Status: StatusOpen, Type: TypeTask, Priority: 2,
		Agent: "claude-code", Session: "s-1",
	}
	data, err := buildHookPayload("pre-create", nil, iss)
	if err != nil {
		t.Fatalf("buildHookPayload: %v", err)
	}
	for _, want := range []string{`"agent":"claude-code"`, `"session":"s-1"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("payload missing %s:\n%s", want, data)
		}
	}

	// Absent when a person filed the issue: a gate reading them must see no key
	// rather than an empty one.
	plain := &Issue{ID: "agt-0002", Title: "t", Status: StatusOpen, Type: TypeTask, Priority: 2}
	data, err = buildHookPayload("pre-create", nil, plain)
	if err != nil {
		t.Fatalf("buildHookPayload: %v", err)
	}
	if strings.Contains(string(data), `"agent"`) || strings.Contains(string(data), `"session"`) {
		t.Errorf("payload carries empty provenance keys:\n%s", data)
	}
}
