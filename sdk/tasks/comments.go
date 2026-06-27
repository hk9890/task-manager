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

// comments.go — sidecar append/read/resolve primitives for the comment log.
//
// The comment sidecar is an append-only multi-document YAML stream stored at
// .tasks/comments/<id>.yml. Each YAML document represents one comment
// (original, edit revision, or tombstone). No prior document is ever rewritten.
//
// Rules (load-bearing):
//   - appendCommentDoc uses vfs.Append (O_APPEND + fsync) — never rewrite.
//   - All disk access goes through the vfs.FS seam; no os import here.
//   - resolveComments and sanitizeCommentBody are PURE: no FS, no os import.
//   - newCommentID is pure: random, no stream read.

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

const (
	// commentsDirName is the subdirectory holding all comment sidecars.
	commentsDirName = "comments"
	// commentFileExt is the extension for sidecar files.
	commentFileExt = ".yml"
	// docSeparator is the multi-document YAML stream separator, at column 0.
	docSeparator = "---\n"
)

// commentsPath returns the path to the sidecar file for issue id.
func (s *Store) commentsPath(id string) string {
	return filepath.Join(s.dir, commentsDirName, id+commentFileExt)
}

// commentOnDisk is the on-disk YAML shape for a single comment document.
// Field order is normative; omitempty keeps the output minimal.
type commentOnDisk struct {
	ID       string `yaml:"id"`
	Author   string `yaml:"author,omitempty"`
	Created  string `yaml:"created"` // RFC3339 UTC whole seconds
	Replaces string `yaml:"replaces,omitempty"`
	Deleted  bool   `yaml:"deleted,omitempty"`
	Body     string `yaml:"body,omitempty"`
}

// marshalCommentDoc encodes a single Comment into the bytes that will be
// appended to the sidecar stream (--- separator + YAML document). The body
// is forced into a YAML literal block scalar (body: |) so that no
// double-quoted, \n-escaped one-liner can appear in the sidecar.
func marshalCommentDoc(c Comment) []byte {
	created := formatTimestamp(c.Created)

	// Build a yaml.Node tree for every doc so all scalars (the ISO-8601 created
	// timestamp included) emit unquoted, and the body can force LiteralStyle.
	// Struct encoding would quote created as a yaml.v3 !!timestamp.
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addScalar := func(key, val string) {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Value: val},
		)
	}
	addScalar("id", c.ID)
	if c.Author != "" {
		addScalar("author", c.Author)
	}
	addScalar("created", created)
	if c.Replaces != "" {
		addScalar("replaces", c.Replaces)
	}
	if c.Deleted {
		// Tombstone: deleted: true, no body.
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "deleted"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"},
		)
	} else {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "body"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: c.Body, Style: yaml.LiteralStyle},
		)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		panic(fmt.Sprintf("marshalCommentDoc node encode: %v", err))
	}
	_ = enc.Close()

	// Prepend the document separator.
	out := make([]byte, 0, len(docSeparator)+buf.Len())
	out = append(out, []byte(docSeparator)...)
	out = append(out, buf.Bytes()...)
	return out
}

// appendCommentDoc serializes c and appends it to the sidecar at path using
// vfs.Append (O_APPEND + fsync). It ensures the comments directory exists.
// Must be called while holding the store lock.
func appendCommentDoc(fs vfs.FS, path string, c Comment) error {
	// Ensure the comments/ directory exists (lazy creation).
	dir := filepath.Dir(path)
	if err := fs.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("comments dir: %w", err)
	}
	data := marshalCommentDoc(c)
	return fs.Append(path, data, 0o644)
}

// readCommentStream reads the sidecar at path and returns all comment
// documents in append order (raw stream, no resolution). Returns an empty
// slice if the file does not exist (the sidecar is created lazily).
func readCommentStream(fs vfs.FS, path string) ([]Comment, error) {
	data, err := fs.ReadFile(path)
	if err != nil {
		if vfs.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read comment sidecar %s: %w", path, err)
	}
	return parseCommentStream(data)
}

// parseCommentStream decodes a raw multi-document YAML stream (the bytes of a
// sidecar file) and returns all comments in append order. This is a pure
// function used by both readCommentStream and tests.
func parseCommentStream(data []byte) ([]Comment, error) {
	// Split on "---\n" at column 0. The first element before the first "---"
	// is empty (sidecars always start with "---\n"); we skip empty segments.
	parts := splitDocStream(data)

	var out []Comment
	for _, part := range parts {
		if len(strings.TrimSpace(part)) == 0 {
			continue
		}
		var d commentOnDisk
		if err := yaml.Unmarshal([]byte(part), &d); err != nil {
			return nil, fmt.Errorf("parse comment document: %w", err)
		}
		// Parse the created timestamp.
		created, tsErr := parseTimestamp(d.Created)
		if tsErr != nil {
			return nil, fmt.Errorf("comment %s: bad created timestamp %q: %w", d.ID, d.Created, tsErr)
		}
		out = append(out, Comment{
			ID:       d.ID,
			Author:   d.Author,
			Created:  created,
			Replaces: d.Replaces,
			Deleted:  d.Deleted,
			Body:     d.Body,
		})
	}
	return out, nil
}

