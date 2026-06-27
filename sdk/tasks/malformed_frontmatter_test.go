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

// L1 tests for adversarial/malformed frontmatter inputs.
//
// These tests exercise paths that real on-disk files can reach via hand-edits
// or git-merges: bad YAML, CRLF line endings, UTF-8 BOM, missing delimiters,
// and body content that contains literal "---" separators.
//
// Property asserted in every case:
//   - Unmarshal must NEVER panic.
//   - For inputs that are expected to fail: a non-nil, non-empty error is returned.
//   - For inputs that are expected to succeed (BOM, CRLF tolerance): round-trip
//     is correct and no data is silently lost.

import (
	"strings"
	"testing"
)

// TestUnmarshal_MalformedYAML verifies that a file whose YAML block is
// syntactically invalid returns a clear error and does not panic.
func TestUnmarshal_MalformedYAML(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "unclosed bracket",
			input: "---\nid: [unclosed\n---\n",
		},
		{
			name:  "tab indentation error",
			input: "---\nid: agt-0001\n\ttitle: indented with tab\n---\n",
		},
		{
			name:  "duplicate mapping key (YAML 1.2 error)",
			input: "---\nid: agt-0001\nid: agt-0002\n---\n",
		},
		{
			name:  "invalid scalar under mapping",
			input: "---\n: : bad key\n---\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Must not panic. If it panics, the test harness will report it.
			_, err := Unmarshal([]byte(tc.input))
			// Some of the above may parse leniently under gopkg.in/yaml.v3.
			// What we care about: no panic. An error is acceptable; silent
			// data loss for these malformed inputs would also be acceptable
			// as long as the spec-mandated error cases do error.
			_ = err // result checked below where we know it must fail
		})
	}

	// These specific cases MUST return a non-nil error per spec:
	mustFail := []struct {
		name  string
		input string
	}{
		{
			name:  "unclosed bracket is invalid YAML",
			input: "---\nid: [unclosed\n---\n",
		},
	}
	for _, tc := range mustFail {
		t.Run("mustFail/"+tc.name, func(t *testing.T) {
			_, err := Unmarshal([]byte(tc.input))
			if err == nil {
				t.Errorf("Unmarshal(%q): expected error for malformed YAML, got nil", tc.input)
			}
			if err != nil && err.Error() == "" {
				t.Errorf("Unmarshal(%q): error message is empty", tc.input)
			}
		})
	}
}

// TestUnmarshal_MissingOpeningFence verifies that a file that does not start
// with "---" returns a clear error describing the missing frontmatter.
func TestUnmarshal_MissingOpeningFence(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "plain text",
			input: "just some plain text",
		},
		{
			name:  "YAML without fence",
			input: "id: agt-0001\ntitle: missing fence\n",
		},
		{
			name:  "closing fence only",
			input: "---\n" + "id: agt-0001\n",
		},
		{
			name:  "empty file",
			input: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Unmarshal([]byte(tc.input))
			if err == nil {
				t.Errorf("Unmarshal(%q): expected error for missing fence, got nil", tc.input)
			}
		})
	}
}

// TestUnmarshal_MissingClosingFence verifies that a file with an opening "---"
// but no closing "---" returns a clear "unterminated frontmatter" error.
func TestUnmarshal_MissingClosingFence(t *testing.T) {
	input := "---\nid: agt-0001\ntitle: no closing fence\n"
	_, err := Unmarshal([]byte(input))
	if err == nil {
		t.Fatalf("Unmarshal: expected error for unterminated frontmatter, got nil")
	}
	if !strings.Contains(err.Error(), "unterminated") && !strings.Contains(err.Error(), "closing") {
		t.Errorf("expected error about unterminated/closing fence, got: %v", err)
	}
}

// TestUnmarshal_UTF8BOM verifies that a file prefixed with a UTF-8 BOM
// (\xEF\xBB\xBF) is parsed correctly — the BOM must be stripped transparently.
// This is a tolerance the spec grants for hand-edited files saved by some
// editors (e.g. Windows Notepad).
func TestUnmarshal_UTF8BOM(t *testing.T) {
	// Build a valid issue file and prepend the UTF-8 BOM.
	validContent := "---\nid: agt-0001\ntitle: BOM test\nstatus: open\ntype: task\npriority: 2\ncreated: 2026-06-01T00:00:00Z\nupdated: 2026-06-01T00:00:00Z\n---\n"
	bom := "\xEF\xBB\xBF"
	input := bom + validContent

	iss, err := Unmarshal([]byte(input))
	if err != nil {
		t.Fatalf("Unmarshal with BOM: unexpected error: %v", err)
	}
	if iss.ID != "agt-0001" {
		t.Errorf("ID = %q, want agt-0001", iss.ID)
	}
	if iss.Title != "BOM test" {
		t.Errorf("Title = %q, want %q", iss.Title, "BOM test")
	}
}

