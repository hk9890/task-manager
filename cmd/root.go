package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/hk9890/agent-tasks/sdk/tasks"
)

// Build info, overridable via -ldflags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

var (
	flagJSON bool
	flagDir  string
)

var rootCmd = &cobra.Command{
	Use:   "atctl",
	Short: "Agent Tasks Control — a file-based task tracker",
	Long: `atctl is the command-line tool for agent-tasks: a lean, file-based task
tracker. Each issue is a Markdown file with YAML frontmatter under a project's
.tasks directory. atctl is the only thing that should write those files — it
validates everything and serializes concurrent writers.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "atctl: "+err.Error())
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "emit machine-readable JSON")
	rootCmd.PersistentFlags().StringVarP(&flagDir, "dir", "C", "", "start directory for locating .tasks (default: current directory)")

	rootCmd.AddCommand(versionCmd)
}

// openStore locates and opens the project store, honouring the --dir flag.
func openStore() (*tasks.Store, error) {
	return tasks.Open(flagDir)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagJSON {
			return printJSON(map[string]string{"version": Version, "commit": Commit, "date": Date})
		}
		fmt.Printf("atctl %s (commit %s, built %s)\n", Version, Commit, Date)
		return nil
	},
}
