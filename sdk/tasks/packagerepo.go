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

// packagerepo.go — the installed package repositories under <home>/packages
// (HOOK-SPEC §3.5): what is installed, what each provides, and removing one.
//
// A repository is a directory whose subdirectories are packages. taskmgr does
// not create one — `taskmgr package repo add` clones it, and the clone is the
// install — so this file reads the result and removes it, and nothing here
// reaches a network or knows what a clone is.
package tasks

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// PackageRepo is one installed package repository: the directory a `name:`
// entry is resolved against, and the packages it provides.
type PackageRepo struct {
	// Name is the repository's directory name under <home>/packages.
	Name string
	// Path is that directory.
	Path string
	// Packages names the packages the repository provides, sorted. A directory
	// counts as a package when it holds a manifest — an ordinary file the
	// repository ships beside its packages (a README, a licence) is not one.
	Packages []string
	// Detail explains a repository that provides nothing.
	Detail string
}

// PackageReposDir returns <home>/packages, the directory the installed package
// repositories live in.
func PackageReposDir() (string, error) {
	e := env.NewOS()
	home, err := taskmgrHome(e)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, packagesSubdir), nil
}

// PackageRepoDir returns the directory one named repository occupies, whether or
// not it is installed. The name follows the package-name grammar, because it is
// a directory name under a shared parent exactly as a package's is.
func PackageRepoDir(name string) (string, error) {
	if !validPackageName(name) {
		return "", fmt.Errorf("invalid repository name %q: one path segment, starting with a letter or digit, at most 64 characters", name)
	}
	root, err := PackageReposDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

// PackageRepos lists the installed package repositories, sorted by name.
//
// A home with no packages directory is not an error: nothing is installed, which
// is a state a fresh machine is in and a listing can report.
func PackageRepos() ([]PackageRepo, error) {
	root, err := PackageReposDir()
	if err != nil {
		return nil, err
	}
	return packageRepos(vfs.NewOS(), root)
}

// packageRepos is PackageRepos against an explicit seam and root, so the listing
// rule is testable on vfs.Mem.
func packageRepos(fs vfs.FS, root string) ([]PackageRepo, error) {
	entries, err := fs.ReadDir(root)
	if err != nil {
		if vfs.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", root, err)
	}

	var repos []PackageRepo
	for _, e := range entries {
		// The same skip findPackageInRepos makes: only a directory named like a
		// package is a repository, so a stray file or a dot-directory is not
		// listed as one that provides nothing.
		if !e.IsDir() || !validPackageName(e.Name()) {
			continue
		}
		repo := PackageRepo{Name: e.Name(), Path: filepath.Join(root, e.Name())}
		repo.Packages, repo.Detail = repoPackages(fs, repo.Path)
		repos = append(repos, repo)
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	return repos, nil
}

// repoPackages names the packages one repository directory provides.
//
// The manifest is what makes a directory a package (HOOK-SPEC §3.6), so it is
// what the listing tests for: a repository's README, its licence and its own
// .git directory are not packages and are not reported as broken ones.
func repoPackages(fs vfs.FS, dir string) (names []string, detail string) {
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return nil, fmt.Sprintf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() || !validPackageName(e.Name()) {
			continue
		}
		if _, err := fs.Stat(filepath.Join(dir, e.Name(), PackageManifestName)); err != nil {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		detail = "no directory here holds a " + PackageManifestName
	}
	return names, detail
}

// RemovePackageRepo deletes one installed repository and returns the directory
// it removed.
//
// It is the one place taskmgr deletes a package directory, and it is scoped to
// exactly that: a repository under <home>/packages, named rather than pathed, so
// nothing outside the home is reachable. The `use:` entries that named its
// packages are left alone — `package rm` is the verb for those, and an entry
// whose package is gone reports `missing`, which says what to reinstall.
func RemovePackageRepo(name string) (string, error) {
	dir, err := PackageRepoDir(name)
	if err != nil {
		return "", err
	}
	fs := vfs.NewOS()
	if _, err := fs.Stat(dir); err != nil {
		if vfs.IsNotExist(err) {
			return "", fmt.Errorf("no package repository %q is installed at %s", name, dir)
		}
		return "", err
	}
	if err := fs.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("remove %s: %w", dir, err)
	}
	return dir, nil
}
