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
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

var initFlags struct {
	prefix  string
	central bool
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a store for the current project (local, or central with --central)",
	Long: `Initialize a new task-manager store: create a .tasks directory with a config
file. The --prefix sets the ID prefix for this project (e.g. "agt" -> agt-0001).
If omitted, it is derived from the directory name.

With --central the store is created under the per-user central root and registered
for this project path instead of a local .tasks directory; --store-name sets its
registry name (default: the project directory name). See CONFIG-SPEC.`,
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
		// Both the prefix and the central store name are derived from the last
		// element of this path, so it has to be absolute before either is taken:
		// `-C .` otherwise derives from "." and falls back to the "task" prefix,
		// which is then immutable, and `-C ..` derives a name the store grammar
		// rejects. Init/InitCentral make it absolute too, but only afterwards.
		root, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		prefix := initFlags.prefix
		if prefix == "" {
			// The engine owns this rule (CONFIG-SPEC §5) — the CLI must not
			// re-implement it, or `init` and `init --central` could drift apart.
			prefix = tasks.DerivePrefix(root)
		}

		if initFlags.central {
			name := flagStoreName
			if name == "" {
				name = filepath.Base(root)
			}
			s, err := tasks.InitCentral(root, name, prefix)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]string{"dir": s.Dir(), "prefix": s.Prefix(), "store": name})
			}
			_, _ = fmt.Fprintf(stdout, "Initialized central store %q at %s (prefix %q)\n", name, s.Dir(), s.Prefix())
			_, _ = fmt.Fprintln(stderr, "next: run 'taskmgr guide' to learn the workflow")
			return nil
		}

		s, err := tasks.Init(root, prefix)
		if err != nil {
			return err
		}
		if flagJSON {
			return printJSON(map[string]string{"dir": s.Dir(), "prefix": s.Prefix()})
		}
		_, _ = fmt.Fprintf(stdout, "Initialized task-manager store at %s (prefix %q)\n", s.Dir(), s.Prefix())
		_, _ = fmt.Fprintln(stderr, "next: run 'taskmgr guide' to learn the workflow")
		return nil
	},
}

func init() {
	initCmd.Flags().StringVar(&initFlags.prefix, "prefix", "", "ID prefix for this project (default: derived from directory name)")
	initCmd.Flags().BoolVar(&initFlags.central, "central", false, "create the store under the central root and register it (instead of a local .tasks)")
	rootCmd.AddCommand(initCmd)
}
