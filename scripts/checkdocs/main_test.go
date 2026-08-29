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

// TestResolves_Wildcard: a glob citation is checked against disk like any other.
// The branch was reached by no test, so the gate would have accepted a wildcard
// that matches nothing — exactly the rot it exists to catch.
func TestResolves_Wildcard(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "root.go"), "package cmd\n")
	writeFile(t, filepath.Join(root, "sdk", "tasks", "store.go"), "package tasks\n")

	live := []string{"cmd/*.go", "sdk/tasks/*.go", "cmd/root*"}
	for _, token := range live {
		if !resolves(root, token) {
			t.Errorf("%q matches a file on disk and must be accepted", token)
		}
	}
	dead := []string{"cmd/*.md", "cmd/nosuch-*.go", "sdk/absent/*.go"}
	for _, token := range dead {
		if resolves(root, token) {
			t.Errorf("%q matches nothing on disk and must be reported", token)
		}
	}
}

// TestAnchorsOf_DuplicateHeadingsGetGitHubsSuffix: GitHub disambiguates a repeated
// heading by appending -1, -2, … to the later slugs. Nothing repeated a heading in
// the fixtures, so the rule was unasserted — and an off-by-one there makes the gate
// reject valid links into any document with two identically named sections.
func TestAnchorsOf_DuplicateHeadingsGetGitHubsSuffix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.md")
	writeFile(t, path, strings.Join([]string{
		"# Page",
		"",
		"## Conventions",
		"",
		"## Conventions",
		"",
		"## Conventions",
	}, "\n"))

	got := anchorsOf(path)
	// The first occurrence keeps the bare slug; later ones are suffixed from 1.
	for _, want := range []string{"conventions", "conventions-1", "conventions-2"} {
		if !got[want] {
			t.Errorf("anchor %q missing from %v", want, got)
		}
	}
	// The numbering starts at the SECOND occurrence: a "-0" would mean the first
	// heading was suffixed too, and a link to it would 404.
	if got["conventions-0"] {
		t.Errorf("anchor %q was minted; GitHub numbers duplicates from 1", "conventions-0")
	}
	if got["conventions-3"] {
		t.Errorf("anchor %q was minted for three headings", "conventions-3")
	}
}

// TestMarkdownFiles_CollectsTheRootFilesAndDocsTree: the function that decides
// which documents the gate owns had no test, so the gate could have stopped
// checking the root steering files — or stopped walking docs/ — and every run
// would still have printed "no rot".
func TestMarkdownFiles_CollectsTheRootFilesAndDocsTree(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "README.md"), "# readme\n")
	writeFile(t, filepath.Join(root, "AGENTS.md"), "# agents\n")
	writeFile(t, filepath.Join(root, "docs", "TESTING.md"), "# testing\n")
	writeFile(t, filepath.Join(root, "docs", "specs", "CLI-SPEC.md"), "# cli\n")
	// Not owned: a non-Markdown file, a Markdown file nested outside docs/, and
	// the copies a worktree under .claude/ holds of both.
	writeFile(t, filepath.Join(root, "go.work"), "go 1.26\n")
	writeFile(t, filepath.Join(root, "cmd", "notes.md"), "# not a doc\n")
	writeFile(t, filepath.Join(root, ".claude", "worktrees", "wt", "README.md"), "# copy\n")
	writeFile(t, filepath.Join(root, ".claude", "worktrees", "wt", "docs", "TESTING.md"), "# copy\n")

	got, err := markdownFiles(root)
	if err != nil {
		t.Fatalf("markdownFiles: %v", err)
	}

	want := []string{"AGENTS.md", "README.md", "docs/TESTING.md", "docs/specs/CLI-SPEC.md"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}
