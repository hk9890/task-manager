package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hk9890/agent-tasks/sdk/tasks"
)

// printJSON writes v as indented JSON to stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// --- JSON DTOs: stable, snake_case shapes for agents ---

type issueDTO struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	Type        string     `json:"type"`
	Priority    int        `json:"priority"`
	Assignee    string     `json:"assignee,omitempty"`
	Labels      []string   `json:"labels,omitempty"`
	Parent      string     `json:"parent,omitempty"`
	BlockedBy   []string   `json:"blocked_by,omitempty"`
	Related     []string   `json:"related,omitempty"`
	Created     time.Time  `json:"created"`
	Updated     time.Time  `json:"updated"`
	Closed      *time.Time `json:"closed,omitempty"`
	CloseReason string     `json:"close_reason,omitempty"`
}

type refDTO struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	Priority int    `json:"priority"`
}

type commentDTO struct {
	ID       string    `json:"id"`
	Author   string    `json:"author,omitempty"`
	Created  time.Time `json:"created"`
	Replaces string    `json:"replaces,omitempty"`
	Body     string    `json:"body,omitempty"`
}

type detailDTO struct {
	issueDTO
	Description   string       `json:"description,omitempty"`
	ParentRef     *refDTO      `json:"parent_ref,omitempty"`
	BlockedByRefs []refDTO     `json:"blocked_by_refs,omitempty"`
	RelatedRefs   []refDTO     `json:"related_refs,omitempty"`
	Blocks        []refDTO     `json:"blocks,omitempty"`
	Children      []refDTO     `json:"children,omitempty"`
	Comments      []commentDTO `json:"comments,omitempty"`
}

func toIssueDTO(i *tasks.Issue) issueDTO {
	d := issueDTO{
		ID: i.ID, Title: i.Title, Status: string(i.Status), Type: string(i.Type),
		Priority: i.Priority, Assignee: i.Assignee, Labels: i.Labels,
		Parent: i.Parent, BlockedBy: i.BlockedBy, Related: i.Related,
		Created: i.Created, Updated: i.Updated, CloseReason: i.CloseReason,
	}
	if !i.Closed.IsZero() {
		c := i.Closed
		d.Closed = &c
	}
	return d
}

func toRefDTO(r tasks.Ref) refDTO {
	return refDTO{ID: r.ID, Title: r.Title, Type: string(r.Type), Status: string(r.Status), Priority: r.Priority}
}

func toRefDTOs(rs []tasks.Ref) []refDTO {
	if len(rs) == 0 {
		return nil
	}
	out := make([]refDTO, len(rs))
	for i, r := range rs {
		out[i] = toRefDTO(r)
	}
	return out
}

func toDetailDTO(d *tasks.Detail) detailDTO {
	out := detailDTO{
		issueDTO:      toIssueDTO(&d.Issue),
		Description:   d.Description,
		BlockedByRefs: toRefDTOs(d.BlockedBy),
		RelatedRefs:   toRefDTOs(d.Related),
		Blocks:        toRefDTOs(d.Blocks),
		Children:      toRefDTOs(d.Children),
	}
	if d.ParentRef != nil {
		r := toRefDTO(*d.ParentRef)
		out.ParentRef = &r
	}
	for _, c := range d.Comments {
		out.Comments = append(out.Comments, commentDTO{
			ID:       c.ID,
			Author:   c.Author,
			Created:  c.Created,
			Replaces: c.Replaces,
			Body:     c.Body,
		})
	}
	return out
}

// --- human-readable rendering ---

// printIssueTable renders a compact one-line-per-issue table.
func printIssueTable(issues []*tasks.Issue) {
	if len(issues) == 0 {
		fmt.Println("(none)")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tP\tTYPE\tSTATUS\tTITLE")
	for _, i := range issues {
		_, _ = fmt.Fprintf(w, "%s\tP%d\t%s\t%s\t%s\n", i.ID, i.Priority, i.Type, i.Status, i.Title)
	}
	_ = w.Flush()
}

// printDetail renders a full issue for human reading.
func printDetail(d *tasks.Detail) {
	fmt.Printf("%s  %s\n", d.ID, d.Title)
	fmt.Printf("  status:   %s\n", d.Status)
	fmt.Printf("  type:     %s   priority: P%d\n", d.Type, d.Priority)
	if d.Assignee != "" {
		fmt.Printf("  assignee: %s\n", d.Assignee)
	}
	if len(d.Labels) > 0 {
		fmt.Printf("  labels:   %s\n", strings.Join(d.Labels, ", "))
	}
	if d.ParentRef != nil {
		fmt.Printf("  parent:   %s  %s\n", d.ParentRef.ID, d.ParentRef.Title)
	}
	printRefLine("blocked by", d.BlockedBy)
	printRefLine("blocks", d.Blocks)
	printRefLine("related", d.Related)
	printRefLine("children", d.Children)
	fmt.Printf("  created:  %s\n", d.Created.Format(time.RFC3339))
	fmt.Printf("  updated:  %s\n", d.Updated.Format(time.RFC3339))
	if !d.Closed.IsZero() {
		fmt.Printf("  closed:   %s", d.Closed.Format(time.RFC3339))
		if d.CloseReason != "" {
			fmt.Printf("  (%s)", d.CloseReason)
		}
		fmt.Println()
	}
	if strings.TrimSpace(d.Description) != "" {
		fmt.Printf("\n%s\n", d.Description)
	}
	if len(d.Comments) > 0 {
		fmt.Printf("\nComments (%d):\n", len(d.Comments))
		for _, c := range d.Comments {
			who := c.Author
			if who == "" {
				who = "?"
			}
			fmt.Printf("  - %s @%s\n    %s\n", c.Created.Format(time.RFC3339), who, indent(c.Body))
		}
	}
}

func printRefLine(label string, refs []tasks.Ref) {
	if len(refs) == 0 {
		return
	}
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = fmt.Sprintf("%s (%s)", r.ID, r.Status)
	}
	fmt.Printf("  %-9s %s\n", label+":", strings.Join(parts, ", "))
}

func indent(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n    ")
}
