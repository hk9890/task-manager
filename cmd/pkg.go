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

// pkg.go — `taskmgr package` and `taskmgr hook list`: the surface for hook
// packages (CLI-SPEC §2.3).
//
// Three commands, one of which writes. A package itself is authored by hand —
// it is a directory with one owner, so there is nothing for taskmgr to mediate.
// What needs a command is the `use:` list, which lives in a shared, lock-protected
// config file, and the merged chain, which neither config file shows on its own.
package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// ── JSON DTOs (CLI-SPEC §6) ──────────────────────────────────────────────────

// packageDTO is one entry of `package list`: a `use:` entry and what it resolves
// to on this machine.
type packageDTO struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Scope    string `json:"scope"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Hooks    int    `json:"hooks"`
	Guide    int    `json:"guide"`
	Shadowed bool   `json:"shadowed,omitempty"`
}

// packageRemovedDTO is what `package rm` prints: the entry that was removed and
// the file it was removed from. It is deliberately not a packageDTO — status,
// hook and guide counts describe a package a configuration uses, and this one is
// no longer used.
type packageRemovedDTO struct {
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"`
	Scope  string `json:"scope"`
	Config string `json:"config"`
}

// hookDTO is one hook of the effective chain, as `hook list` prints it. ID is the
// effective id — the one a denial reason reports.
type hookDTO struct {
	ID      string   `json:"id"`
	Event   string   `json:"event"`
	When    string   `json:"when,omitempty"`
	Run     []string `json:"run,omitempty"`
	Package string   `json:"package"`
	Scope   string   `json:"scope"`
}

// ── commands ─────────────────────────────────────────────────────────────────

// packageFlags holds the one selector every subcommand of this group reads, as a
// persistent flag so it is accepted in both positions.
var packageFlags struct{ global bool }

var packageCmd = &cobra.Command{
	Use:   "package",
	Short: "Manage the hook packages a config file uses",
	Long: `Manage the hook packages a configuration uses (HOOK-SPEC §3.6).

A package is a directory holding taskmgr-package.yaml and the scripts its hooks
run. You create one by hand: make the directory, write the manifest. taskmgr
never downloads, extracts, or writes a package.

Without --global these commands act on the resolved store's config.yaml, whose
'use:' list travels with the repository. With --global they act on the per-user
config.yaml, whose packages apply to every store on this machine.`,
}

var packageAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a package to the use list of one config file",
	Long: `Add one package to a config file's 'use:' list.

By default the argument is a package name, resolved to <taskmgr home>/packages/<name>.
A name is machine-independent, so a store's config can carry it into git and every
machine finds its own copy.

With --path the argument names a directory instead, resolved against the directory
holding the config file: the store's .tasks/ directory, or the taskmgr home for
--global. A package committed inside a store therefore travels with the store.

The package is loaded and checked before the entry is written, so a package that
could never run is refused here rather than at the next unrelated mutation.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ref := tasks.PackageRef{Name: args[0]}
		if packageAddPath {
			ref = tasks.PackageRef{Path: args[0]}
		}
		return addPackage(ref, packageFlags.global)
	},
}

// packageAddPath selects a path reference over a name one. It is a bool rather
// than a string so the argument position is the same either way, which keeps the
// "state it explicitly" rule from reading as two different commands.
var packageAddPath bool

// addPackage appends ref to the target file's use list.
//
// The package is inspected **before** the entry is written. A package that is
// present but unusable is refused outright: writing it would stop every mutation
// in this store — and, from a store config, in every colleague's clone — with
// nothing to show for it. A package that is merely not installed *is* written,
// with a warning, because a store config travels in git and legitimately names a
// package the machine writing it does not have.
func addPackage(ref tasks.PackageRef, global bool) error {
	t, err := loadConfigTarget(global)
	if err != nil {
		return err
	}

	info, err := inspectPackage(t, ref)
	if err != nil {
		return err
	}
	if info.Status == tasks.PackageBroken {
		return fmt.Errorf("package %s is not usable, so it was not added to %s: %s",
			refLabel(ref), t.path, info.Detail)
	}

	if err := t.update(func(t *configTarget) error {
		for _, cur := range t.use() {
			if sameRefTarget(cur, ref) {
				return fmt.Errorf("package %s is already in %s", refLabel(ref), t.path)
			}
		}
		t.setUse(append(t.use(), ref))
		return nil
	}); err != nil {
		return err
	}

	if flagJSON {
		return printJSON(packageInfoDTO(info))
	}
	_, _ = fmt.Fprintf(stdout, "Added package %s to %s\n", refLabel(ref), t.path)
	if info.Status != tasks.PackageOK {
		_, _ = fmt.Fprintf(stdout, "warning: %s is %s — %s\n", info.Name, info.Status, info.Detail)
	}
	if info.Shadowed {
		_, _ = fmt.Fprintf(stdout, "warning: %s contributes no hooks — %s\n", info.Name, info.Detail)
	}
	return nil
}

// inspectPackage asks the engine what ref would resolve to, without writing it.
func inspectPackage(t *configTarget, ref tasks.PackageRef) (tasks.PackageInfo, error) {
	if t.global {
		return tasks.InspectGlobalPackage(ref)
	}
	return t.store.InspectPackage(ref), nil
}

// sameRefTarget reports whether two entries name the same package. It compares
// what they resolve to rather than the bytes: `packages/p`, `./packages/p` and
// `packages/p/` are one directory, and writing all three leaves two entries that
// contribute nothing and no verb to remove them.
func sameRefTarget(a, b tasks.PackageRef) bool {
	if a == b {
		return true
	}
	an, bn := strings.TrimSpace(a.Name), strings.TrimSpace(b.Name)
	if an != "" || bn != "" {
		return an == bn
	}
	return filepath.Clean(strings.TrimSpace(a.Path)) == filepath.Clean(strings.TrimSpace(b.Path))
}

var packageRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove a package from the use list of one config file",
	Long: `Remove one package from a config file's 'use:' list.

