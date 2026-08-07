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
	"errors"
	"strings"
	"testing"
	"time"
)

// L1: the overflow layout rule and rendering are pure — no store, no filesystem.

// TestLayoutFor_Hysteresis pins the split/join thresholds and, most importantly,
// the band between them where the layout does NOT change. That band is the whole
// point: without it an issue hovering near the cap flips representation on every
// edit, and each flip is a maximal git diff in both directions.
func TestLayoutFor_Hysteresis(t *testing.T) {
	tests := []struct {
		name         string
		size         int
		prevExternal bool
		want         bool
	}{
		// Currently inline: split only above the cap.
		{"inline, empty", 0, false, false},
		{"inline, just under cap", MaxInlineBody - 1, false, false},
		{"inline, exactly at cap", MaxInlineBody, false, false},
		{"inline, one over cap", MaxInlineBody + 1, false, true},
		{"inline, far over cap", MaxInlineBody * 10, false, true},

		// Currently external: join only below the floor.
		{"external, far over cap", MaxInlineBody * 10, true, true},
		{"external, just over cap", MaxInlineBody + 1, true, true},
		// The band: under the cap but not under the floor -> stays external.
		{"external, in the band at cap", MaxInlineBody, true, true},
		{"external, mid band", (MaxInlineBody + joinInlineBody) / 2, true, true},
		{"external, exactly at floor", joinInlineBody, true, true},
		// Below the floor -> rejoin.
		{"external, one under floor", joinInlineBody - 1, true, false},
		{"external, empty", 0, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := layoutFor(strings.Repeat("x", tc.size), tc.prevExternal); got != tc.want {
				t.Fatalf("layoutFor(%d bytes, prevExternal=%v) = %v, want %v",
					tc.size, tc.prevExternal, got, tc.want)
			}
		})
	}
}

// TestLayoutFor_MeasuresTrimmedBody verifies the decision uses the same trimmed
// body that actually gets written, not the caller's raw string. A body that is
// only over the cap because of surrounding blank lines must stay inline, or the
// stored bytes and the decision would disagree.
func TestLayoutFor_MeasuresTrimmedBody(t *testing.T) {
	body := "\n\n\n" + strings.Repeat("x", MaxInlineBody) + "\n\n\n"
	if len(body) <= MaxInlineBody {
		t.Fatalf("test setup: raw body should exceed the cap, got %d", len(body))
	}
	if layoutFor(body, false) {
		t.Fatal("body that fits the cap after trimming must stay inline")
	}
}

func testIssue(body string) *Issue {
	return &Issue{
		ID: "agt-000001", Title: "t", Status: StatusOpen, Type: TypeTask,
		Priority: PriorityDefault,
		Created:  time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		Updated:  time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),

		Description: body,
	}
}

// TestRenderForWrite_Inline: a small body stays in the .md, no sidecar is
// produced, and no flag is emitted.
func TestRenderForWrite_Inline(t *testing.T) {
	iss := testIssue("a short body")
	md, sidecar, drop, err := renderForWrite(iss, false)
	if err != nil {
		t.Fatalf("renderForWrite: %v", err)
	}
	if sidecar != nil {
		t.Fatalf("expected no sidecar, got %d bytes", len(sidecar))
	}
	if drop {
		t.Fatal("expected no sidecar removal for an issue that was never external")
	}
	if !strings.Contains(string(md), "a short body") {
		t.Fatal("body must stay in the .md")
	}
	if strings.Contains(string(md), "body_external") {
		t.Fatal("body_external must be omitted when the body is inline")
	}
}

