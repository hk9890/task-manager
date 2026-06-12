package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

var initPrefix string

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a .tasks directory in the current project",
	Long: `Initialize a new agent-tasks store: create a .tasks directory with a config
file. The --prefix sets the ID prefix for this project (e.g. "agt" -> agt-0001).
If omitted, it is derived from the directory name.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		root := flagDir
		if root == "" {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			root = wd
		}
		prefix := initPrefix
		if prefix == "" {
			prefix = derivePrefix(root)
		}
		s, err := tasks.Init(root, prefix)
		if err != nil {
			return err
		}
		if flagJSON {
			return printJSON(map[string]string{"dir": s.Dir(), "prefix": s.Prefix()})
		}
		fmt.Printf("Initialized agent-tasks store at %s (prefix %q)\n", s.Dir(), s.Prefix())
		return nil
	},
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]`)

// derivePrefix turns a directory name into a valid ID prefix.
func derivePrefix(root string) string {
	base := strings.ToLower(filepath.Base(root))
	base = nonAlnum.ReplaceAllString(base, "")
	base = strings.TrimLeft(base, "0123456789")
	if base == "" {
		return "task"
	}
	if len(base) > 8 {
		base = base[:8]
	}
	return base
}

func init() {
	initCmd.Flags().StringVar(&initPrefix, "prefix", "", "ID prefix for this project (default: derived from directory name)")
	rootCmd.AddCommand(initCmd)
}
