package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hk9890/agent-tasks/sdk/tasks"
)

type filterFlags struct {
	statuses    []string
	types       []string
	priorityMin int
	priorityMax int
	assignee    string
	labels      []string
	text        string
	all         bool
	ready       bool
	blocked     bool
	sort        string
	reverse     bool
	limit       int
}

func addFilterFlags(cmd *cobra.Command, ff *filterFlags) {
	f := cmd.Flags()
	f.StringSliceVar(&ff.statuses, "status", nil, "filter by status (repeatable/csv)")
	f.StringSliceVar(&ff.types, "type", nil, "filter by type (repeatable/csv)")
	f.IntVar(&ff.priorityMin, "priority-min", 0, "minimum priority (most urgent)")
	f.IntVar(&ff.priorityMax, "priority-max", 0, "maximum priority (least urgent)")
	f.StringVar(&ff.assignee, "assignee", "", "filter by assignee")
	f.StringSliceVar(&ff.labels, "label", nil, "require label (repeatable; all must match)")
	f.BoolVar(&ff.all, "all", false, "include closed issues")
	f.BoolVar(&ff.ready, "ready", false, "only ready issues")
	f.BoolVar(&ff.blocked, "blocked", false, "only blocked issues")
	f.StringVar(&ff.sort, "sort", "", "sort by: id|priority|created|updated|closed (default: priority)")
	f.BoolVar(&ff.reverse, "reverse", false, "reverse sort order")
	f.IntVar(&ff.limit, "limit", 0, "maximum number of results (0 = all)")
}

func (ff *filterFlags) build(cmd *cobra.Command) tasks.Filter {
	flt := tasks.Filter{
		Assignee:      ff.assignee,
		Labels:        ff.labels,
		Text:          ff.text,
		IncludeClosed: ff.all,
		OnlyReady:     ff.ready,
		OnlyBlocked:   ff.blocked,
		Sort:          tasks.SortField(ff.sort),
		Reverse:       ff.reverse,
		Limit:         ff.limit,
	}
	for _, s := range ff.statuses {
		flt.Statuses = append(flt.Statuses, tasks.Status(s))
	}
	for _, t := range ff.types {
		flt.Types = append(flt.Types, tasks.Type(t))
	}
	if cmd.Flags().Changed("priority-min") {
		p := ff.priorityMin
		flt.PriorityMin = &p
	}
	if cmd.Flags().Changed("priority-max") {
		p := ff.priorityMax
		flt.PriorityMax = &p
	}
	return flt
}

func runList(cmd *cobra.Command, ff *filterFlags) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	issues, err := s.List(ff.build(cmd))
	if err != nil {
		return err
	}
	return emitIssues(issues)
}

func emitIssues(issues []*tasks.Issue) error {
	if flagJSON {
		out := make([]issueDTO, len(issues))
		for i, iss := range issues {
			out[i] = toIssueDTO(iss)
		}
		return printJSON(out)
	}
	printIssueTable(issues)
	return nil
}

var listFilter filterFlags

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List issues (open by default)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runList(cmd, &listFilter)
	},
}

var searchFilter filterFlags

var searchCmd = &cobra.Command{
	Use:   "search <text>",
	Short: "Search issues by text (ID, title, description)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		searchFilter.text = args[0]
		return runList(cmd, &searchFilter)
	},
}

var readyLimit int

var readyCmd = &cobra.Command{
	Use:   "ready",
	Short: "List issues ready to work (open, no open blockers)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		issues, err := s.Ready()
		if err != nil {
			return err
		}
		if readyLimit > 0 && len(issues) > readyLimit {
			issues = issues[:readyLimit]
		}
		return emitIssues(issues)
	},
}

type blockedDTO struct {
	issueDTO
	BlockedBy []refDTO `json:"blocked_by_refs"`
}

var blockedCmd = &cobra.Command{
	Use:   "blocked",
	Short: "List blocked issues and what blocks them",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		blocked, err := s.Blocked()
		if err != nil {
			return err
		}
		if flagJSON {
			out := make([]blockedDTO, len(blocked))
			for i, b := range blocked {
				out[i] = blockedDTO{issueDTO: toIssueDTO(b.Issue), BlockedBy: toRefDTOs(b.BlockedBy)}
			}
			return printJSON(out)
		}
		if len(blocked) == 0 {
			fmt.Println("(none)")
			return nil
		}
		for _, b := range blocked {
			fmt.Printf("%s  P%d  %s\n", b.Issue.ID, b.Issue.Priority, b.Issue.Title)
			for _, r := range b.BlockedBy {
				fmt.Printf("    blocked by %s (%s)  %s\n", r.ID, r.Status, r.Title)
			}
		}
		return nil
	},
}

var labelsCmd = &cobra.Command{
	Use:   "labels",
	Short: "List labels in use",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		labels, err := s.Labels()
		if err != nil {
			return err
		}
		if flagJSON {
			return printJSON(labels)
		}
		for _, l := range labels {
			fmt.Println(l)
		}
		return nil
	},
}

var statusesCmd = &cobra.Command{
	Use:   "statuses",
	Short: "List valid statuses",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := make([]string, len(tasks.Statuses))
		for i, s := range tasks.Statuses {
			out[i] = string(s)
		}
		if flagJSON {
			return printJSON(out)
		}
		for _, s := range out {
			fmt.Println(s)
		}
		return nil
	},
}

var typesCmd = &cobra.Command{
	Use:   "types",
	Short: "List valid issue types",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := make([]string, len(tasks.Types))
		for i, t := range tasks.Types {
			out[i] = string(t)
		}
		if flagJSON {
			return printJSON(out)
		}
		for _, t := range out {
			fmt.Println(t)
		}
		return nil
	},
}

func init() {
	addFilterFlags(listCmd, &listFilter)
	addFilterFlags(searchCmd, &searchFilter)
	readyCmd.Flags().IntVar(&readyLimit, "limit", 0, "maximum number of results (0 = all)")
	rootCmd.AddCommand(listCmd, searchCmd, readyCmd, blockedCmd, labelsCmd, statusesCmd, typesCmd)
}
