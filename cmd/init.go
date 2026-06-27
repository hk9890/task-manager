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
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

var (
	initPrefix  string
	initCentral bool
)

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
		prefix := initPrefix
		if prefix == "" {
			prefix = derivePrefix(root)
		}

		if initCentral {
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
			fmt.Printf("Initialized central store %q at %s (prefix %q)\n", name, s.Dir(), s.Prefix())
			fmt.Fprintln(os.Stderr, "next: run 'taskmgr guide' to learn the workflow")
			return nil
		}

		s, err := tasks.Init(root, prefix)
		if err != nil {
			return err
		}
		if flagJSON {
			return printJSON(map[string]string{"dir": s.Dir(), "prefix": s.Prefix()})
		}
		fmt.Printf("Initialized task-manager store at %s (prefix %q)\n", s.Dir(), s.Prefix())
		fmt.Fprintln(os.Stderr, "next: run 'taskmgr guide' to learn the workflow")
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
	initCmd.Flags().BoolVar(&initCentral, "central", false, "create the store under the central root and register it (instead of a local .tasks)")
	rootCmd.AddCommand(initCmd)
}
