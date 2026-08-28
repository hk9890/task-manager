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

// main_test.go — the two classifications that decide whether the gate fires: the
// anchor table a link fragment is checked against, and what counts as a cited
// path. Both were wrong in opposite directions — one accepted links that 404,
// the other rejected citations that resolve.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestAnchorsOf_SkipsFencedBlocks: GitHub mints no anchor for a `#` line inside
// a fence, so counting one made the gate accept a link that 404s.
func TestAnchorsOf_SkipsFencedBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.md")
	writeFile(t, path, strings.Join([]string{
		"# Real heading",
		"",
		"```sh",
		"# not a real heading",
		"```",
		"",
		"~~~yaml",
		"## also not one",
		"~~~",
		"",
		"## Second real heading",
	}, "\n"))

	got := anchorsOf(path)
	for _, want := range []string{"real-heading", "second-real-heading"} {
		if !got[want] {
			t.Errorf("anchor %q missing from %v", want, got)
		}
	}
	for _, unwanted := range []string{"not-a-real-heading", "also-not-one"} {
		if got[unwanted] {
			t.Errorf("anchor %q was derived from a line inside a fenced block", unwanted)
		}
	}
}

// TestCheckLinks_RejectsAFragmentThatOnlyExistsInAFence is the gate's own
// behaviour end to end.
func TestCheckLinks_RejectsAFragmentThatOnlyExistsInAFence(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "b.md"), "# Title\n\n```sh\n# fenced heading\n```\n")

	got := checkLinks("docs/a.md", "See [B](b.md#fenced-heading).\n", root, map[string]map[string]bool{})
	if len(got) != 1 {
		t.Fatalf("findings = %+v, want one dead anchor", got)
	}
	if !strings.Contains(got[0].msg, "fenced-heading") {
		t.Errorf("finding %q does not name the anchor", got[0].msg)
	}
}

// TestCheckCitations_AcceptsACitationThatEndsASentence: the trailing period is
// punctuation, not part of the path. Reporting it failed the gate — and CI with
// it — on a citation that is correct.
func TestCheckCitations_AcceptsACitationThatEndsASentence(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "root.go"), "package cmd\n")

	for _, line := range []string{
		"The entry point lives in cmd/root.go.",
		"The entry point lives in cmd/root.go, then returns.",
		"The entry point lives in `cmd/root.go`.",
	} {
		if got := checkCitations("docs/a.md", line+"\n", root); len(got) != 0 {
			t.Errorf("%q reported %+v, want no finding", line, got)
		}
	}
}

// TestCheckCitations_StillFlagsWhatItIsFor guards the trim: it must not turn a
// dead path into a passing one.
func TestCheckCitations_StillFlagsWhatItIsFor(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "root.go"), "package cmd\n")

	cases := map[string]string{
		"missing path":      "It moved to cmd/gone.go.",
		"line-anchored":     "See cmd/root.go:42 for the loop.",
		"missing with dots": "It moved to cmd/gone.go...",
	}
	for name, line := range cases {
		if got := checkCitations("docs/a.md", line+"\n", root); len(got) != 1 {
			t.Errorf("%s: findings = %+v, want exactly one", name, got)
		}
	}
}

// TestCheckLinks_ChecksATitledLink: a target followed by a title matched the
// link pattern nowhere, so the link was skipped entirely — a gate that fails
// open is worse than no gate, because the doc reads as checked.
func TestCheckLinks_ChecksATitledLink(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "b.md"), "# Title\n")

	got := checkLinks("docs/a.md", `See [B](gone.md "The Title").`+"\n", root, map[string]map[string]bool{})
	if len(got) != 1 {
		t.Fatalf("findings = %+v, want the dead target reported", got)
	}
	if !strings.Contains(got[0].msg, "gone.md") {
		t.Errorf("finding %q does not name the target", got[0].msg)
	}
	// A titled link that resolves is still fine.
	got = checkLinks("docs/a.md", `See [B](b.md "The Title").`+"\n", root, map[string]map[string]bool{})
	if len(got) != 0 {
		t.Errorf("a resolving titled link reported %+v", got)
	}
}

// TestResolves_ModuleTagIsTheVersionShapeNotThePrefix: `sdk/vX.Y.Z` names a
// release tag rather than a path, but exempting everything under `sdk/v`
// exempted real paths too — `sdk/validate.go` is the letter v.
func TestResolves_ModuleTagIsTheVersionShapeNotThePrefix(t *testing.T) {
	root := t.TempDir()

	exempt := []string{"sdk/v0.7.0", "sdk/vX.Y.Z", "sdk/v1.0.0-rc.1"}
	for _, token := range exempt {
		if !resolves(root, token) {
			t.Errorf("%q is a module tag and must be accepted", token)
		}
	}
	// None of these exist under root, and none is a tag.
	for _, token := range []string{"sdk/validate.go", "sdk/version/build.go", "sdk/v2things.go"} {
		if resolves(root, token) {
			t.Errorf("%q is a path, not a tag: it must be checked against disk", token)
		}
	}
}
