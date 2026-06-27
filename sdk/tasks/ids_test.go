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
	"strings"
	"testing"
)

// TestIDStem covers the pure stem parser for both legacy numeric IDs and the
// new base36 tokens. L1: no FS, no store.
func TestIDStem(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		input    string
		wantStem string
		wantOK   bool
	}{
		{"legacy numeric", "agt", "agt-0042.md", "agt-0042", true},
		{"base36 token", "agt", "agt-3k9f2x.md", "agt-3k9f2x", true},
		{"wrong prefix", "agt", "xyz-0001.md", "", false},
		{"no extension", "agt", "agt-0001", "", false},
		{"empty token after dash", "agt", "agt-.md", "", false},
		{"hidden file", "agt", ".lock", "", false},
		{"prefix substring is not a match", "ag", "agt-0001.md", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stem, ok := idStem(tc.prefix, tc.input)
			if ok != tc.wantOK || stem != tc.wantStem {
				t.Errorf("idStem(%q, %q) = (%q, %v); want (%q, %v)",
					tc.prefix, tc.input, stem, ok, tc.wantStem, tc.wantOK)
			}
		})
	}
}

// TestRandToken checks length and alphabet.
func TestRandToken(t *testing.T) {
	for _, n := range []int{1, 6, 12} {
		tok := randToken(n)
		if len(tok) != n {
			t.Errorf("randToken(%d) length = %d, want %d", n, len(tok), n)
		}
		for _, c := range tok {
			if !strings.ContainsRune(idAlphabet, c) {
				t.Errorf("randToken(%d) = %q contains non-base36 rune %q", n, tok, c)
			}
		}
	}
}

// TestNewIDFromNames covers the collision-resistant allocator: every ID is
// well-formed, carries the prefix, and never collides with an existing entry
// (including legacy numeric IDs already on disk — back-compat).
func TestNewIDFromNames(t *testing.T) {
	t.Run("format and prefix", func(t *testing.T) {
		id := newIDFromNames("agt", nil)
		if !strings.HasPrefix(id, "agt-") || !idRe.MatchString(id) {
			t.Errorf("newIDFromNames(agt, nil) = %q, want a valid agt- prefixed ID", id)
		}
		if want := len("agt-") + idTokenLen; len(id) != want {
			t.Errorf("len(%q) = %d, want %d", id, len(id), want)
		}
	})

	t.Run("never collides with existing names", func(t *testing.T) {
		// Mix of legacy numeric and base36 entries; generate many and assert
		// none reuse an existing stem and all are unique among themselves.
		existing := []string{"agt-0001.md", "agt-0002.md", "agt-3k9f2x.md", "other-0099.md"}
		taken := map[string]bool{"agt-0001": true, "agt-0002": true, "agt-3k9f2x": true}
		seen := map[string]bool{}
		for i := 0; i < 1000; i++ {
			id := newIDFromNames("agt", existing)
			if taken[id] {
				t.Fatalf("newIDFromNames re-issued existing ID %q", id)
			}
			if seen[id] {
				t.Fatalf("newIDFromNames returned duplicate %q within run", id)
			}
			seen[id] = true
			if !strings.HasPrefix(id, "agt-") || !idRe.MatchString(id) {
				t.Fatalf("newIDFromNames produced malformed ID %q", id)
			}
		}
	})
}
