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

// storeMoveDTO is the JSON shape of `store move` (CLI-SPEC §6).
type storeMoveDTO struct {
	Store       string `json:"store"`
	StorePath   string `json:"store_path"`
	ProjectPath string `json:"project_path"`
}

var storeCmd = &cobra.Command{
	Use:   "store",
	Short: "Inspect and edit central stores",
	Long:  "Commands for the central store registry (CONFIG-SPEC §3): list the entries and move stores between locations.",
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

var (
	moveCentral bool
	moveRename  bool
	moveRelink  bool
	moveTo      string
)

var storeMoveCmd = &cobra.Command{
	// The mode alternatives sit in Use so both cobra's usage line and the
	// generated catalog example (§5) show a form that actually runs — the bare
	// command is always a misuse.
	Use:   "move --central|--rename|--relink [--to <name>]",
	Short: "Promote a local store to central, rename a central store, or re-point one at a moved project",
	Long: `Move a store, in one of three modes (exactly one is required):

  --central   Promote the local .tasks store that resolves here into the central
              root: it becomes <central_root>/stores/<to> and is registered for
              this project. --to defaults to the project directory name. The
              local .tasks directory is gone afterwards — with no confirmation
              prompt, and taskmgr does nothing with git, so committing the
              removal is yours to do.
  --rename    Rename the central store that resolves here to --to: the subfolder
              and its registry entry both change. Use --store-name to name a
              store you are not standing in.
  --relink    Re-point the registry entry named --to at this directory, for a
              project that moved on disk. No files are touched.

A store keeps its ID prefix and its hooks across all three, so existing IDs stay
valid. See CONFIG-SPEC §5.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		switch {
		case moveCentral:
			return runStoreMoveCentral()
		case moveRename:
			return runStoreMoveRename()
		default:
			return runStoreMoveRelink()
		}
	},
}

// runStoreMoveCentral promotes the local store resolving here into the central root.
func runStoreMoveCentral() error {
	_, info, err := tasks.Resolve(resolveOptions(), logOption())
	if err != nil {
		return err
	}
	if info.Kind != tasks.ResolvedLocal {
		return fmt.Errorf("this project already uses a central store at %s; --central promotes a local .tasks store", info.StorePath)
	}
	name := moveTo
	if name == "" {
		name = filepath.Base(info.ProjectPath)
	}
	s, err := tasks.MoveToCentral(info.ProjectPath, name, logOption())
	if err != nil {
		return err
	}
	return emitStoreMove(storeMoveDTO{Store: name, StorePath: s.Dir(), ProjectPath: s.Root()},
		fmt.Sprintf("Moved store to central %q at %s (prefix %q)", name, s.Dir(), s.Prefix()))
}

// runStoreMoveRename renames the central store resolving here to --to.
func runStoreMoveRename() error {
	if moveTo == "" {
		return fmt.Errorf("--rename requires --to <name>")
	}
	_, info, err := tasks.Resolve(resolveOptions(), logOption())
	if err != nil {
		return err
	}
	if info.Kind != tasks.ResolvedCentral && info.Kind != tasks.ResolvedOverrideName {
		return fmt.Errorf("--rename needs a central store, but %s resolves as %s", info.StorePath, info.Kind)
	}
	old := filepath.Base(info.StorePath)
	dir, err := tasks.RenameCentral(old, moveTo)
	if err != nil {
		return err
	}
	return emitStoreMove(storeMoveDTO{Store: moveTo, StorePath: dir, ProjectPath: info.ProjectPath},
		fmt.Sprintf("Renamed central store %q to %q at %s", old, moveTo, dir))
}

// runStoreMoveRelink re-points the entry named --to at the current directory.
func runStoreMoveRelink() error {
	if moveTo == "" {
		return fmt.Errorf("--relink requires --to <name>")
	}
	dir := flagDir
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir = wd
	}
	project, err := tasks.RelinkCentral(moveTo, dir)
	if err != nil {
		return err
	}
	entries, err := tasks.Stores(resolveOptions())
	if err != nil {
		return err
	}
	var storePath string
	for _, e := range entries {
		if e.Store == moveTo {
			storePath = e.StorePath
		}
	}
	return emitStoreMove(storeMoveDTO{Store: moveTo, StorePath: storePath, ProjectPath: project},
		fmt.Sprintf("Central store %q now tracks %s", moveTo, project))
}

func emitStoreMove(d storeMoveDTO, human string) error {
	if flagJSON {
		return printJSON(d)
	}
	fmt.Println(human)
	return nil
}

func init() {
	storeMoveCmd.Flags().BoolVar(&moveCentral, "central", false, "promote the local store here into the central root")
	storeMoveCmd.Flags().BoolVar(&moveRename, "rename", false, "rename the central store here to --to")
	storeMoveCmd.Flags().BoolVar(&moveRelink, "relink", false, "re-point the entry named --to at this directory")
	storeMoveCmd.Flags().StringVar(&moveTo, "to", "", "target registry name (default with --central: the project directory name)")
	storeMoveCmd.MarkFlagsMutuallyExclusive("central", "rename", "relink")
	storeMoveCmd.MarkFlagsOneRequired("central", "rename", "relink")

	storeCmd.AddCommand(storeListCmd)
	storeCmd.AddCommand(storeMoveCmd)
	rootCmd.AddCommand(storeCmd)
}