// TestRenderForWrite_Split: an oversized body moves to the sidecar, the .md
// keeps only the frontmatter, and the flag is set so the sidecar is
// authoritative.
func TestRenderForWrite_Split(t *testing.T) {
	body := strings.Repeat("y", MaxInlineBody+1)
	iss := testIssue(body)
	md, sidecar, drop, err := renderForWrite(iss, false)
	if err != nil {
		t.Fatalf("renderForWrite: %v", err)
	}
	if string(sidecar) != body {
		t.Fatalf("sidecar must hold the whole body: got %d bytes, want %d", len(sidecar), len(body))
	}
	if drop {
		t.Fatal("a split must not ask for the sidecar to be removed")
	}
	if !strings.Contains(string(md), "body_external: true") {
		t.Fatalf("md must record the flag, got:\n%s", md)
	}
	if strings.Contains(string(md), body) {
		t.Fatal("the .md must not also contain the body")
	}

	// The .md must round-trip to an issue that knows its body is elsewhere.
	back, err := Unmarshal(md)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !back.bodyExternal {
		t.Fatal("round-tripped issue must carry bodyExternal")
	}
	if back.Description != "" {
		t.Fatalf("round-tripped body must be empty, got %d bytes", len(back.Description))
	}
}

// TestRenderForWrite_Join: shrinking below the floor brings the body back inline
// and asks for the now-stale sidecar to be removed.
func TestRenderForWrite_Join(t *testing.T) {
	iss := testIssue("now small again")
	md, sidecar, drop, err := renderForWrite(iss, true)
	if err != nil {
		t.Fatalf("renderForWrite: %v", err)
	}
	if sidecar != nil {
		t.Fatal("a join must not write a sidecar")
	}
	if !drop {
		t.Fatal("a join must ask for the stale sidecar to be removed")
	}
	if !strings.Contains(string(md), "now small again") {
		t.Fatal("body must be back in the .md")
	}
	if strings.Contains(string(md), "body_external") {
		t.Fatal("the flag must be cleared once the body is inline")
	}
}

// TestRenderForWrite_DoesNotMutateInput guards the property the read/write seam
// depends on: the caller's issue keeps its full Description and a clear flag, so
// it stays safe to hand to Marshal after a write.
func TestRenderForWrite_DoesNotMutateInput(t *testing.T) {
	body := strings.Repeat("z", MaxInlineBody+1)
	iss := testIssue(body)
	if _, _, _, err := renderForWrite(iss, false); err != nil {
		t.Fatalf("renderForWrite: %v", err)
	}
	if iss.Description != body {
		t.Fatalf("input Description was modified: got %d bytes, want %d", len(iss.Description), len(body))
	}
	if iss.bodyExternal {
		t.Fatal("input flag was modified; the caller's issue must stay Marshal-safe")
	}
}

// TestMarshalUnmarshal_BodyExternalRoundTrip verifies the flag survives the
// serialization round-trip and is omitted when false, so ordinary issue files
// are byte-identical to what they were before this feature existed.
func TestMarshalUnmarshal_BodyExternalRoundTrip(t *testing.T) {
	iss := testIssue("")
	iss.bodyExternal = true
	data, err := Marshal(iss)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	back, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !back.bodyExternal {
		t.Fatal("flag did not survive the round-trip")
	}

	iss.bodyExternal = false
	data, err = Marshal(iss)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "body_external") {
		t.Fatalf("a false flag must be omitted entirely, got:\n%s", data)
	}
}

// TestTypeDoc_IsNotWork pins doc as the only non-work type. Ready, Blocked and
// the ready/blocked query predicates all key off this.
func TestTypeDoc_IsNotWork(t *testing.T) {
	if TypeDoc.IsWork() {
		t.Fatal("doc must not count as work")
	}
	if !TypeDoc.Valid() {
		t.Fatal("doc must be a valid type")
	}
	for _, ty := range []Type{TypeTask, TypeBug, TypeFeature, TypeEpic, TypeChore} {
		if !ty.IsWork() {
			t.Fatalf("%s must count as work", ty)
		}
	}
}

// TestValidateCommentBody_Cap verifies comments are bounded rather than
// overflowed: the cap that the spec always stated is now actually enforced.
func TestValidateCommentBody_Cap(t *testing.T) {
	if err := validateCommentBody(strings.Repeat("c", MaxCommentBody)); err != nil {
		t.Fatalf("a body exactly at the cap must be accepted: %v", err)
	}
	err := validateCommentBody(strings.Repeat("c", MaxCommentBody+1))
	if err == nil {
		t.Fatal("a body over the cap must be rejected")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "body" {
		t.Fatalf("want a *ValidationError on field body, got %#v", err)
	}
}
