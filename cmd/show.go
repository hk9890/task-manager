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

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

var showCmd = &cobra.Command{
	Use:   "show <id> [more ids...]",
	Short: "Show full detail for one or more issues",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		// Every ID resolves before anything prints, so a missing one leaves stdout empty.
		details := make([]*tasks.Detail, len(args))
		for i, id := range args {
			if details[i], err = s.Detail(id); err != nil {
				return err
			}
		}
		if flagJSON {
			dtos := make([]detailDTO, len(details))
			for i, d := range details {
				dtos[i] = toDetailDTO(s.Name(), d)
			}
			return printJSON(dtos)
		}
		for i, d := range details {
			if i > 0 {
				_, _ = fmt.Fprintln(stdout)
			}
			printDetail(d)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(showCmd)
}
