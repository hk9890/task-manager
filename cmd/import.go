package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

var importFlags struct {
	file     string
	batch    bool
	runHooks bool
}

// importEnvelope is the stable JSON shape a source adapter (e.g. Jira, GitHub)
// emits per issue. All source-specific logic lives in the adapter; taskmgr only
// validates this envelope and writes it. Timestamps are RFC3339.
type importEnvelope struct {
	SourceID    string            `json:"source_id,omitempty"` // echoed back in the result, not stored
	ID          string            `json:"id,omitempty"`        // optional caller-supplied taskmgr ID
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type,omitempty"`
	Priority    *int              `json:"priority,omitempty"`
	Status      string            `json:"status,omitempty"`
	Assignee    string            `json:"assignee,omitempty"`
	Creator     string            `json:"creator,omitempty"`
	Labels      []string          `json:"labels,omitempty"`
	Parent      string            `json:"parent,omitempty"`
	BlockedBy   []string          `json:"blocked_by,omitempty"`
	Related     []string          `json:"related,omitempty"`
	CreatedAt   string            `json:"created_at,omitempty"`
	UpdatedAt   string            `json:"updated_at,omitempty"`
	ClosedAt    string            `json:"closed_at,omitempty"`
	CloseReason string            `json:"close_reason,omitempty"`
	Comments    []commentEnvelope `json:"comments,omitempty"`
}

type commentEnvelope struct {
	Author    string `json:"author,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	Body      string `json:"body"`
}

// importResult is the per-record outcome reported back to the caller, so it can
// build a source-ID → taskmgr-ID map.
type importResult struct {
	SourceID string `json:"source_id,omitempty"`
	ID       string `json:"id,omitempty"`
	Error    string `json:"error,omitempty"`
}

func parseImportTime(field, s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: invalid RFC3339 timestamp %q", field, s)
	}
	return t.UTC(), nil
}

func (e importEnvelope) toInput() (tasks.ImportInput, error) {
	created, err := parseImportTime("created_at", e.CreatedAt)
	if err != nil {
		return tasks.ImportInput{}, err
	}
	updated, err := parseImportTime("updated_at", e.UpdatedAt)
	if err != nil {
		return tasks.ImportInput{}, err
	}
	closed, err := parseImportTime("closed_at", e.ClosedAt)
	if err != nil {
		return tasks.ImportInput{}, err
	}
	in := tasks.ImportInput{
		ID:          e.ID,
		Title:       e.Title,
		Description: e.Description,
		Type:        tasks.Type(e.Type),
		Priority:    e.Priority,
		Status:      tasks.Status(e.Status),
		Assignee:    e.Assignee,
		Creator:     e.Creator,
		Labels:      e.Labels,
		Parent:      e.Parent,
		BlockedBy:   e.BlockedBy,
		Related:     e.Related,
		Created:     created,
		Updated:     updated,
		Closed:      closed,
		CloseReason: e.CloseReason,
	}
	for i, c := range e.Comments {
		cCreated, err := parseImportTime(fmt.Sprintf("comments[%d].created_at", i), c.CreatedAt)
		if err != nil {
			return tasks.ImportInput{}, err
		}
		in.Comments = append(in.Comments, tasks.ImportComment{
			Author:  c.Author,
			Created: cCreated,
			Body:    c.Body,
		})
	}
	return in, nil
}

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import a complete issue (status, timestamps, comments) from an external system",
	Long: `Import writes a complete, externally-sourced issue verbatim — its final status
(including closed), original timestamps, labels, edges, and full comment log — in
a single validated write. It is the low-level primitive a migration adapter
(e.g. Jira, GitHub) drives: the adapter does all source-specific mapping and emits
the import envelope; taskmgr validates it against the data model and writes it.

The envelope is JSON on stdin (or --file). Edge fields (parent, blocked_by,
related) are taskmgr IDs that must already exist, so import in dependency order.
With --batch the input is a stream of JSON objects (NDJSON or concatenated); each
is imported independently (best-effort) and a {source_id, id, error} result is
emitted per record.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		data, err := readFileOrStdin(importFlags.file)
		if err != nil {
			return err
		}

		if importFlags.batch {
			return runImportBatch(s, data)
		}

		var e importEnvelope
		if err := json.Unmarshal(data, &e); err != nil {
			return fmt.Errorf("parse import envelope: %w", err)
		}
		in, err := e.toInput()
		if err != nil {
			return err
		}
		in.RunHooks = importFlags.runHooks
		res, err := s.Import(in)
		if err != nil {
			return mutationError(err)
		}
		if flagJSON {
			return printJSON(importResult{SourceID: e.SourceID, ID: res.Issue.ID})
		}
		fmt.Printf("Imported %s\n", res.Issue.ID)
		printNotes(res.Hints, res.Warnings)
		return nil
	},
}

// runImportBatch imports a stream of envelopes best-effort: a record that fails
// to validate/import is reported with its error and the rest continue. The
// source-ID → taskmgr-ID map is always printed (JSON); a non-zero exit signals
// that at least one record failed.
func runImportBatch(s *tasks.Store, data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var results []importResult
	failures := 0
	for dec.More() {
		var e importEnvelope
		if err := dec.Decode(&e); err != nil {
			return fmt.Errorf("parse import stream: %w", err)
		}
		in, err := e.toInput()
		if err == nil {
			in.RunHooks = importFlags.runHooks
			var res *tasks.MutationResult
			res, err = s.Import(in)
			if err == nil {
				results = append(results, importResult{SourceID: e.SourceID, ID: res.Issue.ID})
				continue
			}
		}
		results = append(results, importResult{SourceID: e.SourceID, Error: err.Error()})
		failures++
	}
	if err := printJSON(results); err != nil {
		return err
	}
	if failures > 0 {
		return fmt.Errorf("import: %d of %d records failed", failures, len(results))
	}
	return nil
}

func init() {
	f := importCmd.Flags()
	f.StringVar(&importFlags.file, "file", "-", `read the import envelope from a file ("-" for stdin)`)
	f.BoolVar(&importFlags.batch, "batch", false, "import a stream of envelopes (NDJSON), best-effort")
	f.BoolVar(&importFlags.runHooks, "run-hooks", false, "run lifecycle hooks for each imported issue (default: omit)")
	rootCmd.AddCommand(importCmd)
}
