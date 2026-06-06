package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hk9890/agent-tasks/sdk/tasks"
)

var updateFlags struct {
	title           string
	description     string
	descriptionFile string
	status          string
	typ             string
	priority        int
	assignee        string
	parent          string
	addLabels       []string
	removeLabels    []string
	setLabels       []string
	clearLabels     bool
}

var updateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update fields on an issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		f := cmd.Flags()

		if f.Changed("description") && f.Changed("description-file") {
			return fmt.Errorf("--description and --description-file are mutually exclusive")
		}

		// Detect whether any mutating flag was passed; if none, bail out early to
		// avoid a no-op write that bumps updated and churns the file.
		mutatingFlags := []string{
			"title", "description", "description-file", "status", "type",
			"priority", "assignee", "parent",
			"add-label", "remove-label", "set-labels", "clear-labels",
		}
		anyChanged := false
		for _, name := range mutatingFlags {
			if f.Changed(name) {
				anyChanged = true
				break
			}
		}
		if !anyChanged {
			return fmt.Errorf("no update flags provided; nothing to change")
		}

		in := tasks.UpdateInput{
			AddLabels:    updateFlags.addLabels,
			RemoveLabels: updateFlags.removeLabels,
			ClearLabels:  updateFlags.clearLabels,
		}
		if f.Changed("set-labels") {
			in.SetLabels = updateFlags.setLabels
		}
		if f.Changed("title") {
			in.Title = &updateFlags.title
		}
		if f.Changed("description") {
			in.Description = &updateFlags.description
		}
		if f.Changed("description-file") {
			b, err := readFileOrStdin(updateFlags.descriptionFile)
			if err != nil {
				return err
			}
			body := string(b)
			in.Description = &body
		}
		if f.Changed("status") {
			st := tasks.Status(updateFlags.status)
			in.Status = &st
		}
		if f.Changed("type") {
			t := tasks.Type(updateFlags.typ)
			in.Type = &t
		}
		if f.Changed("priority") {
			p := updateFlags.priority
			in.Priority = &p
		}
		if f.Changed("assignee") {
			in.Assignee = &updateFlags.assignee
		}
		if f.Changed("parent") {
			in.Parent = &updateFlags.parent
		}

		iss, err := s.Update(args[0], in)
		if err != nil {
			return err
		}
		return reportMutation(iss, "Updated")
	},
}

var closeReason string

var closeCmd = &cobra.Command{
	Use:   "close <id>",
	Short: "Close an issue: stamp close time, move to cold partition",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		iss, err := s.Close(args[0], closeReason)
		if err != nil {
			return err
		}
		return reportMutation(iss, "Closed")
	},
}

