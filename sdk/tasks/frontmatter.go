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
	"bytes"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// fence is the delimiter that brackets the YAML frontmatter block.
const fence = "---"

// frontmatter is the on-disk YAML shape. It is kept separate from Issue so we
// control field order, omitempty behaviour, and the fact that Description is
// stored as the markdown body rather than a YAML field.
//
// No comments in frontmatter — comments live in the sidecar (TASK-STORAGE-SPEC §4.3/§4.4).
type frontmatter struct {
	ID          string     `yaml:"id"`
	Title       string     `yaml:"title"`
	Status      Status     `yaml:"status"`
	Type        Type       `yaml:"type"`
	Priority    int        `yaml:"priority"`
	Assignee    string     `yaml:"assignee,omitempty"`
	Creator     string     `yaml:"creator,omitempty"`
	Labels      []string   `yaml:"labels,omitempty"`
	Parent      string     `yaml:"parent,omitempty"`
	BlockedBy   []string   `yaml:"blocked_by,omitempty"`
	Related     []string   `yaml:"related,omitempty"`
	Created     time.Time  `yaml:"created"`
	Updated     time.Time  `yaml:"updated"`
	Closed      *time.Time `yaml:"closed,omitempty"`
	CloseReason string     `yaml:"close_reason,omitempty"`

	// BodyExternal, when true, means the body is NOT in this file — it lives in
	// the content sidecar at content/<id> (TASK-STORAGE-SPEC §4.6). It is emitted
	// last so the field order of every ordinary issue file is unchanged.
	//
	// This flag, not the mere presence of a sidecar file, is what makes the
	// sidecar authoritative. That is deliberate: writes touch two files and are
	// not a single transaction, so a crash can leave a sidecar behind. With the
	// flag, such a leftover is inert garbage; without it, the leftover would
	// silently override the .md and resurrect an older body.
	BodyExternal bool `yaml:"body_external,omitempty"`
}

// legacyFrontmatter extends frontmatter to read (but not write) the old inline
// comments field, used during migration of pre-sidecar issue files.
type legacyFrontmatter struct {
	frontmatter `yaml:",inline"`
	Comments    []legacyComment `yaml:"comments,omitempty"`
}

// legacyComment is the old inline comment shape stored in frontmatter.
type legacyComment struct {
	Author  string `yaml:"author,omitempty"`
	Created string `yaml:"created"`
	Body    string `yaml:"body,omitempty"`
}

// Marshal renders an issue to its on-disk file bytes: a YAML frontmatter block
// followed by the markdown description body. Comments are NOT written to the
// frontmatter; they live in the sidecar (TASK-STORAGE-SPEC §4.3/§4.4).
//
// Timestamps (Created, Updated, Closed) are truncated to whole seconds in UTC
// before serialization, as required by TASK-STORAGE-SPEC §6.
func Marshal(iss *Issue) ([]byte, error) {
	// Truncate timestamps to whole seconds in UTC (TASK-STORAGE-SPEC §6).
	// This prevents sub-second noise or non-UTC offsets from appearing in
	// the on-disk representation when SDK callers build Issues from time.Now().
	created := iss.Created.UTC().Truncate(time.Second)
	updated := iss.Updated.UTC().Truncate(time.Second)

	fm := frontmatter{
		ID:           iss.ID,
		Title:        iss.Title,
		Status:       iss.Status,
		Type:         iss.Type,
		Priority:     iss.Priority,
		Assignee:     iss.Assignee,
		Creator:      iss.Creator,
		Labels:       iss.Labels,
		Parent:       iss.Parent,
		BlockedBy:    iss.BlockedBy,
		Related:      iss.Related,
		Created:      created,
		Updated:      updated,
		CloseReason:  iss.CloseReason,
		BodyExternal: iss.bodyExternal,
	}
	if !iss.Closed.IsZero() {
		c := iss.Closed.UTC().Truncate(time.Second)
		fm.Closed = &c
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		return nil, fmt.Errorf("encode frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close encoder: %w", err)
	}

	var out bytes.Buffer
	out.WriteString(fence + "\n")
	out.Write(buf.Bytes())
	out.WriteString(fence + "\n")

	body := strings.TrimSpace(iss.Description)
	if body != "" {
		out.WriteString("\n")
		out.WriteString(body)
		out.WriteString("\n")
	}
	return out.Bytes(), nil
}

// Unmarshal parses on-disk file bytes back into an Issue. Any legacy inline
// comments in the frontmatter are silently ignored (use unmarshalWithLegacy
// to retrieve them for migration).
func Unmarshal(data []byte) (*Issue, error) {
	iss, _, err := unmarshalWithLegacy(data)
	return iss, err
}

// unmarshalWithLegacy parses on-disk file bytes into an Issue and also returns
// any legacy inline comments that were embedded in the frontmatter. The second
// return value is non-nil only for files that predate the sidecar migration.
// After migration, the comments field is absent and legacyComments is nil.
func unmarshalWithLegacy(data []byte) (*Issue, []legacyComment, error) {
	text := string(data)
	text = strings.TrimPrefix(text, "\uFEFF") // tolerate a UTF-8 BOM

	if !strings.HasPrefix(text, fence) {
		return nil, nil, fmt.Errorf("missing frontmatter: file must start with %q", fence)
	}

	// Strip the opening fence line, then split on the closing fence.
	rest := strings.TrimPrefix(text, fence)
	rest = strings.TrimPrefix(rest, "\n")

	idx := strings.Index(rest, "\n"+fence)
	if idx < 0 {
		return nil, nil, fmt.Errorf("unterminated frontmatter: no closing %q", fence)
	}
	yamlPart := rest[:idx]
	body := rest[idx+len("\n"+fence):]
	body = strings.TrimPrefix(body, "\n") // drop the newline after the closing fence

	// Use legacyFrontmatter to capture old inline comments if present.
	var lfm legacyFrontmatter
	if err := yaml.Unmarshal([]byte(yamlPart), &lfm); err != nil {
		return nil, nil, fmt.Errorf("parse frontmatter: %w", err)
	}

	fm := lfm.frontmatter
	iss := &Issue{
		ID:           fm.ID,
		Title:        fm.Title,
		Status:       fm.Status,
		Type:         fm.Type,
		Priority:     fm.Priority,
		Assignee:     fm.Assignee,
		Creator:      fm.Creator,
		Labels:       fm.Labels,
		Parent:       fm.Parent,
		BlockedBy:    fm.BlockedBy,
		Related:      fm.Related,
		Created:      fm.Created,
		Updated:      fm.Updated,
		CloseReason:  fm.CloseReason,
		Description:  strings.TrimSpace(body),
		bodyExternal: fm.BodyExternal,
	}
	if fm.Closed != nil {
		iss.Closed = *fm.Closed
	}
	// The ID is checked here, at the parse boundary, because it is the one
	// frontmatter field the store turns into a filesystem path: the .md, the
	// comment sidecar and the content sidecar are all <dir>/<id>. A file whose
	// frontmatter carries "../../etc/passwd" is reachable by anyone who can write
	// into .tasks/ — a hand edit, or a pull request touching the tracked store —
	// and every read and write keyed on that ID would escape the store. Rejecting
	// it on the way in keeps the grammar's no-separator guarantee meaningful
	// (validIssueID, TASK-STORAGE-SPEC §3).
	if !validIssueID(iss.ID) {
		return nil, nil, fmt.Errorf("invalid issue id %q: must match the issue-ID grammar (TASK-STORAGE-SPEC §3)", iss.ID)
	}
	return iss, lfm.Comments, nil
}
