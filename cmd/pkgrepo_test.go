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

// pkgrepo_test.go — `taskmgr package repo`, in-process through cmd.Run
// (CLI-SPEC §2.3).
//
// Every origin here is a git repository in a temp directory, so the tests
// exercise the real clone and pull paths without reaching a network.
package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// originRepo builds a git repository whose top-level directories are packages,
// and returns a URL for it. Each package gets one pre-create hook.
func originRepo(t *testing.T, packages ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range packages {
		writeCmdPackageAt(t, filepath.Join(dir, name), `hooks:
  - id: gate
    event: pre-create
    run: ["/bin/true"]
`)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("packages\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "--quiet", "-b", "main")
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "--quiet", "-m", "packages")
	return dir
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// The whole flow, in the order a user meets it: install a repository, see what
// it provides, use one of its packages, and find that package gating the store.
func TestPackageRepoAdd_ClonesAndItsPackagesBecomeUsable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TASKMGR_HOME", home)
	origin := originRepo(t, "task-writing")
	root := newStore(t)

	out, errOut, code := run(t, "package", "repo", "add", origin)
	if code != 0 {
		t.Fatalf("repo add: exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "task-writing") {
		t.Errorf("stdout = %q, want it to name the package the repository provides", out)
	}

	// The name is the URL's last segment, and the clone is the install.
	installed := filepath.Join(home, "packages", filepath.Base(origin), "task-writing")
	if _, err := os.Stat(filepath.Join(installed, "taskmgr-package.yaml")); err != nil {
		t.Fatalf("want the package installed at %s: %v", installed, err)
	}

	if _, errOut, code := run(t, "package", "add", "--global", "task-writing"); code != 0 {
		t.Fatalf("package add: exit %d, stderr %q", code, errOut)
	}
	out, _, code = run(t, "--dir", root, "--json", "hook", "list")
	if code != 0 {
		t.Fatalf("hook list: exit %d", code)
	}
	if !strings.Contains(out, "pkg:task-writing:gate") {
		t.Errorf("hook list = %q, want the installed package gating the store", out)
	}
}

func TestPackageRepoList_ReportsPackagesAndWhichAreUsed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TASKMGR_HOME", home)
	origin := originRepo(t, "task-writing", "doc-policy")

	if _, errOut, code := run(t, "package", "repo", "add", origin); code != 0 {
		t.Fatalf("repo add: exit %d, stderr %q", code, errOut)
	}
	if _, errOut, code := run(t, "package", "add", "--global", "doc-policy"); code != 0 {
		t.Fatalf("package add: exit %d, stderr %q", code, errOut)
	}

	out, _, code := run(t, "--json", "package", "repo", "list")
	if code != 0 {
		t.Fatalf("repo list: exit %d", code)
	}
	var repos []struct {
		Name     string   `json:"name"`
		URL      string   `json:"url"`
		Packages []string `json:"packages"`
		Used     []string `json:"used"`
	}
	if err := json.Unmarshal([]byte(out), &repos); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(repos) != 1 {
		t.Fatalf("listed %d repositories, want 1:\n%s", len(repos), out)
	}
	if got := strings.Join(repos[0].Packages, ","); got != "doc-policy,task-writing" {
		t.Errorf("packages = %q, want both, sorted", got)
	}
	if got := strings.Join(repos[0].Used, ","); got != "doc-policy" {
		t.Errorf("used = %q, want only the package the config names", got)
	}
	if repos[0].URL != origin {
		t.Errorf("url = %q, want the clone's origin %q", repos[0].URL, origin)
	}
}

// The name is derived from the URL, so two repositories whose URLs end the same
// way collide. Overwriting either would delete a package a config still names,
// so the collision is refused and --as is named as the way past it.
func TestPackageRepoAdd_ANameAlreadyTakenIsRefusedAndNamesTheFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TASKMGR_HOME", home)
	origin := originRepo(t, "task-writing")

	if _, _, code := run(t, "package", "repo", "add", origin); code != 0 {
		t.Fatal("setup: repo add")
	}
	_, errOut, code := run(t, "package", "repo", "add", origin)
	if code == 0 {
		t.Fatal("installing over an existing repository must be refused")
	}
	if !strings.Contains(errOut, "--as") {
		t.Errorf("stderr = %q, want it to name --as", errOut)
	}

	if _, errOut, code := run(t, "package", "repo", "add", origin, "--as", "second"); code != 0 {
		t.Fatalf("repo add --as: exit %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(home, "packages", "second")); err != nil {
		t.Errorf("--as must install under the given name: %v", err)
	}
}

// Two installed repositories providing one package name cannot both mint
// "pkg:<name>:<hook>", so the name is refused with both repositories named.
func TestPackageAdd_ANameTwoRepositoriesProvideIsRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TASKMGR_HOME", home)
	origin := originRepo(t, "task-writing")

	if _, _, code := run(t, "package", "repo", "add", origin, "--as", "first"); code != 0 {
		t.Fatal("setup: repo add first")
	}
	if _, _, code := run(t, "package", "repo", "add", origin, "--as", "second"); code != 0 {
		t.Fatal("setup: repo add second")
	}

	_, errOut, code := run(t, "package", "add", "--global", "task-writing")
	if code == 0 {
		t.Fatal("an ambiguous package name must be refused")
	}
	for _, repo := range []string{"first", "second"} {
		if !strings.Contains(errOut, repo) {
			t.Errorf("stderr = %q, want it to name %s", errOut, repo)
		}
	}
}

