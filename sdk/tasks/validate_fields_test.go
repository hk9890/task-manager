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

// validate_fields_test.go — table-driven tests for §4 field constraints.
// All cases call validateFields directly (L1, no FS).
// Constraints come from TASK-STORAGE-SPEC §4 + §10.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// baseIssue returns a minimal valid issue that can be mutated per test case.
func baseIssue() *Issue {
	return &Issue{
		ID:     "agt-0001",
		Title:  "A valid title",
		Status: StatusOpen,
		Type:   TypeTask,
		// Priority zero (critical) is valid.
	}
}

// makeIDsN builds n distinct IDs of the form agt-NNNN starting at 1000,
// none of which equals "agt-0001" (the baseIssue ID).
func makeIDsN(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("agt-%04d", 1000+i)
	}
	return ids
}

type fieldCase struct {
	name      string
	mutate    func(*Issue)
	wantErr   bool
	wantField string // only checked when wantErr == true
}

func TestValidateFieldConstraints(t *testing.T) {
	cases := []fieldCase{
		// ── title ──────────────────────────────────────────────────────────────
		{
			name:      "title empty string",
			mutate:    func(i *Issue) { i.Title = "" },
			wantErr:   true,
			wantField: "title",
		},
		{
			name:      "title only whitespace",
			mutate:    func(i *Issue) { i.Title = "   " },
			wantErr:   true,
			wantField: "title",
		},
		{
			name:      "title 201 chars (over limit)",
			mutate:    func(i *Issue) { i.Title = strings.Repeat("a", 201) },
			wantErr:   true,
			wantField: "title",
		},
		{
			name:    "title 200 chars (at limit, accepted)",
			mutate:  func(i *Issue) { i.Title = strings.Repeat("a", 200) },
			wantErr: false,
		},
		{
			name:      "title with embedded LF",
			mutate:    func(i *Issue) { i.Title = "line one\nline two" },
			wantErr:   true,
			wantField: "title",
		},
		{
			name:      "title with NUL control char",
			mutate:    func(i *Issue) { i.Title = "bad\x00title" },
			wantErr:   true,
			wantField: "title",
		},
		{
			name:      "title with CR control char",
			mutate:    func(i *Issue) { i.Title = "bad\rtitle" },
			wantErr:   true,
			wantField: "title",
		},
		{
			name:      "title with TAB control char",
			mutate:    func(i *Issue) { i.Title = "bad\ttitle" },
			wantErr:   true,
			wantField: "title",
		},
		{
			name:    "title valid unicode within 200 runes",
			mutate:  func(i *Issue) { i.Title = strings.Repeat("é", 100) },
			wantErr: false,
		},
		{
			name:      "title 5000 chars (way over limit)",
			mutate:    func(i *Issue) { i.Title = strings.Repeat("x", 5000) },
			wantErr:   true,
			wantField: "title",
		},
		{
			name:    "title unicode 200 runes exactly (multi-byte chars, accepted)",
			mutate:  func(i *Issue) { i.Title = strings.Repeat("日", 200) },
			wantErr: false,
		},
		{
			name:      "title unicode 201 runes (over limit despite being unicode)",
			mutate:    func(i *Issue) { i.Title = strings.Repeat("日", 201) },
			wantErr:   true,
			wantField: "title",
		},

		// ── assignee ───────────────────────────────────────────────────────────
		{
			name:      "assignee 129 chars (over limit)",
			mutate:    func(i *Issue) { i.Assignee = strings.Repeat("a", 129) },
			wantErr:   true,
			wantField: "assignee",
		},
		{
			name:    "assignee 128 chars (at limit, accepted)",
			mutate:  func(i *Issue) { i.Assignee = strings.Repeat("a", 128) },
			wantErr: false,
		},
		{
			name:      "assignee with LF",
			mutate:    func(i *Issue) { i.Assignee = "alice\nbob" },
			wantErr:   true,
			wantField: "assignee",
		},
		{
			name:      "assignee with NUL",
			mutate:    func(i *Issue) { i.Assignee = "alice\x00" },
			wantErr:   true,
			wantField: "assignee",
		},
		{
			name:    "assignee empty (allowed)",
			mutate:  func(i *Issue) { i.Assignee = "" },
			wantErr: false,
		},
		{
			name:    "assignee normal name (allowed)",
			mutate:  func(i *Issue) { i.Assignee = "hans" },
			wantErr: false,
		},
		{
			name:      "assignee with TAB control char",
			mutate:    func(i *Issue) { i.Assignee = "alice\tbob" },
			wantErr:   true,
			wantField: "assignee",
		},

		// ── labels ─────────────────────────────────────────────────────────────
		{
			name: "labels 65 items (over limit)",
			mutate: func(i *Issue) {
				labels := make([]string, 65)
				for j := range labels {
					labels[j] = "area"
				}
				i.Labels = labels
			},
			wantErr:   true,
			wantField: "labels",
		},
		{
			name: "labels 64 items (at limit, accepted)",
			mutate: func(i *Issue) {
				labels := make([]string, 64)
				for j := range labels {
					labels[j] = "a"
				}
				i.Labels = labels
			},
			wantErr: false,
		},
		{
			name:      "label 65 chars (over per-label length limit)",
			mutate:    func(i *Issue) { i.Labels = []string{strings.Repeat("a", 65)} },
			wantErr:   true,
			wantField: "labels",
		},
		{
			name:    "label 64 chars (at per-label limit, accepted)",
			mutate:  func(i *Issue) { i.Labels = []string{strings.Repeat("a", 64)} },
			wantErr: false,
		},
		{
			name:      "label starts with uppercase (bad pattern)",
			mutate:    func(i *Issue) { i.Labels = []string{"Area:foo"} },
			wantErr:   true,
			wantField: "labels",
		},
		{
			name:      "label starts with colon (bad pattern)",
			mutate:    func(i *Issue) { i.Labels = []string{":foo"} },
			wantErr:   true,
			wantField: "labels",
		},
		{
			name:      "label with space (bad pattern)",
			mutate:    func(i *Issue) { i.Labels = []string{"area foo"} },
			wantErr:   true,
			wantField: "labels",
		},
		{
			name:      "label with @ sign (bad pattern)",
			mutate:    func(i *Issue) { i.Labels = []string{"area@foo"} },
			wantErr:   true,
			wantField: "labels",
		},
		{
			name:      "label empty string (bad pattern)",
			mutate:    func(i *Issue) { i.Labels = []string{""} },
			wantErr:   true,
			wantField: "labels",
		},
		{
			name:    "label valid single char",
			mutate:  func(i *Issue) { i.Labels = []string{"a"} },
			wantErr: false,
		},
		{
			name:    "label valid with colon separator (area:sdk)",
			mutate:  func(i *Issue) { i.Labels = []string{"area:sdk"} },
			wantErr: false,
		},
		{
			name:    "label valid with dot, slash, dash",
			mutate:  func(i *Issue) { i.Labels = []string{"v1.0/beta-tag"} },
			wantErr: false,
		},
		{
			name:    "label valid digit start",
			mutate:  func(i *Issue) { i.Labels = []string{"2fa:enabled"} },
			wantErr: false,
		},
		{
			// §4 pattern ^[a-z0-9][a-z0-9:._/-]*$ includes underscore — it is valid.
			name:    "label valid with underscore (accepted per spec)",
			mutate:  func(i *Issue) { i.Labels = []string{"area_foo"} },
			wantErr: false,
		},

		// ── blocked_by count ───────────────────────────────────────────────────
		{
			name: "blocked_by 257 items (over limit)",
			mutate: func(i *Issue) {
				i.BlockedBy = makeIDsN(257)
			},
			wantErr:   true,
			wantField: "blocked_by",
		},
		{
			name: "blocked_by 256 items (at limit, accepted)",
			mutate: func(i *Issue) {
				i.BlockedBy = makeIDsN(256)
			},
			wantErr: false,
		},

		// ── related count ──────────────────────────────────────────────────────
		{
			name: "related 257 items (over limit)",
			mutate: func(i *Issue) {
				i.Related = makeIDsN(257)
			},
			wantErr:   true,
			wantField: "related",
		},
		{
			name: "related 256 items (at limit, accepted)",
			mutate: func(i *Issue) {
				i.Related = makeIDsN(256)
			},
			wantErr: false,
		},

		// ── combined valid edge cases ──────────────────────────────────────────
		{
			name: "all valid: 200-char title, 64-char label",
			mutate: func(i *Issue) {
				i.Title = strings.Repeat("a", 200)
				i.Labels = []string{strings.Repeat("a", 64)}
			},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			iss := baseIssue()
			tc.mutate(iss)
			err := validateFields(iss)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected validation error, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected *ValidationError, got %T: %v", err, err)
				}
				if ve.Field != tc.wantField {
					t.Errorf("error Field = %q; want %q (message: %s)", ve.Field, tc.wantField, ve.Message)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			}
		})
	}
}
