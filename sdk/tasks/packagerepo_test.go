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

// packagerepo_test.go — resolving a `name:` entry against the installed package
// repositories, and listing what they provide (HOOK-SPEC §3.5).
package tasks

import (
	"errors"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// repoHome writes packages into named repositories under /hm and returns the
// seam they live on. Each entry is "<repo>/<package>".
func repoHome(t *testing.T, packages ...string) vfs.FS {
	t.Helper()
	fs := vfs.NewMem()
	for _, p := range packages {
		repo, name, ok := strings.Cut(p, "/")
		if !ok {
			t.Fatalf("fixture %q is not <repo>/<package>", p)
		}
		writeHomePackage(t, fs, "/hm", repo, name, []Hook{{ID: "g", Event: "pre-create", Run: []string{"/bin/true"}}})
	}
	return fs
}

func TestResolveRef_NameResolvesInsideTheRepositoryThatProvidesIt(t *testing.T) {
	fs := repoHome(t, "packages-a/doc-policy")

	dir, name, err := resolveRef(fs, PackageRef{Name: "doc-policy"}, "/hm", "/store/.tasks")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if dir != "/hm/packages/packages-a/doc-policy" {
		t.Errorf("dir = %q, want it inside the repository that provides the name", dir)
	}
	if name != "doc-policy" {
		t.Errorf("name = %q, want the package name", name)
	}
}

// The package name is what mints an effective hook id, so a name two
// repositories provide is refused rather than resolved by an ordering rule: a
// denial reporting "pkg:doc-policy:gate" could not otherwise say which package
// refused.
func TestResolveRef_NameInTwoRepositoriesIsRefused(t *testing.T) {
	fs := repoHome(t, "packages-a/doc-policy", "packages-b/doc-policy")

	_, _, err := resolveRef(fs, PackageRef{Name: "doc-policy"}, "/hm", "/store/.tasks")
	if !errors.Is(err, ErrPackageAmbiguous) {
		t.Fatalf("err = %v, want ErrPackageAmbiguous", err)
	}
	for _, repo := range []string{"packages-a", "packages-b"} {
		if !strings.Contains(err.Error(), repo) {
			t.Errorf("error %q must name %s, so the reader knows what to choose between", err, repo)
		}
	}
}

// A name nothing provides is `missing`, not `broken`: the entry is well formed
// and the repair is an install. Callers tell the two apart with errors.Is, and
// `package add` writes a missing entry with a warning rather than refusing it.
func TestResolveRef_NameInNoRepositoryIsMissing(t *testing.T) {
	fs := repoHome(t, "packages-a/other")

	_, _, err := resolveRef(fs, PackageRef{Name: "doc-policy"}, "/hm", "/store/.tasks")
	if !errors.Is(err, ErrPackageMissing) {
		t.Fatalf("err = %v, want ErrPackageMissing", err)
	}
}

func TestResolveRef_NoRepositoriesInstalledIsMissing(t *testing.T) {
	_, _, err := resolveRef(vfs.NewMem(), PackageRef{Name: "doc-policy"}, "/hm", "/store/.tasks")
	if !errors.Is(err, ErrPackageMissing) {
		t.Fatalf("err = %v, want ErrPackageMissing for a home with no packages directory", err)
	}
}

// A `name:` entry with no home resolves to nothing rather than to the relative
// "packages/...", which would resolve against the process working directory —
// a path any local process can plant a hook in.
func TestResolveRef_NameWithNoHomeIsAnError(t *testing.T) {
	_, _, err := resolveRef(vfs.NewMem(), PackageRef{Name: "lint"}, "", "/store/.tasks")
	if err == nil || !strings.Contains(err.Error(), "no taskmgr home") {
		t.Fatalf("err = %v, want it to say the home could not be located", err)
	}
}

// A package directory with no manifest still resolves. Reporting it "not
// installed" would point at the wrong repair — the package is there, and what
// is wrong with it is loadPackage's answer to give.
func TestResolveRef_ADirectoryWithNoManifestStillResolves(t *testing.T) {
	fs := vfs.NewMem()
	if err := fs.MkdirAll("/hm/packages/packages-a/half-written", 0o755); err != nil {
		t.Fatal(err)
	}

	dir, _, err := resolveRef(fs, PackageRef{Name: "half-written"}, "/hm", "/store/.tasks")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := loadPackage(fs, dir, "half-written"); err == nil {
		t.Error("loading it must fail, and that is the error the caller should see")
	} else if errors.Is(err, ErrPackageMissing) {
		t.Errorf("err = %v, want a broken package rather than a missing one", err)
	}
}

func TestPackageRepos_ListsRepositoriesAndWhatTheyProvide(t *testing.T) {
	fs := repoHome(t, "packages-b/lint", "packages-a/doc-policy", "packages-a/style")

	repos, err := packageRepos(fs, "/hm/packages")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("listed %d repositories, want 2: %+v", len(repos), repos)
	}
	// Sorted, so a listing does not reorder itself between runs.
	if repos[0].Name != "packages-a" || repos[1].Name != "packages-b" {
		t.Errorf("names = %q, %q, want them sorted", repos[0].Name, repos[1].Name)
	}
	if got := strings.Join(repos[0].Packages, ","); got != "doc-policy,style" {
		t.Errorf("packages-a provides %q, want doc-policy,style", got)
	}
}

// A repository's own README, licence and .git directory are not packages. A
// listing that reported them would send the reader looking for a manifest that
// was never meant to be there.
func TestPackageRepos_IgnoresWhatIsNotAPackage(t *testing.T) {
	fs := repoHome(t, "packages-a/doc-policy")
	if err := fs.WriteAtomic("/hm/packages/packages-a/README.md", []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fs.MkdirAll("/hm/packages/packages-a/.git/objects", 0o755); err != nil {
		t.Fatal(err)
	}

	repos, err := packageRepos(fs, "/hm/packages")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(repos) != 1 || len(repos[0].Packages) != 1 || repos[0].Packages[0] != "doc-policy" {
		t.Errorf("repos = %+v, want only the one package", repos)
	}
}

func TestPackageRepos_ARepositoryProvidingNothingSaysSo(t *testing.T) {
	fs := vfs.NewMem()
	if err := fs.MkdirAll("/hm/packages/empty-repo", 0o755); err != nil {
		t.Fatal(err)
	}

	repos, err := packageRepos(fs, "/hm/packages")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("listed %d repositories, want the empty one reported: %+v", len(repos), repos)
	}
	if repos[0].Detail == "" {
		t.Error("a repository that provides no package must explain itself")
	}
}

// Nothing installed is a state a fresh machine is in, and a listing can say so.
func TestPackageRepos_NoPackagesDirectoryIsNotAnError(t *testing.T) {
	repos, err := packageRepos(vfs.NewMem(), "/hm/packages")
	if err != nil {
		t.Fatalf("a home with no packages directory must list empty, not fail: %v", err)
	}
	if len(repos) != 0 {
		t.Errorf("repos = %+v, want none", repos)
	}
}
