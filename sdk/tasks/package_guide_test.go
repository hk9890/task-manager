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

// Guide fragments contributed by packages (HOOK-SPEC §3.7): the manifest half at
// L1, and the reading half at L2 on vfs.Mem.
package tasks

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// ── L1: the manifest half ────────────────────────────────────────────────────

func TestGuideFromManifest_ResolvesFileInsideThePackage(t *testing.T) {
	m := packageManifest{Guide: []GuideEntry{{ID: "bodies", File: "./guide/bodies.md"}}}
	got, err := guideFromManifest(m, "policy", "/pkgs/policy")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want one fragment, got %d", len(got))
	}
	if want := filepath.Join("/pkgs/policy", "guide", "bodies.md"); got[0].path != want {
		t.Errorf("path = %q, want %q", got[0].path, want)
	}
	if want := "pkg:policy:bodies"; got[0].id != want {
		t.Errorf("id = %q, want %q", got[0].id, want)
	}
}

// The id rules are the hook id's, for the same reason: the effective topic is
// what a caller names, so it must keep meaning the same fragment.
func TestGuideFromManifest_RejectsUnusableIDs(t *testing.T) {
	cases := []struct {
		name  string
		entry GuideEntry
		want  string
	}{
		{"empty", GuideEntry{File: "g.md"}, "id is required"},
		{"colon", GuideEntry{ID: "a:b", File: "g.md"}, "must not contain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := guideFromManifest(packageManifest{Guide: []GuideEntry{tc.entry}}, "policy", "/p")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// `into` names one built-in topic, so a ':' in it is the caller confusing it with
// an effective topic id — caught here, where the manifest is written.
func TestGuideFromManifest_RejectsAnIntoThatLooksLikeAnEffectiveID(t *testing.T) {
	m := packageManifest{Guide: []GuideEntry{{ID: "rules", Into: "pkg:policy:filing", File: "g.md"}}}
	_, err := guideFromManifest(m, "policy", "/p")
	if err == nil || !strings.Contains(err.Error(), "must not contain") {
		t.Fatalf("err = %v, want one containing %q", err, "must not contain")
	}
}

// Whether the named topic exists is not this layer's question, and must not be:
// the manifest is parsed on the write path, so a package naming a topic the
// binary has since renamed would become broken — and a broken package runs no
// hooks, turning a documentation mismatch into refused writes.
func TestGuideFromManifest_CarriesAnUnknownIntoWithoutJudgingIt(t *testing.T) {
	m := packageManifest{Guide: []GuideEntry{{ID: "rules", Into: "nosuchtopic", File: "g.md"}}}
	got, err := guideFromManifest(m, "policy", "/p")
	if err != nil {
		t.Fatalf("an unresolvable into must not fail the manifest: %v", err)
	}
	if len(got) != 1 || got[0].into != "nosuchtopic" {
		t.Fatalf("into must be carried verbatim, got %+v", got)
	}
}

// The `overview:` key declares the fragment that reaches the guide's overview,
// so it comes first and is marked — the two things that separate it from a
// section a caller has to ask for.
func TestGuideFromManifest_OverviewComesFirstAndIsMarked(t *testing.T) {
	m := packageManifest{
		Overview: "./guide/overview.md",
		Guide:    []GuideEntry{{ID: "bodies", File: "./guide/bodies.md"}},
	}
	got, err := guideFromManifest(m, "policy", "/p")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want the overview and the section, got %+v", got)
	}
	if !got[0].overview || got[0].id != "pkg:policy:overview" {
		t.Errorf("the overview fragment must come first and be marked: %+v", got[0])
	}
	if got[1].overview || got[1].id != "pkg:policy:bodies" {
		t.Errorf("a guide entry is not an overview fragment: %+v", got[1])
	}
}

// One id space, one meaning: "pkg:<package>:overview" always names the fragment
// that reaches the overview, so a section may not claim it.
func TestGuideFromManifest_RejectsAGuideEntryClaimingTheOverviewID(t *testing.T) {
	m := packageManifest{Guide: []GuideEntry{{ID: GuideOverviewID, File: "g.md"}}}
	_, err := guideFromManifest(m, "policy", "/p")
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("err = %v, want the reserved-id error", err)
	}
}

func TestGuideFromManifest_RejectsADuplicateID(t *testing.T) {
	m := packageManifest{Guide: []GuideEntry{
		{ID: "bodies", File: "a.md"},
		{ID: "bodies", File: "b.md"},
	}}
	_, err := guideFromManifest(m, "policy", "/p")
	if err == nil || !strings.Contains(err.Error(), "declared twice") {
		t.Fatalf("err = %v, want a duplicate-id error", err)
	}
}

// A fragment has no PATH-lookup meaning and no reason to name anything outside
// the package: an absolute path is machine-specific, which is the one thing the
// package format exists to avoid.
func TestGuideFromManifest_RejectsAPathOutsideThePackage(t *testing.T) {
	cases := []struct {
		name string
		file string
	}{
		{"absolute", "/etc/motd"},
		{"climbing", "../../secrets.md"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := packageManifest{Guide: []GuideEntry{{ID: "g", File: tc.file}}}
			if _, err := guideFromManifest(m, "policy", "/p"); err == nil {
				t.Fatalf("file %q must be refused", tc.file)
			}
		})
	}
}

