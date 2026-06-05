package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hk9890/agent-tasks/sdk/tasks"
)

// filterFlags holds the flag values for list/search commands.
// The structured field filters (Statuses, Types, etc.) have been removed;
// filtering is now done exclusively via the -q/--query expression language
// (QUERY-SPEC.md). The --all flag maps to Filter.IncludeClosed.
type filterFlags struct {
	query   string // filter expression (-q/--query); closed-scope auto-detected
	all     bool   // include closed issues (reads the cold partition)
	sort    string // sort field
	reverse bool   // reverse sort order
	limit   int    // maximum number of results (0 = all)
}

func addFilterFlags(cmd *cobra.Command, ff *filterFlags) {
	f := cmd.Flags()
	// -q/--query: filter expression (QUERY-SPEC.md). Closed-referencing expressions
	// automatically include the cold partition; --all is not required in that case.
	f.StringVarP(&ff.query, "query", "q", "", "filter expression (QUERY-SPEC.md); closed-scope auto-detected")
	// --all reads the cold partition (closed/) in addition to the hot set. When a
	// closed-referencing -q expression is used, --all is not needed but harmless.
	f.BoolVar(&ff.all, "all", false, "include closed issues (reads the cold partition)")
	f.StringVar(&ff.sort, "sort", "", "sort by: id|priority|created|updated|closed (default: priority)")
	f.BoolVar(&ff.reverse, "reverse", false, "reverse sort order")
	f.IntVar(&ff.limit, "limit", 0, "maximum number of results (0 = all)")
}

func (ff *filterFlags) build() tasks.Filter {
	return tasks.Filter{
		Expr:          ff.query,
		IncludeClosed: ff.all,
		Sort:          tasks.SortField(ff.sort),
		Reverse:       ff.reverse,
		Limit:         ff.limit,
	}
}

func runList(cmd *cobra.Command, ff *filterFlags) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	issues, err := s.List(ff.build())
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
		// Translate the search text into a text ~ "<text>" expression.
		// If the user also passed -q, combine them with &&.
		textExpr := fmt.Sprintf(`text ~ %q`, args[0])
		if searchFilter.query != "" {
			searchFilter.query = "(" + searchFilter.query + ") && " + textExpr
		} else {
			searchFilter.query = textExpr
		}
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
