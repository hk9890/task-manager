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
	Labels      []string   `yaml:"labels,omitempty"`
	Parent      string     `yaml:"parent,omitempty"`
	BlockedBy   []string   `yaml:"blocked_by,omitempty"`
	Related     []string   `yaml:"related,omitempty"`
	Created     time.Time  `yaml:"created"`
	Updated     time.Time  `yaml:"updated"`
	Closed      *time.Time `yaml:"closed,omitempty"`
	CloseReason string     `yaml:"close_reason,omitempty"`
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
		ID:          iss.ID,
		Title:       iss.Title,
		Status:      iss.Status,
		Type:        iss.Type,
		Priority:    iss.Priority,
		Assignee:    iss.Assignee,
		Labels:      iss.Labels,
		Parent:      iss.Parent,
		BlockedBy:   iss.BlockedBy,
		Related:     iss.Related,
		Created:     created,
		Updated:     updated,
		CloseReason: iss.CloseReason,
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
		ID:          fm.ID,
		Title:       fm.Title,
		Status:      fm.Status,
		Type:        fm.Type,
		Priority:    fm.Priority,
		Assignee:    fm.Assignee,
		Labels:      fm.Labels,
		Parent:      fm.Parent,
		BlockedBy:   fm.BlockedBy,
		Related:     fm.Related,
		Created:     fm.Created,
		Updated:     fm.Updated,
		CloseReason: fm.CloseReason,
		Description: strings.TrimSpace(body),
	}
	if fm.Closed != nil {
		iss.Closed = *fm.Closed
	}
	return iss, lfm.Comments, nil
}
