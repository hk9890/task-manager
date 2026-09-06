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

// pkgrepo.go — `taskmgr package repo`: installing package repositories
// (CLI-SPEC §2.3).
//
// A package repository is a git repository whose top-level directories are hook
// packages. Cloning it *is* installing it: there is no copy step and no archive
// to extract, so the only thing taskmgr adds over `git clone` is knowing where
// the clone belongs and what it provides.
//
// git is spawned here and nowhere else. The SDK's contract stays "a directory
// that exists on disk", so nothing on the hook path — the resolution a create or
// a close performs — has any notion of a network, a URL or a revision
// (HOOK-SPEC §3.5).
package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// repoDTO is one installed package repository, as `package repo list` prints it.
type repoDTO struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	URL      string   `json:"url,omitempty"`
	Packages []string `json:"packages"`
	Used     []string `json:"used,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

// repoAddedDTO is what `package repo add` and `package repo rm` print.
type repoAddedDTO struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	URL      string   `json:"url,omitempty"`
	Packages []string `json:"packages,omitempty"`
}

var packageRepoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Install and update the package repositories this machine has",
	Long: `Manage the package repositories installed under <taskmgr home>/packages.

A package repository is a git repository whose top-level directories are hook
packages. Cloning it is installing it — there is no separate copy step.

Installing a repository does not use anything from it: 'package add <name>'
is the separate decision to gate a store with one of its packages.

These commands take no --global. A repository is installed for the machine by
construction, so there is no per-store repository to choose between.`,
}

// packageRepoAddFlags holds the flags of `package repo add`.
var packageRepoAddFlags struct{ as string }

var packageRepoAddCmd = &cobra.Command{
	Use:   "add <url>",
	Short: "Clone a package repository into the taskmgr home",
	Long: `Clone a package repository into <taskmgr home>/packages/<name>.

The name is derived from the URL's last segment with any '.git' suffix removed,
so 'git@github.com:you/task-manager-packages.git' installs as
'task-manager-packages'. Use --as to choose a different one, which is also the
way past a name another installed repository already has.

The clone is the install. Nothing is used until 'package add <package>' names
one of the packages it provides.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := repoRejectsGlobal(); err != nil {
			return err
		}
		return addPackageRepo(args[0], packageRepoAddFlags.as)
	},
}

// addPackageRepo clones url into the repositories directory.
func addPackageRepo(url, as string) error {
	name := strings.TrimSpace(as)
	if name == "" {
		var err error
		name, err = repoNameFromURL(url)
		if err != nil {
			return err
		}
	}

	dir, err := tasks.PackageRepoDir(name)
	if err != nil {
		return err
	}
	// A directory that is already there is never overwritten: it is either this
	// repository (which `repo update` refreshes) or another one that took the
	// derived name, and clobbering either would delete a package some store's
	// config still names.
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists; update it with 'taskmgr package repo update %s', or install this one under a different name with --as <name>", dir, name)
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dir), err)
	}
	if err := runGit("", "clone", "--quiet", url, dir); err != nil {
		return err
	}

	repos, err := tasks.PackageRepos()
	if err != nil {
		return err
	}
	out := repoAddedDTO{Name: name, Path: dir, URL: url}
	for _, r := range repos {
		if r.Name == name {
			out.Packages = r.Packages
		}
	}

	if flagJSON {
		return printJSON(out)
	}
	_, _ = fmt.Fprintf(stdout, "Installed package repository %s at %s\n", name, dir)
	if len(out.Packages) == 0 {
		_, _ = fmt.Fprintf(stdout, "warning: it provides no packages — no top-level directory holds a %s\n", tasks.PackageManifestName)
		return nil
	}
	_, _ = fmt.Fprintf(stdout, "It provides: %s\n", strings.Join(out.Packages, ", "))
	_, _ = fmt.Fprintf(stdout, "next: taskmgr package add %s --global\n", out.Packages[0])
	return nil
}

var packageRepoListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the installed package repositories and what they provide",
	Long: `List the package repositories installed under <taskmgr home>/packages, with the
packages each provides and which of those the per-user config already uses.

This is what is available to 'package add <name>'. A package name two
repositories provide cannot be added by name — remove one of them, or vendor the
one you want into the store and name it by path.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := repoRejectsGlobal(); err != nil {
			return err
		}
		repos, err := tasks.PackageRepos()
		if err != nil {
			return err
		}
		used, err := usedPackageNames()
		if err != nil {
			return err
		}

		out := make([]repoDTO, 0, len(repos))
		for _, r := range repos {
			d := repoDTO{Name: r.Name, Path: r.Path, URL: repoURL(r.Path), Packages: r.Packages, Detail: r.Detail}
			if d.Packages == nil {
				d.Packages = []string{}
			}
			for _, p := range r.Packages {
				if used[p] {
					d.Used = append(d.Used, p)
				}
			}
			out = append(out, d)
		}
		if flagJSON {
			return printJSON(out)
		}
		if len(out) == 0 {
			_, _ = fmt.Fprintln(stdout, "no package repository is installed")
			return nil
		}
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "NAME\tPACKAGES\tUSED\tURL")
		for _, r := range out {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				r.Name, orDash(strings.Join(r.Packages, " ")), orDash(strings.Join(r.Used, " ")), orDash(r.URL))
		}
		if err := w.Flush(); err != nil {
			return err
		}
		for _, r := range out {
			if r.Detail != "" {
				_, _ = fmt.Fprintf(stdout, "\n%s: %s\n", r.Name, r.Detail)
			}
		}
		return nil
	},
}

var packageRepoUpdateCmd = &cobra.Command{
	Use:   "update [name]",
	Short: "Pull the installed package repositories",
	Long: `Pull one installed package repository, or every one of them when no name is
given.

An update changes the packages on this machine at once — a store that uses one
of them is gated by the new version at its next write. Nothing about the 'use:'
lists changes.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := repoRejectsGlobal(); err != nil {
			return err
		}
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		return updatePackageRepos(name)
	},
}

// updatePackageRepos pulls one repository, or every one when name is empty.
//
// Every repository is attempted before the first failure is returned: a machine
// with five repositories and one unreachable remote still gets the other four
// updated, which is what the caller asked for.
func updatePackageRepos(name string) error {
	repos, err := tasks.PackageRepos()
	if err != nil {
		return err
	}
	if name != "" {
		dir, err := tasks.PackageRepoDir(name)
		if err != nil {
			return err
		}
		found := false
		for _, r := range repos {
			if r.Name == name {
				repos, found = []tasks.PackageRepo{r}, true
				break
			}
		}
		if !found {
			return fmt.Errorf("no package repository %q is installed at %s", name, dir)
		}
	}
	if len(repos) == 0 {
		return fmt.Errorf("no package repository is installed; add one with 'taskmgr package repo add <url>'")
	}

	var first error
	out := make([]repoAddedDTO, 0, len(repos))
	for _, r := range repos {
		if err := runGit(r.Path, "pull", "--quiet", "--ff-only"); err != nil {
			if first == nil {
				first = err
			}
			if !flagJSON {
				_, _ = fmt.Fprintf(stdout, "%s: %v\n", r.Name, err)
			}
			continue
		}
		out = append(out, repoAddedDTO{Name: r.Name, Path: r.Path, URL: repoURL(r.Path)})
		if !flagJSON {
			_, _ = fmt.Fprintf(stdout, "Updated %s\n", r.Name)
		}
	}
	if flagJSON && first == nil {
		return printJSON(out)
	}
	return first
}

var packageRepoRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove an installed package repository",
	Long: `Delete one installed package repository from <taskmgr home>/packages.

The 'use:' entries that name its packages are left alone: 'package rm' is the
verb for those, and an entry whose package is gone reports 'missing', which says
what to reinstall rather than silently ungating a store.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := repoRejectsGlobal(); err != nil {
			return err
		}
		name := args[0]

		// Name what a removal is about to break before it breaks it: a `use:`
		// entry naming one of these packages keeps failing every mutation until
		// it is removed too.
		var orphaned []string
		repos, err := tasks.PackageRepos()
		if err != nil {
			return err
		}
		used, err := usedPackageNames()
		if err != nil {
			return err
		}
		for _, r := range repos {
			if r.Name != name {
				continue
			}
			for _, p := range r.Packages {
				if used[p] {
					orphaned = append(orphaned, p)
				}
			}
		}

		dir, err := tasks.RemovePackageRepo(name)
		if err != nil {
			return err
		}
		if flagJSON {
			return printJSON(repoAddedDTO{Name: name, Path: dir, Packages: orphaned})
		}
		_, _ = fmt.Fprintf(stdout, "Removed package repository %s from %s\n", name, dir)
		for _, p := range orphaned {
			_, _ = fmt.Fprintf(stdout, "warning: the per-user config still uses %s; remove it with 'taskmgr package rm %s --global'\n", p, p)
		}
		return nil
	},
}

// ── helpers ──────────────────────────────────────────────────────────────────

// orDash fills an empty list cell. "(unset)" is for a setting nobody has chosen;
// a repository with no package in use has simply not been used yet, which is a
// gap in a list rather than an unset value.
func orDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

// repoRejectsGlobal refuses --global on this group. A repository is installed
// for the machine by construction, so accepting the selector would promise a
// per-store install that does not exist.
func repoRejectsGlobal() error {
	if packageFlags.global {
		return fmt.Errorf("--global is not accepted here: a package repository is installed for this machine, never for one store")
	}
	return nil
}

// repoNameFromURL derives the directory name a clone installs under: the URL's
// last segment with any '.git' suffix removed.
//
// Both git URL shapes end in the same thing — a '/' or ':' separated path whose
// last segment is the repository — so one split covers 'https://host/you/x.git'
// and 'git@host:you/x.git' alike.
func repoNameFromURL(url string) (string, error) {
	s := strings.TrimSpace(url)
	s = strings.TrimSuffix(s, "/")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(s, ".git")
	if _, err := tasks.PackageRepoDir(s); err != nil {
		return "", fmt.Errorf("no usable repository name could be derived from %q: %w; name it with --as <name>", url, err)
	}
	return s, nil
}

// repoURL reports the clone's origin, best effort: a listing is worth printing
// for a directory git cannot describe.
func repoURL(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// usedPackageNames is the set of package names the per-user config's `use:` list
// already names, so a listing can say which of a repository's packages are live.
func usedPackageNames() (map[string]bool, error) {
	cfg, err := tasks.LoadGlobalConfig()
	if err != nil {
		return nil, err
	}
	used := make(map[string]bool, len(cfg.Use))
	for _, ref := range cfg.Use {
		used[refName(ref)] = true
	}
	return used, nil
}

// runGit runs one git command, in dir when it is not empty.
//
// git's own stderr is what a caller needs to see — an unreachable host, a
// missing key, a diverged branch are all its message to give — so it is carried
// into the error rather than replaced with a summary of it.
func runGit(dir string, args ...string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is not on PATH: installing a package repository clones it, so git must be available")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return nil
}

func init() {
	packageRepoAddCmd.Flags().StringVar(&packageRepoAddFlags.as, "as", "", "install under this name instead of the one derived from the URL")

	packageRepoCmd.AddCommand(packageRepoAddCmd)
	packageRepoCmd.AddCommand(packageRepoListCmd)
	packageRepoCmd.AddCommand(packageRepoUpdateCmd)
	packageRepoCmd.AddCommand(packageRepoRmCmd)
	packageCmd.AddCommand(packageRepoCmd)
}
