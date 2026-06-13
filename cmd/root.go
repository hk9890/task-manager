package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
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
	Use:   "taskmgr",
	Short: "Task Manager — a file-based task tracker",
	Long: `taskmgr is a lean, file-based task tracker. Each issue is a Markdown file
with YAML frontmatter under a project's .tasks directory. taskmgr is the only
thing that should write those files — it validates everything and serializes
concurrent writers.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// silentError marks an error whose output the command already emitted to stdout
// (e.g. a hook_denied JSON object); Execute then exits non-zero without printing
// the usual "taskmgr: …" stderr line, so a --json consumer sees only the JSON.
type silentError struct{ err error }

func (e silentError) Error() string { return e.err.Error() }
func (e silentError) Unwrap() error { return e.err }

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		var se silentError
		if !errors.As(err, &se) {
			fmt.Fprintln(os.Stderr, "taskmgr: "+err.Error())
		}
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
	return tasks.Open(flagDir, logOption())
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagJSON {
			return printJSON(map[string]string{"version": Version, "commit": Commit, "date": Date})
		}
		fmt.Printf("taskmgr %s (commit %s, built %s)\n", Version, Commit, Date)
		return nil
	},
}
