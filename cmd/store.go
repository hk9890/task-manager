// Copyright 2026 Hans Kohlreiter
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

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
