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
	Comments    []Comment  `yaml:"comments,omitempty"`
}

// Marshal renders an issue to its on-disk file bytes: a YAML frontmatter block
// followed by the markdown description body.
func Marshal(iss *Issue) ([]byte, error) {
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
		Created:     iss.Created,
		Updated:     iss.Updated,
		CloseReason: iss.CloseReason,
		Comments:    iss.Comments,
	}
	if !iss.Closed.IsZero() {
		c := iss.Closed
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

// Unmarshal parses on-disk file bytes back into an Issue.
func Unmarshal(data []byte) (*Issue, error) {
	text := string(data)
	text = strings.TrimPrefix(text, "\uFEFF") // tolerate a UTF-8 BOM

	if !strings.HasPrefix(text, fence) {
		return nil, fmt.Errorf("missing frontmatter: file must start with %q", fence)
	}

	// Strip the opening fence line, then split on the closing fence.
	rest := strings.TrimPrefix(text, fence)
	rest = strings.TrimPrefix(rest, "\n")

	idx := strings.Index(rest, "\n"+fence)
	if idx < 0 {
		return nil, fmt.Errorf("unterminated frontmatter: no closing %q", fence)
	}
	yamlPart := rest[:idx]
	body := rest[idx+len("\n"+fence):]
	body = strings.TrimPrefix(body, "\n") // drop the newline after the closing fence

	var fm frontmatter
	if err := yaml.Unmarshal([]byte(yamlPart), &fm); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}

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
		Comments:    fm.Comments,
		Description: strings.TrimSpace(body),
	}
	if fm.Closed != nil {
		iss.Closed = *fm.Closed
	}
	return iss, nil
}
