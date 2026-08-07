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
	"path/filepath"
	"strings"
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
		entries, err := tasks.Stores()
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
			_, _ = fmt.Fprintln(stdout, "no central stores")
			return nil
		}
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "STORE\tPROJECT\tSTORE PATH")
		for _, e := range entries {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", e.Store, e.Path, e.StorePath)
		}
		return w.Flush()
	},
}

var storeMoveFlags struct {
	central bool
	rename  bool
	relink  bool
	to      string
}

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

The store's config.yaml moves verbatim, so its ID prefix and hooks block are
kept and existing IDs stay valid. Note that hooks run with the working directory
set to the *project* root, which --central does not change: a hook whose argv is
a path into .tasks (["\.tasks/validators/check.sh"]) stops resolving once the
store leaves the project, so rewrite such hooks to an absolute path or to
["sh", "-c", "$TASKMGR_STORE/validators/check.sh"]. See CONFIG-SPEC §5.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate the mode here rather than with cobra's flag groups: those
		// count a flag as "set" whenever it appears, so --rename=false would
		// satisfy MarkFlagsOneRequired and then fall through to another mode.
		// Doing it by value also lets a misuse render the full help block,
		// which cobra's own group errors bypass (they carry no annotation
		// Execute can detect).
		switch modes := pickedModes(); len(modes) {
		case 1:
			// fallthrough to dispatch below
		case 0:
			return &usageError{cmd: cmd, msg: "exactly one of --central, --rename, --relink is required"}
		default:
			return &usageError{cmd: cmd, msg: "--central, --rename and --relink are mutually exclusive; got " + strings.Join(modes, " and ")}
		}
		switch {
		case storeMoveFlags.central:
			return runStoreMoveCentral()
		case storeMoveFlags.rename:
			return runStoreMoveRename(cmd)
		default:
			return runStoreMoveRelink(cmd)
		}
	},
}

// pickedModes returns the mode flags that are actually true, by value — an
// explicit --central=false picks nothing.
func pickedModes() []string {
	var picked []string
	for _, m := range []struct {
		name string
		on   bool
	}{{"--central", storeMoveFlags.central}, {"--rename", storeMoveFlags.rename}, {"--relink", storeMoveFlags.relink}} {
		if m.on {
			picked = append(picked, m.name)
		}
	}
	return picked
}

// resolveForMove resolves the store for a move, turning the SDK's generic
// ErrNoStore into the actionable guidance CLI-SPEC §1 requires of every command
// but init and where (openStore does the same for the store-opening commands).
func resolveForMove() (tasks.ResolveInfo, error) {
	_, info, err := tasks.Resolve(resolveOptions(), logOption())
	if errors.Is(err, tasks.ErrNoStore) {
		return info, fmt.Errorf("%w — run 'taskmgr init' to create one", err)
	}
	return info, err
}

// runStoreMoveCentral promotes the local store resolving here into the central root.
func runStoreMoveCentral() error {
	info, err := resolveForMove()
	if err != nil {
		return err
	}
	if info.Kind != tasks.ResolvedLocal {
		return fmt.Errorf("this project already uses a central store at %s; --central promotes a local .tasks store", info.StorePath)
	}
	name := storeMoveFlags.to
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
func runStoreMoveRename(cmd *cobra.Command) error {
	if storeMoveFlags.to == "" {
		return &usageError{cmd: cmd, msg: "--rename requires --to <name>"}
	}
	info, err := resolveForMove()
	if err != nil {
		return err
	}
	if info.Kind != tasks.ResolvedCentral && info.Kind != tasks.ResolvedOverrideName {
		return fmt.Errorf("--rename needs a central store, but %s resolves as %s", info.StorePath, info.Kind)
	}
	old := filepath.Base(info.StorePath)
	dir, err := tasks.RenameCentral(old, storeMoveFlags.to)
	if err != nil {
		return err
	}
	return emitStoreMove(storeMoveDTO{Store: storeMoveFlags.to, StorePath: dir, ProjectPath: info.ProjectPath},
		fmt.Sprintf("Renamed central store %q to %q at %s", old, storeMoveFlags.to, dir))
}

// runStoreMoveRelink re-points the entry named --to at the current directory.
func runStoreMoveRelink(cmd *cobra.Command) error {
	if storeMoveFlags.to == "" {
		return &usageError{cmd: cmd, msg: "--relink requires --to <name>"}
	}
	// Make the directory absolute here: unlike every other command, relink does
	// not route --dir through Resolve, and the SDK resolves a relative path
	// against the central root, not the cwd. filepath.Abs("") is the cwd.
	dir, err := filepath.Abs(flagDir)
	if err != nil {
		return err
	}
	project, err := tasks.RelinkCentral(storeMoveFlags.to, dir)
	if err != nil {
		return err
	}
	entries, err := tasks.Stores()
	if err != nil {
		return err
	}
	var storePath string
	for _, e := range entries {
		if e.Store == storeMoveFlags.to {
			storePath = e.StorePath
		}
	}
	return emitStoreMove(storeMoveDTO{Store: storeMoveFlags.to, StorePath: storePath, ProjectPath: project},
		fmt.Sprintf("Central store %q now tracks %s", storeMoveFlags.to, project))
}

func emitStoreMove(d storeMoveDTO, human string) error {
	if flagJSON {
		return printJSON(d)
	}
	_, _ = fmt.Fprintln(stdout, human)
	return nil
}

func init() {
	storeMoveCmd.Flags().BoolVar(&storeMoveFlags.central, "central", false, "promote the local store here into the central root")
	storeMoveCmd.Flags().BoolVar(&storeMoveFlags.rename, "rename", false, "rename the central store here to --to")
	storeMoveCmd.Flags().BoolVar(&storeMoveFlags.relink, "relink", false, "re-point the entry named --to at this directory")
	storeMoveCmd.Flags().StringVar(&storeMoveFlags.to, "to", "", "target registry name (default with --central: the project directory name)")
	// The mode flags are validated in RunE, not via cobra's flag groups — see
	// the comment there.

	storeCmd.AddCommand(storeListCmd)
	storeCmd.AddCommand(storeMoveCmd)
	rootCmd.AddCommand(storeCmd)
}
