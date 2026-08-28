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
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// whereDTO is the JSON shape of `where` (CLI-SPEC §6). store_path / project_path
// are omitted when nothing resolves (kind "none"); store is omitted for a local
// store, which has no registry name.
type whereDTO struct {
	Kind        string `json:"kind"`
	Store       string `json:"store,omitempty"`
	StorePath   string `json:"store_path,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
}

var whereCmd = &cobra.Command{
	Use:   "where",
	Short: "Show which store resolves for the current directory, and why",
	Long: `Report the store the current context resolves to (CONFIG-SPEC §4): its kind
(local, central, override_name, or none), the store path, and the project path.
Unlike other commands, 'where' never fails on "no store" — it reports the
outcome and exits 0.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, info, err := tasks.Resolve(resolveOptions(), logOption())
		if err != nil {
			if errors.Is(err, tasks.ErrNoStore) {
				return emitWhere(whereDTO{Kind: "none"})
			}
			return err
		}
		return emitWhere(whereDTO{
			Kind:        info.Kind.String(),
			Store:       s.Name(),
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
		_, _ = fmt.Fprintln(stdout, "kind:    none")
		_, _ = fmt.Fprintln(stdout, "(no store resolves here — run 'taskmgr init' to create one)")
		return nil
	}
	_, _ = fmt.Fprintf(stdout, "kind:    %s\n", d.Kind)
	if d.Store != "" {
		_, _ = fmt.Fprintf(stdout, "name:    %s\n", d.Store)
	}
	_, _ = fmt.Fprintf(stdout, "store:   %s\n", d.StorePath)
	_, _ = fmt.Fprintf(stdout, "project: %s\n", d.ProjectPath)
	return nil
}

func init() {
	rootCmd.AddCommand(whereCmd)
}
