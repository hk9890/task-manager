package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/hk9890/agent-tasks/sdk/tasks"
)

var createFlags struct {
	title           string
	description     string
	descriptionFile string
	typ             string
	priority        int
	assignee        string
	labels          []string
	parent          string
	blockedBy       []string
	related         []string
}

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new issue",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
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

		in := tasks.CreateInput{
			Title:       createFlags.title,
			Description: desc,
			Type:        tasks.Type(createFlags.typ),
			Assignee:    createFlags.assignee,
			Labels:      createFlags.labels,
			Parent:      createFlags.parent,
			BlockedBy:   createFlags.blockedBy,
			Related:     createFlags.related,
		}
		if cmd.Flags().Changed("priority") {
			p := createFlags.priority
			in.Priority = &p
		}

		iss, err := s.Create(in)
		if err != nil {
			return err
		}
		if flagJSON {
			return printJSON(map[string]string{"id": iss.ID})
		}
		fmt.Printf("Created %s\n", iss.ID)
		return nil
	},
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
	f.StringVar(&createFlags.typ, "type", "task", "issue type (task|bug|feature|epic|chore)")
	f.IntVar(&createFlags.priority, "priority", tasks.PriorityDefault, "priority 0 (critical) .. 4 (trivial)")
	f.StringVar(&createFlags.assignee, "assignee", "", "assignee")
	f.StringSliceVar(&createFlags.labels, "label", nil, "label (repeatable)")
	f.StringVar(&createFlags.parent, "parent", "", "parent issue ID")
	f.StringSliceVar(&createFlags.blockedBy, "blocked-by", nil, "blocker issue ID (repeatable)")
	f.StringSliceVar(&createFlags.related, "related", nil, "related issue ID (repeatable)")
	_ = createCmd.MarkFlagRequired("title")
	rootCmd.AddCommand(createCmd)
}
