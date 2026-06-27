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

import (
	"encoding/json"
	"testing"
	"time"
)

// L1: the payload serializer is pure. HOOK-SPEC §5. Golden JSON pins the exact
// shape (field order + omitempty), which must stay identical to cmd's issueDTO.

func TestBuildHookPayload_FullIssueGolden(t *testing.T) {
	ts := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	created := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	iss := &Issue{
		ID: "dtt-0042", Title: "Fix drill navigation",
		Status: StatusClosed, Type: TypeBug, Priority: 1,
		Assignee: "hans", Creator: "hans",
		Labels: []string{"area:details"},
		Parent: "dtt-0007", BlockedBy: []string{"dtt-0040"}, Related: []string{"dtt-0012"},
		Created: created, Updated: ts, Closed: ts, CloseReason: "fixed",
		Description: "## Description\nDrilling.",
	}
	old := &Issue{
		ID: "dtt-0042", Title: "Fix drill navigation",
		Status: StatusInProgress, Type: TypeBug, Priority: 1, Creator: "hans",
		Created: created, Updated: created,
	}

	got, err := buildHookPayload("pre-close", old, iss)
	if err != nil {
		t.Fatal(err)
	}

	want := `{"schema":1,"event":"pre-close","issue_id":"dtt-0042",` +
		`"old":{"id":"dtt-0042","title":"Fix drill navigation","status":"in_progress","type":"bug","priority":1,"creator":"hans","created":"2026-06-01T10:00:00Z","updated":"2026-06-01T10:00:00Z"},` +
		`"new":{"id":"dtt-0042","title":"Fix drill navigation","status":"closed","type":"bug","priority":1,"assignee":"hans","creator":"hans","labels":["area:details"],"parent":"dtt-0007","blocked_by":["dtt-0040"],"related":["dtt-0012"],"created":"2026-06-01T10:00:00Z","updated":"2026-06-13T09:00:00Z","closed":"2026-06-13T09:00:00Z","close_reason":"fixed","description":"## Description\nDrilling."}}`

	if string(got) != want {
		t.Errorf("payload mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestBuildHookPayload_CreateHasNullOld(t *testing.T) {
	ts := time.Date(2026, 6, 13, 11, 0, 0, 0, time.UTC)
	iss := &Issue{
		ID: "dtt-0050", Title: "Add export", Status: StatusOpen, Type: TypeFeature,
		Priority: 2, Creator: "hans", Created: ts, Updated: ts,
		Description: "## Goal\nExport.\n",
	}
	got, err := buildHookPayload("pre-create", nil, iss)
	if err != nil {
		t.Fatal(err)
	}

	// old must be explicit null (present, not omitted).
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(got, &envelope); err != nil {
		t.Fatal(err)
	}
	raw, ok := envelope["old"]
	if !ok {
		t.Fatal(`payload must include the "old" key for a create`)
	}
	if string(raw) != "null" {
		t.Errorf(`"old" = %s, want null`, raw)
	}
	if string(envelope["issue_id"]) != `"dtt-0050"` {
		t.Errorf("issue_id = %s, want dtt-0050 (== new.id)", envelope["issue_id"])
	}
	if string(envelope["schema"]) != "1" {
		t.Errorf("schema = %s, want 1", envelope["schema"])
	}
}

func TestToHookIssue_OmitsEmptyOptionals(t *testing.T) {
	ts := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	iss := &Issue{
		ID: "x-1", Title: "minimal", Status: StatusOpen, Type: TypeTask,
		Priority: 2, Created: ts, Updated: ts,
	}
	data, err := json.Marshal(toHookIssue(iss))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	// Required keys always present.
	for _, k := range []string{"id", "title", "status", "type", "priority", "created", "updated"} {
		if _, ok := m[k]; !ok {
			t.Errorf("required key %q missing", k)
		}
	}
	// Empty optionals omitted.
	for _, k := range []string{"assignee", "creator", "labels", "parent", "blocked_by", "related", "closed", "close_reason", "description"} {
		if _, ok := m[k]; ok {
			t.Errorf("empty optional key %q must be omitted", k)
		}
	}
	if toHookIssue(nil) != nil {
		t.Error("toHookIssue(nil) must be nil (-> JSON null)")
	}
}
