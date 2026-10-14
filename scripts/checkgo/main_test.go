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

// main_test.go — the three decisions the check makes: which version go.work
// pins, whether go.dev still releases that version's minor, and whether a
// release is a newer patch of it.

import (
	"strings"
	"testing"
)

func TestToolchainVersion_ReadsTheToolchainLine(t *testing.T) {
	gowork := []byte("go 1.27.0\n\ntoolchain go1.27.2\n\nuse (\n\t.\n\t./sdk\n)\n")
	got, err := toolchainVersion(gowork)
	if err != nil || got != "1.27.2" {
		t.Fatalf("toolchainVersion = %q, %v; want 1.27.2", got, err)
	}
}

func TestToolchainVersion_TabAndTrailingComment_ReadsTheVersion(t *testing.T) {
	got, err := toolchainVersion([]byte("go 1.27.0\ntoolchain\tgo1.27.2 // bumped by hand\n"))
	if err != nil || got != "1.27.2" {
		t.Fatalf("toolchainVersion = %q, %v; want 1.27.2", got, err)
	}
}

func TestToolchainVersion_NoLine_Fails(t *testing.T) {
	if got, err := toolchainVersion([]byte("go 1.27.0\n\nuse .\n")); err == nil {
		t.Fatalf("toolchainVersion = %q, nil; want an error", got)
	}
}

func TestToolchainVersion_NotAPatchRelease_Fails(t *testing.T) {
	if got, err := toolchainVersion([]byte("go 1.27.0\ntoolchain go1.28rc1\n")); err == nil {
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
		{"1.25.4", "", false},      // a minor the feed no longer lists: listsMinor fails it
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

func TestListsMinor_UnsupportedMinor_IsNotListed(t *testing.T) {
	releases := []string{"1.27.2", "1.26.9"}
	cases := []struct {
		pinned string
		listed bool
	}{
		{"1.27.2", true},
		{"1.27.0", true},  // behind, but supported: newerPatch reports it
		{"1.26.3", true},  // the older supported minor
		{"1.25.4", false}, // end of support
		{"1.28.0", false}, // newer than the feed knows
	}
	for _, c := range cases {
		listed, minors := listsMinor(c.pinned, releases)
		if listed != c.listed {
			t.Errorf("listsMinor(%q) listed = %v; want %v", c.pinned, listed, c.listed)
		}
		if got := strings.Join(minors, ", "); got != "go1.27, go1.26" {
			t.Errorf("listsMinor(%q) minors = %q; want \"go1.27, go1.26\"", c.pinned, got)
		}
	}
}