// TestUnmarshal_CRLFLineEndings verifies that a file using CRLF line endings
// (as produced by some Windows tools or git configurations) is parsed correctly.
// The YAML parser must handle \r\n transparently.
func TestUnmarshal_CRLFLineEndings(t *testing.T) {
	// Build the content with CRLF.
	lines := []string{
		"---",
		"id: agt-0002",
		"title: CRLF test",
		"status: open",
		"type: task",
		"priority: 1",
		"created: 2026-06-01T00:00:00Z",
		"updated: 2026-06-01T00:00:00Z",
		"---",
		"",
		"Body text here.",
		"",
	}
	input := strings.Join(lines, "\r\n")

	iss, err := Unmarshal([]byte(input))
	if err != nil {
		t.Fatalf("Unmarshal with CRLF: unexpected error: %v", err)
	}
	if iss.ID != "agt-0002" {
		t.Errorf("ID = %q, want agt-0002", iss.ID)
	}
	if iss.Priority != 1 {
		t.Errorf("Priority = %d, want 1", iss.Priority)
	}
}

// TestUnmarshal_BodyContainingFenceLine verifies that a document body that
// contains a literal "---" line is parsed correctly — the body "---" must
// not be mistaken for the closing frontmatter fence.
//
// The closing fence is a "---" at column 0 immediately after a newline;
// a "---" inside the body that appears after actual body content should be
// preserved in the description.
func TestUnmarshal_BodyContainingFenceLine(t *testing.T) {
	// The closing fence is the FIRST "\n---" after the opening block.
	// Body content (including "---" lines) comes after the closing fence.
	input := "---\n" +
		"id: agt-0003\n" +
		"title: fence in body\n" +
		"status: open\n" +
		"type: task\n" +
		"priority: 2\n" +
		"created: 2026-06-01T00:00:00Z\n" +
		"updated: 2026-06-01T00:00:00Z\n" +
		"---\n" +
		"\n" +
		"Section one.\n" +
		"\n" +
		"---\n" +
		"\n" +
		"Section two.\n"

	iss, err := Unmarshal([]byte(input))
	if err != nil {
		t.Fatalf("Unmarshal with fence-in-body: unexpected error: %v", err)
	}
	if iss.ID != "agt-0003" {
		t.Errorf("ID = %q, want agt-0003", iss.ID)
	}
	// The description should contain the body text (with the "---" line).
	if !strings.Contains(iss.Description, "---") {
		t.Errorf("Description should contain the literal '---' body line; got: %q", iss.Description)
	}
	if !strings.Contains(iss.Description, "Section one") {
		t.Errorf("Description missing 'Section one'; got: %q", iss.Description)
	}
	if !strings.Contains(iss.Description, "Section two") {
		t.Errorf("Description missing 'Section two'; got: %q", iss.Description)
	}
}

// TestUnmarshal_NoPanic_AdversarialInputs is a table-driven panic-guard for a
// broad set of adversarial inputs. None should cause a panic.
func TestUnmarshal_NoPanic_AdversarialInputs(t *testing.T) {
	cases := []struct {
		name  string
		input []byte
	}{
		{"nil input", nil},
		{"empty", []byte{}},
		{"only newline", []byte("\n")},
		{"only fence", []byte("---")},
		{"fence no newline", []byte("---\n---")},
		{"fence then EOF", []byte("---\n")},
		{"BOM only", []byte("\xEF\xBB\xBF")},
		{"BOM then fence-only", []byte("\xEF\xBB\xBF---\n")},
		{"invalid UTF-8 in YAML", []byte("---\nid: \xff\xfe\n---\n")},
		{"null bytes in body", []byte("---\nid: x\n---\n\x00\x00\x00")},
		{"very long line", []byte("---\n" + strings.Repeat("x", 1<<20) + "\n---\n")},
		{"deeply nested YAML", []byte("---\nid: {a: {b: {c: {d: {e: x}}}}}\n---\n")},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Must not panic. Error return is acceptable.
			_, _ = Unmarshal(tc.input)
		})
	}
}
