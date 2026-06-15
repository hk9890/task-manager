package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// storeEntryDTO is the JSON shape of one `store list` entry (CLI-SPEC §6).
type storeEntryDTO struct {
	Path      string `json:"path"`
	Store     string `json:"store"`
	StorePath string `json:"store_path"`
}

var storeCmd = &cobra.Command{
	Use:   "store",
	Short: "Inspect central stores",
	Long:  "Commands for the central store registry (CONFIG-SPEC §3). Registry editing verbs (move/link/unlink) are a use-gated follow-up; edit mapping.yaml by hand for now.",
}

var storeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List central registry entries",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := tasks.Stores(resolveOptions())
		if err != nil {
			return err
		}
		if flagJSON {
			out := make([]storeEntryDTO, 0, len(entries))
			for _, e := range entries {
				out = append(out, storeEntryDTO{Path: e.Path, Store: e.Store, StorePath: e.StorePath})
			}
			return printJSON(out)
		}
		if len(entries) == 0 {
			fmt.Println("no central stores")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "STORE\tPROJECT\tSTORE PATH")
		for _, e := range entries {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", e.Store, e.Path, e.StorePath)
		}
		return w.Flush()
	},
}

func init() {
	storeCmd.AddCommand(storeListCmd)
	rootCmd.AddCommand(storeCmd)
}