func TestPackageRepoUpdate_PullsWhatTheOriginGained(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TASKMGR_HOME", home)
	origin := originRepo(t, "task-writing")

	if _, _, code := run(t, "package", "repo", "add", origin); code != 0 {
		t.Fatal("setup: repo add")
	}

	writeCmdPackageAt(t, filepath.Join(origin, "doc-policy"), `hooks:
  - id: gate
    event: pre-create
    run: ["/bin/true"]
`)
	git(t, origin, "add", "-A")
	git(t, origin, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "--quiet", "-m", "add doc-policy")

	if _, errOut, code := run(t, "package", "repo", "update"); code != 0 {
		t.Fatalf("repo update: exit %d, stderr %q", code, errOut)
	}
	out, _, _ := run(t, "package", "repo", "list")
	if !strings.Contains(out, "doc-policy") {
		t.Errorf("repo list = %q, want the package the pull brought in", out)
	}
}

func TestPackageRepoUpdate_AnUninstalledNameIsAnError(t *testing.T) {
	t.Setenv("TASKMGR_HOME", t.TempDir())
	_, errOut, code := run(t, "package", "repo", "update", "nope")
	if code == 0 {
		t.Fatal("updating a repository that is not installed must fail")
	}
	if !strings.Contains(errOut, "nope") {
		t.Errorf("stderr = %q, want it to name the repository", errOut)
	}
}

// Removing a repository leaves the `use:` entries alone — `package rm` is the
// verb for those — but says which of them stop resolving, because until they
// are removed they fail every mutation in the stores that name them.
func TestPackageRepoRm_RemovesTheCloneAndWarnsAboutUsedPackages(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TASKMGR_HOME", home)
	origin := originRepo(t, "task-writing")

	if _, _, code := run(t, "package", "repo", "add", origin); code != 0 {
		t.Fatal("setup: repo add")
	}
	if _, _, code := run(t, "package", "add", "--global", "task-writing"); code != 0 {
		t.Fatal("setup: package add")
	}

	out, errOut, code := run(t, "package", "repo", "rm", filepath.Base(origin))
	if code != 0 {
		t.Fatalf("repo rm: exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "task-writing") {
		t.Errorf("stdout = %q, want it to warn that the config still uses the package", out)
	}
	if _, err := os.Stat(filepath.Join(home, "packages", filepath.Base(origin))); !os.IsNotExist(err) {
		t.Errorf("the clone must be gone, got %v", err)
	}

	// The entry is still there, and now reports what to reinstall.
	out, _, _ = run(t, "package", "list", "--global")
	if !strings.Contains(out, "missing") {
		t.Errorf("package list = %q, want the orphaned entry reported missing", out)
	}
}

func TestPackageRepoRm_AnUninstalledNameIsAnError(t *testing.T) {
	t.Setenv("TASKMGR_HOME", t.TempDir())
	if _, _, code := run(t, "package", "repo", "rm", "nope"); code == 0 {
		t.Fatal("removing a repository that is not installed must fail")
	}
}

// A repository is installed for the machine, so the selector that chooses
// between a store and the per-user config has no meaning here and is refused
// rather than silently ignored.
func TestPackageRepo_RejectsGlobal(t *testing.T) {
	t.Setenv("TASKMGR_HOME", t.TempDir())
	_, errOut, code := run(t, "package", "repo", "list", "--global")
	if code == 0 {
		t.Fatal("--global must be refused on the repo group")
	}
	if !strings.Contains(errOut, "--global") {
		t.Errorf("stderr = %q, want it to name the flag", errOut)
	}
}

func TestPackageRepoList_NothingInstalled(t *testing.T) {
	t.Setenv("TASKMGR_HOME", t.TempDir())
	out, _, code := run(t, "package", "repo", "list")
	if code != 0 {
		t.Fatalf("repo list: exit %d", code)
	}
	if !strings.Contains(out, "no package repository is installed") {
		t.Errorf("stdout = %q, want it to say nothing is installed", out)
	}
}
