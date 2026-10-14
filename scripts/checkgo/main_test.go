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

package main

// main_test.go — the two decisions the check makes: which version go.work pins,
// and whether a release is a newer patch of that version's minor.

import "testing"

func TestToolchainVersion_ReadsTheToolchainLine(t *testing.T) {
	gowork := []byte("go 1.27.0\n\ntoolchain go1.27.2\n\nuse (\n\t.\n\t./sdk\n)\n")
	got, err := toolchainVersion(gowork)
	if err != nil || got != "1.27.2" {
		t.Fatalf("toolchainVersion = %q, %v; want 1.27.2", got, err)
	}
}

func TestToolchainVersion_NoLine_Fails(t *testing.T) {
	if got, err := toolchainVersion([]byte("go 1.27.0\n\nuse .\n")); err == nil {
		t.Fatalf("toolchainVersion = %q, nil; want an error", got)
	}
}

func TestNewerPatch_ComparesOnlyTheSameMinorByNumber(t *testing.T) {
	releases := []string{"1.27.2", "1.26.9"}
	cases := []struct {
		pinned string
		want   string
		behind bool
	}{
		{"1.27.2", "", false},      // newest patch
		{"1.27.1", "1.27.2", true}, // a patch behind
		{"1.26.3", "1.26.9", true}, // the older supported minor, behind
		{"1.26.9", "", false},      // a newer minor exists: not an update
		{"1.25.4", "", false},      // a minor the feed no longer lists
		{"1.27.10", "", false},     // patch compared as a number, not as text
		{"1.27", "", false},        // not major.minor.patch
		{"1.27rc1", "", false},     // not major.minor.patch
	}
	for _, c := range cases {
		got, behind := newerPatch(c.pinned, releases)
		if got != c.want || behind != c.behind {
			t.Errorf("newerPatch(%q) = %q, %v; want %q, %v", c.pinned, got, behind, c.want, c.behind)
		}
	}
}

func TestNewerPatch_PicksTheHighest(t *testing.T) {
	got, behind := newerPatch("1.27.0", []string{"1.27.1", "1.27.3", "1.27.2"})
	if got != "1.27.3" || !behind {
		t.Fatalf("newerPatch = %q, %v; want 1.27.3, true", got, behind)
	}
}
