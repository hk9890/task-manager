package cmd

import (
	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show full detail for an issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		d, err := s.Detail(args[0])
		if err != nil {
			return err
		}
		if flagJSON {
			return printJSON(toDetailDTO(d))
		}
		printDetail(d)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(showCmd)
}