The argument is what 'package add' took: a package name, or with --path the
directory. Either is matched by what it resolves to rather than by its spelling,
so 'packages/p' and './packages/p' name the same entry.

Removing an entry does not load the package, deliberately. An entry that cannot
load is the one that most needs removing — until it is gone it fails every
mutation in this store, and from a store config in every colleague's clone too.

The package directory itself is untouched: taskmgr never writes a package, and
never deletes one.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ref := tasks.PackageRef{Name: args[0]}
		if packageRmPath {
			ref = tasks.PackageRef{Path: args[0]}
		}
		return removePackage(ref, packageFlags.global)
	},
}

// packageRmPath mirrors packageAddPath, so an entry is removed by the same two
// spellings it was added by.
var packageRmPath bool

// removePackage drops the entry matching ref from the target file's use list.
//
// Nothing is inspected first. `add` checks the package because it is about to
// make the configuration depend on it; `rm` is the way back out of exactly the
// state where that check would fail, so requiring the package to load here would
// leave a store with no way to repair its own config but a hand edit of a
// lock-protected file that travels in git.
func removePackage(ref tasks.PackageRef, global bool) error {
	if strings.TrimSpace(ref.Name) == "" && strings.TrimSpace(ref.Path) == "" {
		return fmt.Errorf("name a package to remove, e.g. 'taskmgr package rm doc-policy'")
	}
	t, err := loadConfigTarget(global)
	if err != nil {
		return err
	}

	var removed tasks.PackageRef
	if err := t.update(func(t *configTarget) error {
		cur := t.use()
		next := make([]tasks.PackageRef, 0, len(cur))
		found := false
		for _, c := range cur {
			if !found && sameRefTarget(c, ref) {
				removed, found = c, true
				continue
			}
			next = append(next, c)
		}
		if !found {
			return fmt.Errorf("package %s is not in %s%s", refLabel(ref), t.path, useSummary(cur))
		}
		t.setUse(next)
		return nil
	}); err != nil {
		return err
	}

	if flagJSON {
		return printJSON(packageRemovedDTO{
			Name: refName(removed), Path: removed.Path, Scope: t.scope(), Config: t.path,
		})
	}
	_, _ = fmt.Fprintf(stdout, "Removed package %s from %s\n", refLabel(removed), t.path)
	return nil
}

// useSummary lists what a file does use, so a name that did not match is one
// command away from the right one rather than one hand-opened file away.
func useSummary(refs []tasks.PackageRef) string {
	if len(refs) == 0 {
		return " (it uses no packages)"
	}
	labels := make([]string, 0, len(refs))
	for _, r := range refs {
		labels = append(labels, refLabel(r))
	}
	return " (it uses: " + strings.Join(labels, ", ") + ")"
}

// refName is the package name an entry contributes: the `name:` given, or the
// last segment of `path:` — the rule that makes a directory name the package
// name (HOOK-SPEC §3.6).
func refName(ref tasks.PackageRef) string {
	if n := strings.TrimSpace(ref.Name); n != "" {
		return n
	}
	return filepath.Base(filepath.Clean(strings.TrimSpace(ref.Path)))
}

var packageListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the packages a config file uses, and whether each one loads",
	Long: `List the 'use:' entries that apply, with what each resolves to on this machine:
ok, missing (nothing at that path) or broken (a directory that is not a finished
package).

Without --global the listing covers both files in the order their hooks run —
the per-user config's packages first, then the store's — because that is what
gates the store. With --global it covers the per-user file alone and needs no
store.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		infos, err := listPackages(packageFlags.global)
		if err != nil {
			return err
		}
		out := make([]packageDTO, 0, len(infos))
		for _, in := range infos {
			out = append(out, packageInfoDTO(in))
		}
		if flagJSON {
			return printJSON(out)
		}
		if len(out) == 0 {
			_, _ = fmt.Fprintln(stdout, "no packages configured")
			return nil
		}
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "NAME\tSCOPE\tSTATUS\tHOOKS\tGUIDE\tPATH")
		for _, p := range out {
			status := p.Status
			if p.Shadowed {
				status = "shadowed"
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%s\n", p.Name, p.Scope, status, p.Hooks, p.Guide, p.Path)
		}
		if err := w.Flush(); err != nil {
			return err
		}
		for _, p := range out {
			if p.Detail != "" {
				_, _ = fmt.Fprintf(stdout, "\n%s: %s\n", p.Name, p.Detail)
			}
		}
		return nil
	},
}

// listPackages reads the entries of one scope. The store scope reports both
// files, because a store is gated by both and a listing that showed one would
// not answer the question the command is asked.
func listPackages(global bool) ([]tasks.PackageInfo, error) {
	if global {
		return tasks.GlobalPackages()
	}
	s, err := openStore()
	if err != nil {
		return nil, err
	}
	return s.Packages()
}

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Inspect the lifecycle hooks that gate this store",
}

var hookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the effective hook chain, in the order it runs",
	Long: `List every hook that gates this store, in the order it runs: the per-user
config's packages first, then the store's, each package's hooks in manifest
order (HOOK-SPEC §3.5).

This is the authoritative answer to what gates a store. Neither config file can
give it alone, because the hooks come from the packages the two files name.

The ID column is the effective id — what a denial reason reports and what the
logs record.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		chain, err := s.HookChain()
		if err != nil {
			return err
		}
		out := make([]hookDTO, 0, len(chain))
		for _, h := range chain {
			out = append(out, hookDTO{
				ID: h.ID, Event: h.Event, When: h.When,
				Run: h.Run, Package: h.Package, Scope: h.Scope,
			})
		}
		if flagJSON {
			return printJSON(out)
		}
		if len(out) == 0 {
			_, _ = fmt.Fprintln(stdout, "no hooks gate this store")
			return nil
		}
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tEVENT\tWHEN\tSCOPE\tRUN")
		for _, h := range out {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				h.ID, h.Event, orUnset(h.When), h.Scope, strings.Join(h.Run, " "))
		}
		return w.Flush()
	},
}

// ── helpers ──────────────────────────────────────────────────────────────────

func packageInfoDTO(in tasks.PackageInfo) packageDTO {
	return packageDTO{
		Name: in.Name, Path: in.Path, Scope: in.Scope,
		Status: in.Status, Detail: in.Detail, Hooks: in.Hooks, Guide: in.Guide, Shadowed: in.Shadowed,
	}
}

// refLabel names a reference the way it was written, so a message points at the
// line in the file rather than at a resolved path the user never typed. An entry
// this build cannot model has neither spelling, and says so rather than printing
// as an empty string in the middle of a list.
func refLabel(ref tasks.PackageRef) string {
	switch {
	case strings.TrimSpace(ref.Path) != "":
		return "path " + ref.Path
	case strings.TrimSpace(ref.Name) != "":
		return ref.Name
	}
	return "(an entry with neither name nor path)"
}

func init() {
	packageCmd.PersistentFlags().BoolVar(&packageFlags.global, "global", false, "act on the per-user config instead of the store's")
	packageAddCmd.Flags().BoolVar(&packageAddPath, "path", false, "treat the argument as a directory path instead of a package name")
	packageRmCmd.Flags().BoolVar(&packageRmPath, "path", false, "treat the argument as a directory path instead of a package name")

	packageCmd.AddCommand(packageAddCmd)
	packageCmd.AddCommand(packageRmCmd)
	packageCmd.AddCommand(packageListCmd)
	rootCmd.AddCommand(packageCmd)

	hookCmd.AddCommand(hookListCmd)
	rootCmd.AddCommand(hookCmd)
}
