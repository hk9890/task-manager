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

// packages_test.go — the hook-package format and the two-list merge
// (HOOK-SPEC §3.5/§3.6). Every test here pins a defect a review reproduced
// against a scratch store; the shape they share is that the old behaviour was
// silent, so each one asserts the noise as much as the outcome.
package tasks

import (
	"errors"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// ── argv[0] resolution (§3.6) ───────────────────────────────────────────────

func TestResolveHookArgv_InsidePackage(t *testing.T) {
	cases := []struct {
		name string
		run  []string
		want string
	}{
		{"relative with a slash lands inside the package", []string{"./hooks/x.sh"}, "/pkg/hooks/x.sh"},
		{"bare relative path too", []string{"hooks/x.sh"}, "/pkg/hooks/x.sh"},
		{"a PATH lookup is left alone", []string{"sh"}, "sh"},
		{"an absolute path is left alone", []string{"/usr/bin/gate"}, "/usr/bin/gate"},
	}
	for _, c := range cases {
		got := resolveHookArgv(c.run, "/pkg")
		if got[0] != c.want {
			t.Errorf("%s: argv[0] = %q, want %q", c.name, got[0], c.want)
		}
	}
}

// A manifest is written once and read on every platform, so '/' has to count as
// a separator wherever taskmgr runs. Testing only filepath.Separator left every
// script-shipping package broken on Windows, which is a release target.
func TestResolveHookArgv_SlashCountsOnEveryPlatform(t *testing.T) {
	got := resolveHookArgv([]string{"./hooks/x.sh", "--strict"}, "/pkg")
	if !strings.HasSuffix(got[0], "hooks/x.sh") || !strings.HasPrefix(got[0], "/pkg") {
		t.Errorf("argv[0] = %q, want it joined onto the package directory", got[0])
	}
	if got[1] != "--strict" {
		t.Errorf("argv[1] = %q, want every other argument passed verbatim", got[1])
	}
}

// ── reference shape (§3.5) ──────────────────────────────────────────────────

func TestPackageRef_Shape(t *testing.T) {
	cases := []struct {
		name    string
		ref     PackageRef
		errWant string
	}{
		{"both set", PackageRef{Name: "a", Path: "b"}, "not both"},
		{"neither set", PackageRef{}, "one of name or path"},
		{"invalid name", PackageRef{Name: "../escape"}, "invalid package name"},
		{"relative path escaping the config dir", PackageRef{Path: "../outside"}, "leaves the directory"},
		{"relative path escaping further", PackageRef{Path: "../../outside"}, "leaves the directory"},
		{"a bare parent", PackageRef{Path: ".."}, "leaves the directory"},
		{"an absolute path", PackageRef{Path: "/abs/p"}, "is an absolute path"},
		{"a posix-absolute path, refused on every platform", PackageRef{Path: "/opt/pkg"}, "is an absolute path"},
	}
	for _, c := range cases {
		err := refShape(c.ref)
		if err == nil {
			t.Errorf("%s: want an error, got none", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.errWant) {
			t.Errorf("%s: error %q does not contain %q", c.name, err, c.errWant)
		}
	}
	for _, ok := range []PackageRef{{Name: "doc-policy"}, {Path: "packages/p"}} {
		if err := refShape(ok); err != nil {
			t.Errorf("%+v: unexpected error %v", ok, err)
		}
	}
}

// A `name:` reference means <home>/packages/<name> and nothing else. Joining an
// empty home yielded the *relative* "packages/<name>", which resolves against
// the process working directory — so taskmgr loaded and ran hooks from a path
// any local process can plant.
func TestPackageDir_NameWithNoHomeIsAnError(t *testing.T) {
	_, _, err := packageDir(PackageRef{Name: "lint"}, "", "/store/.tasks")
	if err == nil {
		t.Fatal("a name reference with no taskmgr home must be an error, not a relative path")
	}
	if !strings.Contains(err.Error(), "no taskmgr home") {
		t.Errorf("error %q must say the home could not be located", err)
	}
}

func TestPackageDir_ResolvesNameAndPath(t *testing.T) {
	dir, name, err := packageDir(PackageRef{Name: "doc-policy"}, "/hm", "/store/.tasks")
	if err != nil || dir != "/hm/packages/doc-policy" || name != "doc-policy" {
		t.Errorf("name ref = (%q, %q, %v)", dir, name, err)
	}
	dir, name, err = packageDir(PackageRef{Path: "packages/p"}, "/hm", "/store/.tasks")
	if err != nil || dir != "/store/.tasks/packages/p" || name != "p" {
		t.Errorf("path ref = (%q, %q, %v)", dir, name, err)
	}
}

// ── the manifest (§3.6) ─────────────────────────────────────────────────────

// An unset version is 1; a version this build does not know is refused rather
// than read under the wrong rules.
func TestHooksFromManifest_Version(t *testing.T) {
	h := []Hook{{ID: "g", Event: "pre-create", Run: []string{"x"}}}
	if _, err := hooksFromManifest(packageManifest{Hooks: h}, "p", "/p"); err != nil {
		t.Errorf("an unset version must read as 1: %v", err)
	}
	if _, err := hooksFromManifest(packageManifest{Version: 1, Hooks: h}, "p", "/p"); err != nil {
		t.Errorf("version 1: %v", err)
	}
	_, err := hooksFromManifest(packageManifest{Version: 2, Hooks: h}, "p", "/p")
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("a future manifest version must be refused, got %v", err)
	}
}

func TestHooksFromManifest_IDRules(t *testing.T) {
	cases := []struct {
		name    string
		hooks   []Hook
		errWant string
	}{
		{"missing id", []Hook{{Event: "pre-create", Run: []string{"x"}}}, "id is required"},
		{"id with a colon", []Hook{{ID: "a:b", Event: "pre-create", Run: []string{"x"}}}, "must not contain"},
		{"duplicate ids", []Hook{
			{ID: "g", Event: "pre-create", Run: []string{"x"}},
			{ID: "g", Event: "pre-close", Run: []string{"y"}},
		}, "declared twice"},
	}
	for _, c := range cases {
		_, err := hooksFromManifest(packageManifest{Version: 1, Hooks: c.hooks}, "p", "/p")
		if err == nil || !strings.Contains(err.Error(), c.errWant) {
			t.Errorf("%s: error %v does not contain %q", c.name, err, c.errWant)
		}
	}
}

// ── a config file that a hand edit made unreadable (§3.4) ───────────────────

// `use:\n  - doc-policy` is the obvious thing to write. Failing the decode on it
// failed *every* command, reads included, and took both documented recovery
// paths with it: `list` and `package list`.
func TestPackageRef_ScalarEntryKeepsReadsWorking(t *testing.T) {
	fs := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", fs)
	if err != nil {
		t.Fatal(err)
	}
	raw := "prefix: tst\nuse:\n  - doc-policy\n"
	if err := fs.WriteAtomic("/p/.tasks/config.yaml", []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := s.readConfig()
	if err != nil {
		t.Fatalf("a scalar use entry must not break the read path: %v", err)
	}
	if len(cfg.Use) != 1 || cfg.Use[0].malformed != "doc-policy" {
		t.Fatalf("use = %+v, want the entry carried through as malformed", cfg.Use)
	}
	if _, err := s.All(); err != nil {
		t.Errorf("All() must work: %v", err)
	}

	// The write path is where it surfaces, with a message that shows the fix.
	err = refShape(cfg.Use[0])
	if err == nil || !strings.Contains(err.Error(), "name: doc-policy") {
		t.Errorf("error %v must show the mapping form", err)
	}
}

// ── the merge (§3.5) ────────────────────────────────────────────────────────

func chainStore(t *testing.T) (*Store, vfs.FS) {
	t.Helper()
	fs := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", fs)
	if err != nil {
		t.Fatal(err)
	}
	s.env = env.Fake{Vars: map[string]string{"TASKMGR_HOME": "/hm"}}
	if err := fs.MkdirAll("/hm", 0o755); err != nil {
		t.Fatal(err)
	}
	s.runner = &exec.Fake{Func: func(exec.Spec) exec.Result { return exec.Allow("") }}
	return s, fs
}

// Identity is the package name as well as the resolved directory: effective ids
// are pkg:<name>:<hook>, so only one directory can hold a given name.
//
// The loser is shadowed, not an error — §3.5 rule 3, and the reason it gives is
// this exact pair. Erroring instead wedged every mutation for anyone who
// installed the shipped task-writing package machine-wide *and* vendored it,
// which is the two-step its own README recommends.
func TestPackageChain_DifferentDirectoriesSharingANameShadow(t *testing.T) {
	s, fs := chainStore(t)
	writePackage(t, fs, "/hm", "policy", []Hook{{ID: "g", Event: "pre-create", Run: []string{"g"}}})
	writePackage(t, fs, s.dir, "policy", []Hook{{ID: "s", Event: "pre-create", Run: []string{"s"}}})

	global := GlobalConfig{Use: []PackageRef{{Name: "policy"}}}
	cfg := Config{Prefix: "tst", Use: []PackageRef{{Path: "packages/policy"}}}

	chain, infos, err := packageChain(fs, s.env, s.dir, global, cfg)
	if err != nil {
		t.Fatalf("a repeated package name must shadow, not fail every mutation: %v", err)
	}
	if len(chain.hooks) != 1 || chain.hooks[0].id != "pkg:policy:g" {
		t.Fatalf("chain = %+v, want only the per-user package's hook", chain.hooks)
	}
	if len(infos) != 2 {
		t.Fatalf("Packages() = %d entries, want both listed", len(infos))
	}
	if infos[0].Shadowed {
		t.Error("the entry that won must not be marked shadowed")
	}
	if !infos[1].Shadowed || infos[1].Status != PackageOK {
		t.Errorf("the store entry must be reported shadowed, got %+v", infos[1])
	}
	if !strings.Contains(infos[1].Detail, "/hm/packages/policy") {
		t.Errorf("detail %q must name the directory that won", infos[1].Detail)
	}
}

// A failing entry provides nothing, so a later entry for the same package must
// still get its chance rather than being reported "already provided" by it.
func TestPackageChain_AFailedEntryDoesNotShadowAWorkingOne(t *testing.T) {
	s, fs := chainStore(t)
	writePackage(t, fs, s.dir, "policy", []Hook{{ID: "g", Event: "pre-create", Run: []string{"g"}}})

	cfg := Config{Prefix: "tst", Use: []PackageRef{
		{Path: "gone/policy"},     // not there
		{Path: "packages/policy"}, // the real one
	}}
	hooks, infos, err := packageChain(fs, s.env, s.dir, GlobalConfig{}, cfg)
	if err == nil {
		t.Fatal("the missing entry must still be reported as an error")
	}
	if len(infos) != 2 {
		t.Fatalf("both entries must be listed, got %+v", infos)
	}
	if infos[1].Shadowed {
		t.Error("the working package must not be marked shadowed by one that failed")
	}
	if infos[1].Status != PackageOK || infos[1].Hooks != 1 {
		t.Errorf("the working package must load: %+v", infos[1])
	}
	if len(hooks.hooks) != 1 {
		t.Errorf("the working package must contribute its hook, got %d", len(hooks.hooks))
	}
}

// A failing per-user entry used to return early, dropping every store row from
// the listing — in exactly the state where a reader needs them.
func TestPackageChain_ReportsBothFilesEvenWhenTheFirstFails(t *testing.T) {
	s, fs := chainStore(t)
	writePackage(t, fs, s.dir, "good", []Hook{{ID: "g", Event: "pre-create", Run: []string{"g"}}})

	global := GlobalConfig{Use: []PackageRef{{Name: "ghost"}}} // not installed
	cfg := Config{Prefix: "tst", Use: []PackageRef{{Path: "packages/good"}}}

	_, infos, err := packageChain(fs, s.env, s.dir, global, cfg)
	if err == nil {
		t.Fatal("the missing per-user package must surface an error")
	}
	if len(infos) != 2 {
		t.Fatalf("both files must be reported, got %d rows: %+v", len(infos), infos)
	}
	if infos[0].Status != PackageMissing || infos[0].Scope != scopeGlobal {
		t.Errorf("row 0 = %+v, want the missing per-user entry", infos[0])
	}
	if infos[1].Status != PackageOK || infos[1].Scope != scopeStore {
		t.Errorf("row 1 = %+v, want the store's working package", infos[1])
	}
}

// ErrPackageMissing separates "install this" from "repair this", which is what
// lets `package add` write an entry for a package that is merely not here yet
// while refusing one that is here and unusable.
func TestLoadPackage_MissingIsASentinel(t *testing.T) {
	fs := vfs.NewMem()
	_, err := loadPackage(fs, "/nowhere", "ghost")
	if !errors.Is(err, ErrPackageMissing) {
		t.Fatalf("err = %v, want it to wrap ErrPackageMissing", err)
	}

	if err := fs.MkdirAll("/here", 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = loadPackage(fs, "/here", "here")
	if err == nil || errors.Is(err, ErrPackageMissing) {
		t.Fatalf("a directory with no manifest is broken, not missing: %v", err)
	}
}

// A package whose hooks do not compile must load as an error, or `package list`
// and `hook list` report a store that cannot be written to as healthy.
func TestLoadPackage_CompilesItsHooks(t *testing.T) {
	fs := vfs.NewMem()
	writePackage(t, fs, "/x", "bad", []Hook{{ID: "nope", Event: "not-an-event", Run: []string{"true"}}})

	_, err := loadPackage(fs, "/x/packages/bad", "bad")
	if err == nil || !strings.Contains(err.Error(), "unknown event") {
		t.Fatalf("err = %v, want the unknown event refused at load", err)
	}
}

func TestStorePackages_ReportsAnUnusablePackageAsBroken(t *testing.T) {
	s, fs := chainStore(t)
	writePackage(t, fs, s.dir, "bad", []Hook{{ID: "nope", Event: "not-an-event", Run: []string{"true"}}})
	s.cfg.Use = []PackageRef{{Path: "packages/bad"}}

	infos, err := s.Packages()
	if err != nil {
		t.Fatalf("Packages must not fail on a bad entry: %v", err)
	}
	if len(infos) != 1 || infos[0].Status != PackageBroken {
		t.Fatalf("infos = %+v, want one broken row", infos)
	}
	if infos[0].Hooks != 0 {
		t.Errorf("a broken package contributes no hooks, got %d", infos[0].Hooks)
	}
}

// A hook whose program the package was supposed to ship must fail the load. Left
// unchecked, a typo in a script name showed `ok` in `package list` and in
// `hook list` while every mutation failed to spawn it — the two commands a
// reader reaches for when writes have stopped, both reporting health.
func TestLoadPackage_ChecksAProgramThePackageShips(t *testing.T) {
	fs := vfs.NewMem()
	dir := writePackage(t, fs, "/x", "typo", []Hook{{ID: "gate", Event: "pre-create", Run: []string{"./hooks/gate.sh"}}})
	if err := fs.Remove(dir + "/hooks/gate.sh"); err != nil {
		t.Fatalf("remove the script the fixture wrote: %v", err)
	}

	_, err := loadPackage(fs, dir, "typo")
	if err == nil || !strings.Contains(err.Error(), "is not there") {
		t.Fatalf("err = %v, want the missing script refused at load", err)
	}

	// A PATH lookup and an absolute path name what the machine provides, so a
	// machine's PATH never decides whether a package loads.
	ok := writePackage(t, fs, "/x", "shell", []Hook{
		{ID: "a", Event: "pre-create", Run: []string{"sh", "-c", "exit 0"}},
		{ID: "b", Event: "pre-create", Run: []string{"/usr/bin/false"}},
	})
	if _, err := loadPackage(fs, ok, "shell"); err != nil {
		t.Errorf("a PATH or absolute program must not be stat-ed: %v", err)
	}
}

// CLI-SPEC §2.3 promises `package add` refuses a package that could never run.
// Resolving the candidate against a fresh map put every cross-entry rule out of
// reach, so a clash passed the check and detonated at the next mutation.
func TestInspectPackage_SeesTheEntriesThatAlreadyApply(t *testing.T) {
	s, fs := chainStore(t)
	writePackage(t, fs, "/hm", "policy", []Hook{{ID: "g", Event: "pre-create", Run: []string{"g"}}})
	writePackage(t, fs, s.dir, "policy", []Hook{{ID: "s", Event: "pre-create", Run: []string{"s"}}})
	if err := fs.WriteAtomic("/hm/config.yaml", []byte("version: 1\nuse:\n    - name: policy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := s.InspectPackage(PackageRef{Path: "packages/policy"})
	if !got.Shadowed {
		t.Fatalf("inspect = %+v, want the entry reported shadowed before it is written", got)
	}
	if got.Status != PackageOK {
		t.Errorf("status = %q, want a shadowed entry to still load", got.Status)
	}
}

// `hook list` is the diagnostic for "what gates this store". Returning the load
// error emptied it in exactly the state it exists to explain.
func TestHookChain_ListsWhatLoadedWhenAnEntryIsBroken(t *testing.T) {
	s, fs := chainStore(t)
	writePackage(t, fs, s.dir, "good", []Hook{{ID: "g", Event: "pre-create", Run: []string{"g"}}})
	s.cfg.Use = []PackageRef{{Path: "packages/good"}, {Name: "not-installed"}}

	chain, err := s.HookChain()
	if err != nil {
		t.Fatalf("HookChain must not fail on an entry that will not load: %v", err)
	}
	if len(chain) != 1 || chain[0].ID != "pkg:good:g" {
		t.Fatalf("chain = %+v, want the hook that did load", chain)
	}

	// The trouble is still reported — by the listing that owns it — and the
	// write path still fails closed.
	infos, _ := s.Packages()
	if len(infos) != 2 || infos[1].Status != PackageMissing {
		t.Errorf("package list must report the missing entry: %+v", infos)
	}
	if _, err := s.Create(CreateInput{Title: "x"}); !errors.Is(err, ErrPackageMissing) {
		t.Errorf("create = %v, want the mutation to fail closed on the missing package", err)
	}
}