// A manifest whose guide entries are unusable is a broken package, exactly as a
// bad hook entry is: both are the manifest failing to describe itself.
func TestLoadPackage_ABadGuideEntryBreaksThePackage(t *testing.T) {
	fs := vfs.NewMem()
	dir := "/pkgs/policy"
	if err := fs.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(packageManifest{Version: 1, Guide: []GuideEntry{{ID: "", File: "g.md"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteAtomic(filepath.Join(dir, PackageManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPackage(fs, dir, "policy"); err == nil {
		t.Fatal("a guide entry with no id must make the package unusable")
	}
}

// ── L2: the reading half, on Mem ─────────────────────────────────────────────

// writeGuidePackage writes a package that contributes one guide section, and
// returns the package directory. A non-empty overview also declares and writes
// the `overview:` fragment.
func writeGuidePackage(t *testing.T, fs vfs.FS, dir, name, fragment string, overview ...string) string {
	t.Helper()
	pkgDir := filepath.Join(dir, packagesSubdir, name)
	if err := fs.MkdirAll(filepath.Join(pkgDir, "guide"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := packageManifest{Version: 1, Guide: []GuideEntry{{ID: "bodies", File: "./guide/bodies.md"}}}
	if len(overview) > 0 {
		m.Overview = "./guide/overview.md"
		if err := fs.WriteAtomic(filepath.Join(pkgDir, "guide", "overview.md"), []byte(overview[0]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteAtomic(filepath.Join(pkgDir, PackageManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if fragment != "" {
		if err := fs.WriteAtomic(filepath.Join(pkgDir, "guide", "bodies.md"), []byte(fragment), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return pkgDir
}

func TestGuideTopics_ReadsTheFragmentAndTagsItsScope(t *testing.T) {
	s, fs := chainStore(t)
	writeGuidePackage(t, fs, s.dir, "policy", "every bug names its repro.\n")
	if err := s.SetConfig(Config{Prefix: "tst", Use: []PackageRef{{Path: "packages/policy"}}}); err != nil {
		t.Fatal(err)
	}

	topics, err := s.GuideTopics()
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 1 {
		t.Fatalf("want one topic, got %+v", topics)
	}
	got := topics[0]
	if got.ID != "pkg:policy:bodies" {
		t.Errorf("id = %q", got.ID)
	}
	if got.Scope != scopeStore {
		t.Errorf("scope = %q, want %q", got.Scope, scopeStore)
	}
	if !strings.Contains(got.Text, "names its repro") {
		t.Errorf("text = %q", got.Text)
	}
	if got.Detail != "" {
		t.Errorf("a readable fragment must carry no detail, got %q", got.Detail)
	}
}

// A guide is not a gate. A fragment whose file is gone is reported in the topic
// and never returned as an error: the caller asked what it could learn, and
// answering "nothing" because one document is missing teaches it less.
func TestGuideTopics_AMissingFragmentIsReportedNotFailed(t *testing.T) {
	s, fs := chainStore(t)
	writeGuidePackage(t, fs, s.dir, "policy", "") // manifest names a file that is not there
	if err := s.SetConfig(Config{Prefix: "tst", Use: []PackageRef{{Path: "packages/policy"}}}); err != nil {
		t.Fatal(err)
	}

	topics, err := s.GuideTopics()
	if err != nil {
		t.Fatalf("a missing fragment must not fail the call: %v", err)
	}
	if len(topics) != 1 {
		t.Fatalf("the topic must still be listed, got %+v", topics)
	}
	if topics[0].Detail == "" {
		t.Error("a fragment that could not be read must say why")
	}
	if topics[0].Text != "" {
		t.Errorf("text must be empty when detail is set, got %q", topics[0].Text)
	}
}

// A package that will not load at all must not stop the guide either — the
// broken entry is `package list`'s to report.
func TestGuideTopics_ABrokenPackageDoesNotFailTheGuide(t *testing.T) {
	s, _ := chainStore(t)
	if err := s.SetConfig(Config{Prefix: "tst", Use: []PackageRef{{Path: "packages/absent"}}}); err != nil {
		t.Fatal(err)
	}
	topics, err := s.GuideTopics()
	if err != nil {
		t.Fatalf("an uninstalled package must not fail the guide: %v", err)
	}
	if len(topics) != 0 {
		t.Errorf("nothing loaded, so no topics: %+v", topics)
	}
}

// The text is written verbatim into a caller's instructions, so one package must
// not be able to spend the whole context. The cut falls on a line boundary.
func TestGuideTopics_CapsAnOversizedFragmentAtALineBoundary(t *testing.T) {
	s, fs := chainStore(t)
	line := strings.Repeat("x", 79) + "\n"
	big := strings.Repeat(line, (MaxGuideFragmentBytes/len(line))+20)
	writeGuidePackage(t, fs, s.dir, "policy", big)
	if err := s.SetConfig(Config{Prefix: "tst", Use: []PackageRef{{Path: "packages/policy"}}}); err != nil {
		t.Fatal(err)
	}

	topics, err := s.GuideTopics()
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 1 {
		t.Fatalf("want one topic, got %d", len(topics))
	}
	got := topics[0]
	if !got.Truncated {
		t.Error("an oversized fragment must be marked truncated")
	}
	if len(got.Text) > MaxGuideFragmentBytes {
		t.Errorf("text is %d bytes, over the %d cap", len(got.Text), MaxGuideFragmentBytes)
	}
	if !strings.HasSuffix(got.Text, "\n") {
		t.Error("the cut must fall on a line boundary")
	}
}

// TestGuideTopics_AFragmentExactlyAtTheCapIsNotTruncated pins the comparison at
// its exact value. The tests either side of it use fragments comfortably under
// and over the cap, so the boundary itself is free to drift: one step tighter
// and a fragment sitting exactly on MaxGuideFragmentBytes loses its final line
// and is reported as Truncated to every `taskmgr guide` caller, although nothing
// exceeded the cap.
func TestGuideTopics_AFragmentExactlyAtTheCapIsNotTruncated(t *testing.T) {
	s, fs := chainStore(t)
	// Whole lines summing to exactly MaxGuideFragmentBytes: 8192 = 128 × 64.
	line := strings.Repeat("z", 63) + "\n"
	exact := strings.Repeat(line, MaxGuideFragmentBytes/len(line))
	if len(exact) != MaxGuideFragmentBytes {
		t.Fatalf("test setup: fragment is %d bytes, want exactly %d", len(exact), MaxGuideFragmentBytes)
	}

	writeGuidePackage(t, fs, s.dir, "policy", exact)
	if err := s.SetConfig(Config{Prefix: "tst", Use: []PackageRef{{Path: "packages/policy"}}}); err != nil {
		t.Fatal(err)
	}

	topics, err := s.GuideTopics()
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 1 {
		t.Fatalf("want one topic, got %d", len(topics))
	}
	if topics[0].Truncated {
		t.Error("a fragment of exactly MaxGuideFragmentBytes was reported as truncated")
	}
	if topics[0].Text != exact {
		t.Errorf("text is %d bytes, want the whole %d — a fragment at the cap lost content",
			len(topics[0].Text), len(exact))
	}
}

// One byte over the cap is the first fragment that must be cut. Together with
// the case above this pins the comparison from both sides.
func TestGuideTopics_AFragmentOneByteOverTheCapIsTruncated(t *testing.T) {
	s, fs := chainStore(t)
	line := strings.Repeat("z", 63) + "\n"
	over := strings.Repeat(line, MaxGuideFragmentBytes/len(line)) + "x"
	if len(over) != MaxGuideFragmentBytes+1 {
		t.Fatalf("test setup: fragment is %d bytes, want %d", len(over), MaxGuideFragmentBytes+1)
	}

	writeGuidePackage(t, fs, s.dir, "policy", over)
	if err := s.SetConfig(Config{Prefix: "tst", Use: []PackageRef{{Path: "packages/policy"}}}); err != nil {
		t.Fatal(err)
	}

	topics, err := s.GuideTopics()
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 1 {
		t.Fatalf("want one topic, got %d", len(topics))
	}
	if !topics[0].Truncated {
		t.Error("a fragment one byte over MaxGuideFragmentBytes was not truncated")
	}
}

// An overview fragment lands in every caller's context rather than only in the
// ones that asked for the subject, so it is held to a cap an order of magnitude
// tighter than a section's.
func TestGuideTopics_AnOverviewFragmentTakesTheTighterCap(t *testing.T) {
	s, fs := chainStore(t)
	line := strings.Repeat("y", 79) + "\n"
	big := strings.Repeat(line, (MaxGuideFragmentBytes/len(line))+20) // over both caps
	writeGuidePackage(t, fs, s.dir, "policy", "section\n", big)
	if err := s.SetConfig(Config{Prefix: "tst", Use: []PackageRef{{Path: "packages/policy"}}}); err != nil {
		t.Fatal(err)
	}

	topics, err := s.GuideTopics()
	if err != nil {
		t.Fatal(err)
	}
	var overview *GuideTopic
	for i := range topics {
		if topics[i].Overview {
			overview = &topics[i]
		}
	}
	if overview == nil {
		t.Fatalf("the overview fragment must be reported: %+v", topics)
	}
	if !overview.Truncated {
		t.Error("an oversized overview fragment must be marked truncated")
	}
	if len(overview.Text) > MaxGuideOverviewBytes {
		t.Errorf("overview text is %d bytes, over the %d cap", len(overview.Text), MaxGuideOverviewBytes)
	}
}

// The per-user config's packages come first, as they do for hooks: one order for
// both halves of a package, so prose and gate are read the same way round.
func TestGuideTopics_GlobalPackagesComeFirst(t *testing.T) {
	s, fs := chainStore(t)
	writeGuidePackage(t, fs, "/hm", "machine", "machine-wide\n")
	writeGuidePackage(t, fs, s.dir, "project", "project\n")
	if err := saveGlobalConfig(fs, "/hm", GlobalConfig{Version: 1, Use: []PackageRef{{Name: "machine"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConfig(Config{Prefix: "tst", Use: []PackageRef{{Path: "packages/project"}}}); err != nil {
		t.Fatal(err)
	}

	topics, err := s.GuideTopics()
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 2 {
		t.Fatalf("want both fragments, got %+v", topics)
	}
	if topics[0].ID != "pkg:machine:bodies" || topics[0].Scope != scopeGlobal {
		t.Errorf("first topic = %+v, want the machine-wide one", topics[0])
	}
	if topics[1].ID != "pkg:project:bodies" || topics[1].Scope != scopeStore {
		t.Errorf("second topic = %+v, want the store's", topics[1])
	}
}

// A window with no line break in it — one paragraph, the ordinary shape under
// the 1 KiB overview cap — has no seam to cut on. The byte offset then lands
// mid-sequence for any multi-byte character, and the fragment reached the caller
// as invalid UTF-8: this text is pasted into an agent's context and wrapped as
// JSON, where a broken character is not cosmetic.
func TestReadGuideFragment_CutsOnAWholeRuneWhenThereIsNoLineBreak(t *testing.T) {
	fs := vfs.NewMem()
	if err := fs.MkdirAll("/frag", 0o755); err != nil {
		t.Fatal(err)
	}
	limit := 1024
	// An em dash straddling the cap: three bytes, the last two past the limit.
	body := strings.Repeat("a", limit-1) + "—" + strings.Repeat("b", 40)
	if err := fs.WriteAtomic("/frag/overview.md", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	text, truncated, err := readGuideFragment(fs, "/frag/overview.md", limit)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("an oversized fragment must be marked truncated")
	}
	if !utf8.ValidString(text) {
		t.Errorf("fragment is not valid UTF-8: %q", text[len(text)-8:])
	}
	if len(text) != limit-1 {
		t.Errorf("text is %d bytes, want the partial character dropped and nothing else", len(text))
	}
}

// The guard skipped a line break sitting at index 0, so a fragment opening with
// a blank line fell through to the rune path and lost every line it had.
func TestReadGuideFragment_CutsAtALineBreakAtTheStart(t *testing.T) {
	fs := vfs.NewMem()
	if err := fs.MkdirAll("/frag", 0o755); err != nil {
		t.Fatal(err)
	}
	body := "\n" + strings.Repeat("a", 200)
	if err := fs.WriteAtomic("/frag/overview.md", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	text, truncated, err := readGuideFragment(fs, "/frag/overview.md", 100)
	if err != nil || !truncated {
		t.Fatalf("readGuideFragment = (%q, %v, %v)", text, truncated, err)
	}
	if text != "\n" {
		t.Errorf("text = %q, want the cut to fall on the one line break there is", text)
	}
}