var reopenCmd = &cobra.Command{
	Use:   "reopen <id>",
	Short: "Move a closed issue back to the active set",
	Long: `Move a closed issue back to the active set: clear its closed timestamp and
close_reason, set status to open, and move the file from closed/ back to the
hot directory.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		iss, err := s.Reopen(args[0])
		if err != nil {
			return err
		}
		return reportMutation(iss, "Reopened")
	},
}

var depCmd = &cobra.Command{
	Use:   "dep",
	Short: "Manage dependencies between issues",
}

var depAddCmd = &cobra.Command{
	Use:   "add <dependent> <blocker>",
	Short: "Record that <dependent> is blocked by <blocker>",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		if err := s.AddDep(args[0], args[1]); err != nil {
			return err
		}
		if !flagJSON {
			fmt.Printf("%s now blocked by %s\n", args[0], args[1])
		} else {
			return printJSON(map[string]string{"dependent": args[0], "blocker": args[1], "op": "add"})
		}
		return nil
	},
}

var depRmCmd = &cobra.Command{
	Use:   "rm <dependent> <blocker>",
	Short: "Remove a dependency",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		if err := s.RemoveDep(args[0], args[1]); err != nil {
			return err
		}
		if !flagJSON {
			fmt.Printf("%s no longer blocked by %s\n", args[0], args[1])
		} else {
			return printJSON(map[string]string{"dependent": args[0], "blocker": args[1], "op": "rm"})
		}
		return nil
	},
}

var commentCmd = &cobra.Command{
	Use:     "comment",
	Aliases: []string{"comments"},
	Short:   "Manage issue comments",
}

var commentFlags struct {
	author string
	file   string
}

var commentAddCmd = &cobra.Command{
	Use:   "add <id> [body]",
	Short: "Append a comment to an issue",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("file") && len(args) == 2 {
			return fmt.Errorf("body argument and --file are mutually exclusive")
		}
		var body string
		switch {
		case commentFlags.file != "":
			b, err := readFileOrStdin(commentFlags.file)
			if err != nil {
				return err
			}
			body = string(b)
		case len(args) == 2:
			body = args[1]
		default:
			return fmt.Errorf("provide a comment body as an argument or via --file")
		}
		if strings.TrimSpace(body) == "" {
			return fmt.Errorf("comment body is empty")
		}
		author := commentFlags.author
		if author == "" {
			author = os.Getenv("USER")
		}
		c, err := s.AddComment(args[0], author, body)
		if err != nil {
			return err
		}
		if flagJSON {
			return printJSON(commentDTO{
				ID:      c.ID,
				Author:  c.Author,
				Created: c.Created,
				Body:    c.Body,
			})
		}
		fmt.Printf("Commented on %s (comment %s)\n", args[0], c.ID)
		return nil
	},
}

var commentEditCmd = &cobra.Command{
	Use:   "edit <id> <comment-id> [body]",
	Short: "Append a revision to an existing comment",
	Args:  cobra.RangeArgs(2, 3),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("file") && len(args) == 3 {
			return fmt.Errorf("body argument and --file are mutually exclusive")
		}
		var body string
		switch {
		case commentFlags.file != "":
			b, err := readFileOrStdin(commentFlags.file)
			if err != nil {
				return err
			}
			body = string(b)
		case len(args) == 3:
			body = args[2]
		default:
			return fmt.Errorf("provide a body as an argument or via --file")
		}
		if strings.TrimSpace(body) == "" {
			return fmt.Errorf("comment body is empty; use comment rm to delete")
		}
		author := commentFlags.author
		if author == "" {
			author = os.Getenv("USER")
		}
		c, err := s.EditComment(args[0], args[1], author, body)
		if err != nil {
			return err
		}
		if flagJSON {
			return printJSON(commentDTO{
				ID:       c.ID,
				Author:   c.Author,
				Created:  c.Created,
				Replaces: c.Replaces,
				Body:     c.Body,
			})
		}
		fmt.Printf("Edited comment %s on %s (new revision %s)\n", args[1], args[0], c.ID)
		return nil
	},
}

var commentRmCmd = &cobra.Command{
	Use:   "rm <id> <comment-id>",
	Short: "Delete a comment (append a tombstone; idempotent)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		author := commentFlags.author
		if author == "" {
			author = os.Getenv("USER")
		}
		if err := s.DeleteComment(args[0], args[1], author); err != nil {
			return err
		}
		if flagJSON {
			return printJSON(map[string]string{"op": "rm", "issue": args[0], "comment_id": args[1]})
		}
		fmt.Printf("Deleted comment %s from %s\n", args[1], args[0])
		return nil
	},
}

// reportMutation prints a uniform success line (or the JSON detail).
func reportMutation(iss *tasks.Issue, verb string) error {
	if flagJSON {
		return printJSON(toIssueDTO(iss))
	}
	fmt.Printf("%s %s\n", verb, iss.ID)
	return nil
}

func init() {
	uf := updateCmd.Flags()
	uf.StringVar(&updateFlags.title, "title", "", "new title")
	uf.StringVar(&updateFlags.description, "description", "", "new description")
	uf.StringVar(&updateFlags.descriptionFile, "description-file", "", `read description from a file ("-" for stdin)`)
	uf.StringVar(&updateFlags.status, "status", "", "new status (open|in_progress|blocked|closed)")
	uf.StringVar(&updateFlags.typ, "type", "", "new type")
	uf.IntVar(&updateFlags.priority, "priority", 0, "new priority 0..4")
	uf.StringVar(&updateFlags.assignee, "assignee", "", "new assignee")
	uf.StringVar(&updateFlags.parent, "parent", "", "new parent issue ID")
	uf.StringSliceVar(&updateFlags.addLabels, "add-label", nil, "add a label (repeatable)")
	uf.StringSliceVar(&updateFlags.removeLabels, "remove-label", nil, "remove a label (repeatable)")
	uf.StringSliceVar(&updateFlags.setLabels, "set-labels", nil, "replace the label set")
	uf.BoolVar(&updateFlags.clearLabels, "clear-labels", false, "remove all labels")

	closeCmd.Flags().StringVar(&closeReason, "reason", "", "reason for closing")

	rootCmd.AddCommand(reopenCmd)

	depCmd.AddCommand(depAddCmd, depRmCmd)

	commentAddCmd.Flags().StringVar(&commentFlags.author, "author", "", "comment author (default: $USER)")
	commentAddCmd.Flags().StringVar(&commentFlags.file, "file", "", `read body from a file ("-" for stdin)`)

	commentEditCmd.Flags().StringVar(&commentFlags.author, "author", "", "comment author (default: $USER)")
	commentEditCmd.Flags().StringVar(&commentFlags.file, "file", "", `read body from a file ("-" for stdin)`)

	commentRmCmd.Flags().StringVar(&commentFlags.author, "author", "", "comment author for tombstone (default: $USER)")

	commentCmd.AddCommand(commentAddCmd, commentEditCmd, commentRmCmd)

	rootCmd.AddCommand(updateCmd, closeCmd, depCmd, commentCmd)
}