// splitDocStream splits a YAML multi-document stream on "---\n" at column 0
// (i.e. at the start of the data or immediately after a newline), returning
// the raw text of each document (without the separator line).
//
// A "---\n" that appears inside a block scalar is always indented (it is
// content under "body: |"), so it never appears at column 0 and will not be
// treated as a document boundary.
func splitDocStream(data []byte) []string {
	// Use the newline-prefixed separator "\n---\n" to match only at column 0.
	// Handle the first document specially: if the stream starts with "---\n"
	// we consume that prefix and proceed.
	newlineSep := []byte("\n---\n")
	startSep := []byte("---\n")

	// Strip a leading "---\n" if present (standard for our sidecars).
	data = bytes.TrimPrefix(data, startSep)

	var parts []string
	for {
		idx := bytes.Index(data, newlineSep)
		if idx < 0 {
			parts = append(parts, string(data))
			break
		}
		// The part is everything up to (but not including) the "\n---\n".
		// We include the "\n" at idx as the last byte of the segment.
		parts = append(parts, string(data[:idx+1]))
		data = data[idx+len(newlineSep):]
	}
	return parts
}

// parseTimestamp parses an RFC3339 UTC timestamp string of the form
// "2006-01-02T15:04:05Z". This is the inverse of the format used by
// marshalCommentDoc.
func parseTimestamp(s string) (time.Time, error) {
	return time.Parse(storageTimeLayout, s)
}

// resolveComments collapses a raw comment stream into the effective list:
// each replaces-chain is reduced to its newest document (later append wins
// on duplicate replaces); tombstones (Deleted: true) are omitted.
//
// Cycle safety: the sidecar is human-/git-merge-editable (TASK-STORAGE-SPEC
// §4.4), so a bad merge or hand-edit can introduce a replaces cycle (A→B→A
// or A→A). This function guards every chain-following loop with a visited-set
// bounded by len(stream). On detecting a revisit the current node is treated
// as its own chain root — the resolution degrades gracefully instead of
// hanging. No panic, no error: the caller receives a best-effort effective
// log.
//
// Dangling replaces: if a document's replaces target is absent from the
// stream (e.g. a sidecar truncated by a bad merge), the document is treated
// as its own root and survives as an independent comment. This is intentional
// fail-open behaviour.
//
// This is a PURE function: no I/O, no imports of os or vfs.
func resolveComments(stream []Comment) []Comment {
	if len(stream) == 0 {
		return nil
	}

	// replacedBy maps a comment ID to the ID of the document that replaces it.
	// When two documents replace the same ID, the later one (higher index) wins.
	replacedBy := make(map[string]string, len(stream))
	for _, c := range stream {
		if c.Replaces != "" {
			replacedBy[c.Replaces] = c.ID
		}
	}

	// Build a lookup by ID so we can follow chains.
	byID := make(map[string]Comment, len(stream))
	for _, c := range stream {
		byID[c.ID] = c
	}

	// For each document in stream order, determine if it is the current
	// effective version of its chain:
	//   - A document is a "root" if it doesn't replace anything (Replaces == "").
	//   - A document is "current" if nothing replaces it (not in replacedBy).
	//   - We only emit a document if it is current AND not a tombstone.
	//
	// To preserve original position order (the spec says position gives sequence
	// and callers expect append-order), we walk the stream in order and emit
	// the chain's winner at the position of the chain root.

	// Find the chain root for every document (the original document in the chain).
	chainRoot := make(map[string]string, len(stream)) // id → root id
	for _, c := range stream {
		if c.Replaces == "" {
			chainRoot[c.ID] = c.ID
		}
	}
	// Propagate roots through the chain.
	// Guard: use a per-walk visited set to break cycles. A cycle exists when we
	// revisit a node we have already stepped through on this walk. The visited
	// set is bounded by len(stream), so the loop terminates in O(n).
	for _, c := range stream {
		if c.Replaces != "" {
			// Follow the chain to find the root.
			root := c.Replaces
			visited := make(map[string]struct{}, len(stream))
			visited[c.ID] = struct{}{}
			for {
				if _, cycle := visited[root]; cycle {
					// Cycle detected: treat the current node as its own root
					// rather than looping forever.
					chainRoot[c.ID] = c.ID
					break
				}
				if r, ok := chainRoot[root]; ok {
					chainRoot[c.ID] = r
					break
				}
				// root not yet resolved; look for what replaces it.
				prev, ok2 := byID[root]
				if !ok2 || prev.Replaces == "" {
					// Dangling or terminal: treat root as the chain root.
					// Dangling (ok2 == false): target absent from stream —
					// fail-open, the current node becomes its own root.
					chainRoot[c.ID] = root
					break
				}
				visited[root] = struct{}{}
				root = prev.Replaces
			}
		}
	}

	// Find the winning (newest) document for each chain root.
	// Because we iterate stream in order, the last winner assignment wins.
	winner := make(map[string]string, len(stream)) // root id → winning id
	for _, c := range stream {
		root := chainRoot[c.ID]
		winner[root] = c.ID
	}

	// Emit in stream order, at the position of each chain root.
	// We only emit the winner for each root, and only if it is not a tombstone.
	seen := make(map[string]bool, len(stream))
	var out []Comment
	for _, c := range stream {
		root := chainRoot[c.ID]
		if seen[root] {
			continue
		}
		seen[root] = true

		winnerID := winner[root]
		w := byID[winnerID]
		if w.Deleted {
			continue // tombstone: omit
		}
		out = append(out, w)
	}
	return out
}

