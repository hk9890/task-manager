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

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// out and errOut are where command output goes. They are the process's own
// streams for a real invocation and whatever Run was handed for an in-process
// one; see setOutput in root.go. Command code writes through these rather than
// naming os.Stdout directly, which is what makes output assertable without
// building and forking the binary.
var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

// printJSON writes v as indented JSON to the command's stdout.
func printJSON(v any) error {
	return newJSONEncoder(stdout).Encode(v)
}

// newJSONEncoder is the one place the CLI's JSON form is set (CLI-SPEC §1):
// indented, HTML left unescaped.
func newJSONEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc
}

// --- JSON DTOs: stable, snake_case shapes for agents ---

type issueDTO struct {
	ID          string     `json:"id"`
	Store       string     `json:"store,omitempty"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	Type        string     `json:"type"`
	Priority    int        `json:"priority"`
	Assignee    string     `json:"assignee,omitempty"`
	Creator     string     `json:"creator,omitempty"`
	Agent       string     `json:"agent,omitempty"`
	Session     string     `json:"session,omitempty"`
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

// mutationDTO is the --json shape of a successful gated mutation: the issue plus
// any hook hints and post-hook warnings (HOOK-SPEC §6.2). The embedded issueDTO
// keeps the issue fields at the top level, so the shape is backward-compatible
// (hints/warnings appear only when present).
type mutationDTO struct {
	issueDTO
	Hints    []string `json:"hints,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// createResultDTO is the --json shape of `create`: the new id plus any hook
// hints/warnings.
type createResultDTO struct {
	ID       string   `json:"id"`
	Store    string   `json:"store,omitempty"`
	Hints    []string `json:"hints,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// createFromResultDTO is one element of the --json array of `create --from`:
// createResultDTO plus the entry's ref, so a caller maps its own names to IDs.
type createFromResultDTO struct {
	Ref string `json:"ref,omitempty"`
	createResultDTO
}

// The operation names carried in the "op" field of the five DTOs below. They
// are the SDK's own log op values (MONITORING.md § What `write` covers), copied
// rather than imported because they are unexported there — a mismatch is caught
// by TestSpec_CLI_EdgeJSON_OpVocabulary, which pins each string.
const (
	opDepAdd        = "dep_add"
	opDepRemove     = "dep_remove"
	opRelAdd        = "rel_add"
	opRelRemove     = "rel_remove"
	opCommentDelete = "comment_delete"
)

// edgeResultDTO is the --json shape of the four edge commands: `dep add`,
// `dep rm`, `rel add` and `rel rm`. commentDeleteDTO below is the fifth of the
// same family, `comment rm`.
//
// These five printed an inline map[string]string with three different key
// vocabularies — "dependent"/"blocker" for one pair, "a"/"b" for the other, and
// an "op" that read "add"/"rm" while the log records for the identical
// operations read "dep_add"/"dep_remove". None of the shapes appeared in
// CLI-SPEC §6 and no test covered them, so nothing held the keys in place: a
// rename during ordinary work would have changed a released command's output
// with nothing to report it. The op values below are the log's own constants
// (log.go), so one operation now has one name across the JSON and the records.
//
// The edge is named "from"/"to" for both pairs. A dependency reads from the
// dependent to its blocker; a related link is stored on "from" and derived on
// the other side, which is the direction `rel add` actually writes.
type edgeResultDTO struct {
	Op   string `json:"op"`
	From string `json:"from"`
	To   string `json:"to"`
}

// commentDeleteDTO is the --json shape of `comment rm`.
type commentDeleteDTO struct {
	Op        string `json:"op"`
	Issue     string `json:"issue"`
	CommentID string `json:"comment_id"`
}

// hookDeniedDTO is the --json error printed when a pre-hook denies a transition
// (HOOK-SPEC §6.2). "error" is always "hook_denied".
type hookDeniedDTO struct {
	Error   string `json:"error"`
	Event   string `json:"event"`
	Hook    string `json:"hook"`
	IssueID string `json:"issue_id,omitempty"`
	// Entry and Ref locate the denied issue in a `create --from` file: the
	// 1-based position and the entry's ref. IssueID is of no use there, since
	// the issue was never written.
	Entry  int      `json:"entry,omitempty"`
	Ref    string   `json:"ref,omitempty"`
	Exit   int      `json:"exit"`
	Reason string   `json:"reason"`
	Hints  []string `json:"hints,omitempty"`
}

type commentDTO struct {
	ID       string    `json:"id"`
	Author   string    `json:"author,omitempty"`
	Agent    string    `json:"agent,omitempty"`
	Session  string    `json:"session,omitempty"`
	Created  time.Time `json:"created"`
	Replaces string    `json:"replaces,omitempty"`
	Body     string    `json:"body,omitempty"`
}

type detailDTO struct {
	issueDTO
	Description string `json:"description,omitempty"`
	// BodyExternal reports that the body came from the content sidecar rather
	// than the .md (TASK-STORAGE-SPEC §4.6). Description is fully resolved either
	// way; this only says where the bytes live.
	BodyExternal  bool         `json:"body_external,omitempty"`
	ParentRef     *refDTO      `json:"parent_ref,omitempty"`
	BlockedByRefs []refDTO     `json:"blocked_by_refs,omitempty"`
	RelatedRefs   []refDTO     `json:"related_refs,omitempty"`
	Blocks        []refDTO     `json:"blocks,omitempty"`
	Children      []refDTO     `json:"children,omitempty"`
	Comments      []commentDTO `json:"comments,omitempty"`
}

// toIssueDTO renders one issue. store is the registry name of the store it came
// from, empty for a local store, so a client merging rows from several stores
// can tell them apart — the ID cannot, since two projects whose directories
// share a name share a prefix (CONFIG-SPEC §5).
func toIssueDTO(store string, i *tasks.Issue) issueDTO {
	d := issueDTO{
		ID: i.ID, Store: store, Title: i.Title, Status: string(i.Status), Type: string(i.Type),
		Priority: i.Priority, Assignee: i.Assignee, Creator: i.Creator,
		Agent: i.Agent, Session: i.Session, Labels: i.Labels,
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

func toCommentDTO(c tasks.Comment) commentDTO {
	return commentDTO{
		ID: c.ID, Author: c.Author, Agent: c.Agent, Session: c.Session,
		Created: c.Created, Replaces: c.Replaces, Body: c.Body,
	}
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

func toDetailDTO(store string, d *tasks.Detail) detailDTO {
	out := detailDTO{
		issueDTO:      toIssueDTO(store, &d.Issue),
		Description:   d.Description,
		BodyExternal:  d.BodyExternal,
		BlockedByRefs: toRefDTOs(d.BlockedByRefs),
		RelatedRefs:   toRefDTOs(d.RelatedRefs),
		Blocks:        toRefDTOs(d.Blocks),
		Children:      toRefDTOs(d.Children),
	}
	if d.ParentRef != nil {
		r := toRefDTO(*d.ParentRef)
		out.ParentRef = &r
	}
	for _, c := range d.Comments {
		out.Comments = append(out.Comments, toCommentDTO(c))
	}
	return out
}

// --- human-readable rendering ---

// printIssueTable renders a compact one-line-per-issue table.
func printIssueTable(issues []*tasks.Issue) {
	if len(issues) == 0 {
		_, _ = fmt.Fprintln(stdout, "(none)")
		return
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tP\tTYPE\tSTATUS\tTITLE")
	for _, i := range issues {
		_, _ = fmt.Fprintf(w, "%s\tP%d\t%s\t%s\t%s\n", i.ID, i.Priority, i.Type, i.Status, i.Title)
	}
	_ = w.Flush()
}

// detailSection is one part of the human detail block and the detailDTO fields it
// renders, so `show --fields` selects the same parts in both output modes.
type detailSection struct {
	fields []string
	print  func(d *tasks.Detail, full bool)
}

// jsonOnlyDetailFields are the detailDTO keys the human block has no line for.
// `show --fields` refuses them without --json: a header with nothing under it
// reads as "this issue has no creator".
var jsonOnlyDetailFields = []string{"store", "creator", "body_external"}

var detailSections = []detailSection{
	{[]string{"status"}, func(d *tasks.Detail, _ bool) {
		_, _ = fmt.Fprintf(stdout, "  status:   %s\n", d.Status)
	}},
	{[]string{"type", "priority"}, func(d *tasks.Detail, _ bool) {
		_, _ = fmt.Fprintf(stdout, "  type:     %s   priority: P%d\n", d.Type, d.Priority)
	}},
	{[]string{"assignee"}, func(d *tasks.Detail, _ bool) {
		if d.Assignee != "" {
			_, _ = fmt.Fprintf(stdout, "  assignee: %s\n", d.Assignee)
		}
	}},
	{[]string{"agent", "session"}, func(d *tasks.Detail, _ bool) {
		if d.Agent != "" {
			_, _ = fmt.Fprintf(stdout, "  agent:    %s\n", agentLine(d.Agent, d.Session))
		}
	}},
	{[]string{"labels"}, func(d *tasks.Detail, _ bool) {
		if len(d.Labels) > 0 {
			_, _ = fmt.Fprintf(stdout, "  labels:   %s\n", strings.Join(d.Labels, ", "))
		}
	}},
	{[]string{"parent", "parent_ref"}, func(d *tasks.Detail, _ bool) {
		if d.ParentRef != nil {
			_, _ = fmt.Fprintf(stdout, "  parent:   %s  %s\n", d.ParentRef.ID, d.ParentRef.Title)
		}
	}},
	{[]string{"blocked_by", "blocked_by_refs"}, func(d *tasks.Detail, _ bool) { printRefLine("blocked by", d.BlockedByRefs) }},
	{[]string{"blocks"}, func(d *tasks.Detail, _ bool) { printRefLine("blocks", d.Blocks) }},
	{[]string{"related", "related_refs"}, func(d *tasks.Detail, _ bool) { printRefLine("related", d.RelatedRefs) }},
	{[]string{"children"}, func(d *tasks.Detail, _ bool) { printRefLine("children", d.Children) }},
	{[]string{"created"}, func(d *tasks.Detail, _ bool) {
		_, _ = fmt.Fprintf(stdout, "  created:  %s\n", d.Created.Format(time.RFC3339))
	}},
	{[]string{"updated"}, func(d *tasks.Detail, _ bool) {
		_, _ = fmt.Fprintf(stdout, "  updated:  %s\n", d.Updated.Format(time.RFC3339))
	}},
	{[]string{"closed", "close_reason"}, func(d *tasks.Detail, _ bool) {
		if d.Closed.IsZero() {
			return
		}
		_, _ = fmt.Fprintf(stdout, "  closed:   %s", d.Closed.Format(time.RFC3339))
		if d.CloseReason != "" {
			_, _ = fmt.Fprintf(stdout, "  (%s)", d.CloseReason)
		}
		_, _ = fmt.Fprintln(stdout)
	}},
	{[]string{"description"}, printBody},
	{[]string{"comments"}, func(d *tasks.Detail, _ bool) { printComments(d.Comments) }},
}

// printDetail renders an issue for human reading: the header line, then every
// section, or only those that render a field in selected. A section the caller
// named prints in full — naming the description is asking for all of it.
func printDetail(d *tasks.Detail, selected []string) {
	_, _ = fmt.Fprintf(stdout, "%s  %s\n", d.ID, d.Title)
	for _, sec := range detailSections {
		named := slices.ContainsFunc(sec.fields, func(f string) bool { return slices.Contains(selected, f) })
		if len(selected) == 0 || named {
			sec.print(d, named)
		}
	}
}

func printComments(comments []tasks.Comment) {
	if len(comments) == 0 {
		return
	}
	_, _ = fmt.Fprintf(stdout, "\nComments (%d):\n", len(comments))
	for _, c := range comments {
		who := c.Author
		if who == "" {
			who = "?"
		}
		if c.Agent != "" {
			who += " via " + c.Agent
		}
		_, _ = fmt.Fprintf(stdout, "  - %s @%s\n    %s\n", c.Created.Format(time.RFC3339), who, indent(c.Body))
	}
}

// agentLine renders the agent provenance of a write for human reading. The
// session is appended only when there is one: most harnesses publish no session
// id, and "claude-code ()" would read as a missing value rather than an absent
// concept.
func agentLine(agent, session string) string {
	if session == "" {
		return agent
	}
	return agent + "  session: " + session
}

// showBodyLimit bounds how much of a body the human renderer prints. Bodies are
// unbounded — an issue may legitimately hold a generated HTML page or a long
// migration plan — and dumping megabytes into a terminal is not a useful default.
// JSON output is never truncated: a script or an agent asked for the whole thing.
const showBodyLimit = 4096

// printBody renders the issue body, truncated for humans unless full is set. The
// truncation notice says where the untruncated content is, so the output is a
// pointer rather than a dead end.
func printBody(d *tasks.Detail, full bool) {
	body := d.Description
	if strings.TrimSpace(body) == "" {
		return
	}
	if full || len(body) <= showBodyLimit {
		_, _ = fmt.Fprintf(stdout, "\n%s\n", body)
		return
	}
	_, _ = fmt.Fprintf(stdout, "\n%s\n", truncateRunes(body, showBodyLimit))
	where := "--fields description or --json for the full body"
	if d.BodyExternal {
		where = fmt.Sprintf("full content in %s/%s, or --fields description, or --json", "content", d.ID)
	}
	_, _ = fmt.Fprintf(stdout, "\n[body is %d bytes; showing the first %d — %s]\n", len(body), showBodyLimit, where)
}

// truncateRunes cuts s to at most n bytes without splitting a UTF-8 rune.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func printRefLine(label string, refs []tasks.Ref) {
	if len(refs) == 0 {
		return
	}
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = fmt.Sprintf("%s (%s)", r.ID, r.Status)
	}
	_, _ = fmt.Fprintf(stdout, "  %-9s %s\n", label+":", strings.Join(parts, ", "))
}

func indent(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n    ")
}
