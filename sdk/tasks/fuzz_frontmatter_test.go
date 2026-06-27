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

// fuzz_frontmatter_test.go — Go native fuzz target for Unmarshal / Marshal round-trip.
//
// Property under test:
//   - FuzzFrontmatterUnmarshal: Unmarshal never panics for any byte input.
//   - For inputs where Unmarshal succeeds, Marshal(issue) must not error and
//     Unmarshal(Marshal(issue)) must parse cleanly (round-trip stability).
//
// Running modes:
//   go test -run=FuzzFrontmatterUnmarshal ./tasks/   -- seed corpus only (CI)
//   go test -fuzz=FuzzFrontmatterUnmarshal -fuzztime=30s ./tasks/  -- actual fuzzing

import (
	"testing"
	"time"
)

// FuzzFrontmatterUnmarshal fuzzes Unmarshal with arbitrary byte inputs.
//
// Invariants verified on every input:
//  1. Unmarshal does not panic.
//  2. If Unmarshal succeeds, Marshal must not error.
//  3. If Marshal succeeds, a second Unmarshal of its output must succeed.
//  4. The re-parsed issue's ID must equal the original (round-trip stability).
func FuzzFrontmatterUnmarshal(f *testing.F) {
	// ── seed: valid issue files ──────────────────────────────────────────────
	// These are well-formed inputs that must survive the round-trip check.
	validIssues := []string{
		// minimal open issue
		"---\nid: tst-0001\ntitle: minimal\nstatus: open\ntype: task\npriority: 2\ncreated: 2026-06-01T00:00:00Z\nupdated: 2026-06-01T00:00:00Z\n---\n",
		// closed issue with all optional fields
		"---\nid: tst-0002\ntitle: full issue\nstatus: closed\ntype: bug\npriority: 0\nassignee: hans\nlabels:\n  - area:db\n  - risk:high\nparent: tst-0007\nblocked_by:\n  - tst-0005\nrelated:\n  - tst-0006\ncreated: 2026-06-01T00:00:00Z\nupdated: 2026-06-04T09:00:00Z\nclosed: 2026-06-05T08:00:00Z\nclose_reason: shipped\n---\n\n## Description\n\nThis is the description body.\n",
		// in_progress with description containing fence-like lines
		"---\nid: tst-0003\ntitle: fence test\nstatus: in_progress\ntype: feature\npriority: 1\ncreated: 2026-06-01T00:00:00Z\nupdated: 2026-06-01T00:00:00Z\n---\n\nBody with --- line.\n\n---\n\nMore body.\n",
		// UTF-8 BOM prefix (tolerated by Unmarshal)
		"\xEF\xBB\xBF---\nid: tst-0004\ntitle: bom test\nstatus: open\ntype: task\npriority: 2\ncreated: 2026-06-01T00:00:00Z\nupdated: 2026-06-01T00:00:00Z\n---\n",
		// CRLF line endings (tolerated by the YAML parser)
		"---\r\nid: tst-0005\r\ntitle: crlf test\r\nstatus: open\r\ntype: task\r\npriority: 2\r\ncreated: 2026-06-01T00:00:00Z\r\nupdated: 2026-06-01T00:00:00Z\r\n---\r\n",
	}

	// ── seed: known-malformed inputs (Unmarshal must fail, not panic) ────────
	malformed := []string{
		"",
		"just plain text",
		"---\nid: [unclosed\n---\n",
		"---\nid: tst-0001\n", // unterminated
		"\xEF\xBB\xBF",        // BOM only
		"---",                 // fence without newline
		"---\n---",            // empty frontmatter no trailing newline
	}

	for _, s := range validIssues {
		f.Add([]byte(s))
	}
	for _, s := range malformed {
		f.Add([]byte(s))
	}

	// ── additional raw byte seeds for coverage ───────────────────────────────
	f.Add([]byte(nil))
	f.Add([]byte{})
	f.Add([]byte("---\n---\n"))                                      // empty frontmatter block
	f.Add([]byte("---\n\n---\n"))                                    // only newline in frontmatter
	f.Add([]byte("---\n" + string(make([]byte, 65536)) + "\n---\n")) // very large body

	f.Fuzz(func(t *testing.T, data []byte) {
		// ── invariant 1: no panic ─────────────────────────────────────────────
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Unmarshal(data) panicked: %v (data[:64]=%q)", r, truncate(data, 64))
			}
		}()

		iss, err := Unmarshal(data)
		if err != nil {
			// Malformed input: error is acceptable. Nothing more to check.
			return
		}
		if iss == nil {
			// Should not happen: successful Unmarshal must return non-nil.
			t.Errorf("Unmarshal succeeded but returned nil issue")
			return
		}

		// ── invariant 2: Marshal must not error ───────────────────────────────
		// Clamp potentially-zero timestamps so Marshal does not serialize
		// zero-value time.Time (which yaml.v3 renders differently).
		if iss.Created.IsZero() {
			iss.Created = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		}
		if iss.Updated.IsZero() {
			iss.Updated = iss.Created
		}
		marshaled, err2 := Marshal(iss)
		if err2 != nil {
			t.Errorf("Marshal(Unmarshal(data)) failed: %v (data[:64]=%q)", err2, truncate(data, 64))
			return
		}

		// ── invariant 3 & 4: second Unmarshal must succeed and preserve ID ────
		iss2, err3 := Unmarshal(marshaled)
		if err3 != nil {
			t.Errorf("Unmarshal(Marshal(Unmarshal(data))) failed: %v", err3)
			return
		}
		if iss2 == nil {
			t.Errorf("second Unmarshal returned nil without error")
			return
		}
		if iss2.ID != iss.ID {
			t.Errorf("round-trip ID mismatch: got %q, want %q", iss2.ID, iss.ID)
		}
	})
}

// truncate returns data[:n] if len(data) > n, else data.
// Used to keep error messages readable.
func truncate(data []byte, n int) []byte {
	if len(data) <= n {
		return data
	}
	return data[:n]
}
