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
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"

	"github.com/hk9890/task-manager/sdk/tasks"
)

var createFlags struct {
	title           string
	description     string
	descriptionFile string
	typ             string
	priority        int
	assignee        string
	creator         string
	agent           string
	session         string
	labels          []string
	parent          string
	blockedBy       []string
	related         []string
	from            string
}

// createFromEntry is one issue of a `create --from` file: the fields of the
// create flags under their JSON names, plus the ref other entries point at.
type createFromEntry struct {
	Ref         string   `yaml:"ref"`
	Title       string   `yaml:"title"`
	Type        string   `yaml:"type"`
	Priority    *int     `yaml:"priority"`
	Assignee    string   `yaml:"assignee"`
	Labels      []string `yaml:"labels"`
	Description string   `yaml:"description"`
	Parent      string   `yaml:"parent"`
	BlockedBy   []string `yaml:"blocked_by"`
	Related     []string `yaml:"related"`
}

// createFromSetFlags are the flags that still apply with --from: they say who
// files the set, not what one issue holds.
var createFromSetFlags = []string{"from", "creator", "agent", "session"}

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new issue, or a set of issues from a file",
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if createFlags.from == "" {
			return nil
		}
		var perIssue []string
		cmd.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
			if f.Changed && !slices.Contains(createFromSetFlags, f.Name) {
				perIssue = append(perIssue, "--"+f.Name)
			}
		})
		if len(perIssue) > 0 {
			return &usageError{cmd: cmd, msg: "--from takes every issue from the file; it excludes " + strings.Join(perIssue, ", ")}
		}
		// Cobra checks required flags after PreRunE. The file carries the titles,
		// so --title counts as given.
		cmd.Flags().Lookup("title").Changed = true
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		if createFlags.from != "" {
			return createFrom(cmd, s)
		}

		if cmd.Flags().Changed("description") && cmd.Flags().Changed("description-file") {
			return fmt.Errorf("--description and --description-file are mutually exclusive")
		}

		desc := createFlags.description
		if createFlags.descriptionFile != "" {
			b, err := readFileOrStdin(createFlags.descriptionFile)
			if err != nil {
				return err
			}
			desc = string(b)
		}

		by, err := resolveActor(cmd, createFlags.creator, createFlags.agent, createFlags.session)
		if err != nil {
			return err
		}

		in := tasks.CreateInput{
			Title:       createFlags.title,
			Description: desc,
			Type:        tasks.Type(createFlags.typ),
			Assignee:    createFlags.assignee,
			Creator:     by.Name,
			Agent:       by.Agent,
			Session:     by.Session,
			Labels:      createFlags.labels,
			Parent:      createFlags.parent,
			BlockedBy:   createFlags.blockedBy,
			Related:     createFlags.related,
		}
		if cmd.Flags().Changed("priority") {
			p := createFlags.priority
			in.Priority = &p
		}

		res, err := s.Create(in)
		if err != nil {
			return mutationError(err)
		}
		if flagJSON {
			return printJSON(createResultDTO{ID: res.Issue.ID, Store: s.Name(), Hints: res.Hints, Warnings: res.Warnings})
		}
		_, _ = fmt.Fprintf(stdout, "Created %s\n", res.Issue.ID)
		printNotes(res.Hints, res.Warnings)
		return nil
	},
}

// createFrom files the set of issues in the --from file, all or nothing.
func createFrom(cmd *cobra.Command, s *tasks.Store) error {
	raw, err := readFileOrStdin(createFlags.from)
	if err != nil {
		return err
	}
	var file []createFromEntry
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	// A misspelt key (blocked-by) would otherwise drop the edge and file the issue without it.
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("--from %s: %w", createFlags.from, err)
	}
	by, err := resolveActor(cmd, createFlags.creator, createFlags.agent, createFlags.session)
	if err != nil {
		return err
	}
	entries := make([]tasks.BatchEntry, len(file))
	for i, e := range file {
		entries[i] = tasks.BatchEntry{Ref: e.Ref, CreateInput: tasks.CreateInput{
			Title:       e.Title,
			Description: e.Description,
			Type:        tasks.Type(e.Type),
			Priority:    e.Priority,
			Assignee:    e.Assignee,
			Creator:     by.Name,
			Agent:       by.Agent,
			Session:     by.Session,
			Labels:      e.Labels,
			Parent:      e.Parent,
			BlockedBy:   e.BlockedBy,
			Related:     e.Related,
		}}
	}
	results, err := s.CreateBatch(entries)
	if err != nil {
		return mutationError(err)
	}
	if flagJSON {
		dtos := make([]createFromResultDTO, len(results))
		for i, res := range results {
			dtos[i] = createFromResultDTO{Ref: file[i].Ref, createResultDTO: createResultDTO{ID: res.Issue.ID, Store: s.Name(), Hints: res.Hints, Warnings: res.Warnings}}
		}
		return printJSON(dtos)
	}
	for i, res := range results {
		_, _ = fmt.Fprintf(stdout, "Created %s", res.Issue.ID)
		if file[i].Ref != "" {
			_, _ = fmt.Fprintf(stdout, "  (%s)", file[i].Ref)
		}
		_, _ = fmt.Fprintln(stdout)
		printNotes(res.Hints, res.Warnings)
	}
	return nil
}

// readFileOrStdin reads from stdin when path is "-", otherwise from the file.
func readFileOrStdin(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func init() {
	f := createCmd.Flags()
	f.StringVar(&createFlags.title, "title", "", "issue title (required)")
	f.StringVar(&createFlags.description, "description", "", "issue description (markdown body)")
	f.StringVar(&createFlags.descriptionFile, "description-file", "", `read description from a file ("-" for stdin)`)
	f.StringVar(&createFlags.typ, "type", "task", "issue type (task|bug|feature|epic|chore|doc)")
	f.IntVar(&createFlags.priority, "priority", tasks.PriorityDefault, "priority 0 (critical) .. 4 (trivial)")
	f.StringVar(&createFlags.assignee, "assignee", "", "assignee")
	f.StringVar(&createFlags.creator, "creator", "", "creator — who filed the issue; recorded once at creation (default: $USER)")
	addActorFlags(createCmd, &createFlags.agent, &createFlags.session)
	f.StringSliceVar(&createFlags.labels, "label", nil, "label (repeatable)")
	f.StringVar(&createFlags.parent, "parent", "", "parent issue ID")
	f.StringSliceVar(&createFlags.blockedBy, "blocked-by", nil, "blocker issue ID (repeatable)")
	f.StringSliceVar(&createFlags.related, "related", nil, "related issue ID (repeatable)")
	f.StringVar(&createFlags.from, "from", "", `create a set of issues from a YAML file ("-" for stdin), all or nothing; replaces the per-issue flags`)
	_ = createCmd.MarkFlagRequired("title")
	rootCmd.AddCommand(createCmd)
}