// sanitizeCommentBody normalizes a comment body for storage:
//   - converts CRLF and bare CR to LF
//   - strips trailing whitespace from every line
//
// Sanitization ensures YAML emits a block scalar (body: |) instead of a
// double-quoted one-liner. Trailing-space markdown hard breaks are not
// preserved (accepted trade-off per TASK-STORAGE-SPEC §4.4 rule 4).
//
// This is a PURE function: no I/O.
func sanitizeCommentBody(body string) string {
	// Normalize line endings: CRLF → LF, then bare CR → LF.
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")

	// Strip trailing whitespace from each line.
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

// newCommentID returns a random 8-character lowercase alphanumeric token
// matching ^[0-9a-z]{8}$. It does NOT read the stream; per-issue collisions
// are negligible given the ~36^8 keyspace.
func newCommentID() string {
	return randToken(8)
}

// validateCommentBody verifies that a comment body, after sanitization, will
// not serialize as a double-quoted YAML scalar (TASK-STORAGE-SPEC §10, §4.4 rule 4).
// Bodies containing control characters (NUL, ESC, etc.) force YAML to emit
// a double-quoted, \n-escaped scalar and are therefore rejected.
//
// This is a PURE function: no I/O, no os import.
func validateCommentBody(body string) error {
	for _, r := range body {
		// Control characters other than HT (\t) and LF (\n) force double-quoting.
		if r < 0x20 && r != '\t' && r != '\n' {
			return invalid("body", "body contains a control character (0x%02x) that would force a double-quoted YAML scalar", r)
		}
		// DEL character.
		if r == 0x7f {
			return invalid("body", "body contains DEL (0x7f) which would force a double-quoted YAML scalar")
		}
	}
	return nil
}

// validateCommentDoc verifies the per-document invariants for a comment that
// is about to be appended to the sidecar (TASK-STORAGE-SPEC §10):
//   - a comment must have a non-empty body OR Deleted:true (not neither)
//   - if the body is non-empty, it must not force a double-quoted scalar
//
// This is a PURE function.
func validateCommentDoc(c Comment) error {
	if !c.Deleted && strings.TrimSpace(c.Body) == "" {
		return invalid("body", "comment must have a non-empty body or deleted:true")
	}
	if !c.Deleted && c.Body != "" {
		if err := validateCommentBody(c.Body); err != nil {
			return err
		}
	}
	return nil
}

// validateReplaces checks that the given replaces ID (if non-empty) names an
// existing **earlier** comment in the stream (TASK-STORAGE-SPEC §4.4/§10).
//
// stream is the raw append-order stream at the point the new document is being
// added — i.e. the pre-append contents of the sidecar. Every document in
// stream is by definition earlier than the document about to be appended, so
// any ID found in stream satisfies the "earlier in the stream" requirement.
// An ID not found in stream is not earlier and is therefore rejected.
//
// This is a PURE function.
func validateReplaces(replacesID string, stream []Comment) error {
	if replacesID == "" {
		return nil
	}
	for _, c := range stream {
		if c.ID == replacesID {
			return nil
		}
	}
	return invalid("replaces", "comment %q does not exist as an earlier comment in the issue's comment stream", replacesID)
}
