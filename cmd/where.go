package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// whereDTO is the JSON shape of `where` (CLI-SPEC §6). store_path / project_path
// are omitted when nothing resolves (kind "none").
type whereDTO struct {
	Kind        string `json:"kind"`
	StorePath   string `json:"store_path,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
}

var whereCmd = &cobra.Command{
	Use:   "where",
	Short: "Show which store resolves for the current directory, and why",
	Long: `Report the store the current context resolves to (CONFIG-SPEC §4): its kind
(local, central, override_path, override_name, or none), the store path, and the
project path. Unlike other commands, 'where' never fails on "no store" — it
reports the outcome and exits 0.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, info, err := tasks.Resolve(resolveOptions(), logOption())
		if err != nil {
			if errors.Is(err, tasks.ErrNoStore) {
				return emitWhere(whereDTO{Kind: "none"})
			}
			return err
		}
		return emitWhere(whereDTO{
			Kind:        info.Kind.String(),
			StorePath:   info.StorePath,
			ProjectPath: info.ProjectPath,
		})
	},
}

func emitWhere(d whereDTO) error {
	if flagJSON {
		return printJSON(d)
	}
	if d.Kind == "none" {
		fmt.Println("kind:    none")
		fmt.Println("(no store resolves here — run 'taskmgr init' to create one)")
		return nil
	}
	fmt.Printf("kind:    %s\n", d.Kind)
	fmt.Printf("store:   %s\n", d.StorePath)
	fmt.Printf("project: %s\n", d.ProjectPath)
	return nil
}

func init() {
	rootCmd.AddCommand(whereCmd)
}
