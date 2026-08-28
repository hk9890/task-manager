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

// pkg_test.go — `taskmgr package` and `taskmgr hook list`, in-process through
// cmd.Run (CLI-SPEC §2.3).
package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCmdPackage writes a package directory under dir and returns its path.
// hooksYAML is the manifest body below `version: 1`.
func writeCmdPackage(t *testing.T, dir, name, hooksYAML string) string {
	t.Helper()
	pkg := filepath.Join(dir, "packages", name)
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatalf("mkdir package: %v", err)
	}
	body := "version: 1\n" + hooksYAML
	if err := os.WriteFile(filepath.Join(pkg, "taskmgr-package.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return pkg
}

// storeDataDir is the directory a store's own config.yaml and packages live in.
func storeDataDir(root string) string { return filepath.Join(root, ".tasks") }

func TestPackageAdd_ByNameAndList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TASKMGR_HOME", home)
	writeCmdPackage(t, home, "doc-policy", `hooks:
  - id: gate
    event: pre-create
    run: ["/bin/true"]
`)
	root := newStore(t)

	if _, errOut, code := run(t, "--dir", root, "package", "add", "doc-policy"); code != 0 {
		t.Fatalf("package add: exit %d, stderr %q", code, errOut)
	}

	out, _, code := run(t, "--dir", root, "--json", "package", "list")
	if code != 0 {
		t.Fatalf("package list: exit %d", code)
	}
	var pkgs []struct {
		Name   string `json:"name"`
		Scope  string `json:"scope"`
		Status string `json:"status"`
		Hooks  int    `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(out), &pkgs); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(pkgs) != 1 {
		t.Fatalf("listed %d packages, want 1:\n%s", len(pkgs), out)
	}
	if pkgs[0].Name != "doc-policy" || pkgs[0].Scope != "store" || pkgs[0].Status != "ok" || pkgs[0].Hooks != 1 {
		t.Errorf("package = %+v, want doc-policy/store/ok with 1 hook", pkgs[0])
	}
}

// A name resolves under the taskmgr home, a path against the config file's own
// directory. Both work, and the listing shows where each one landed.
func TestPackageAdd_ByPathResolvesAgainstTheStore(t *testing.T) {
	root := newStore(t)
	writeCmdPackage(t, storeDataDir(root), "repo-policy", `hooks:
  - id: gate
    event: pre-create
    run: ["/bin/true"]
`)

	if _, errOut, code := run(t, "--dir", root, "package", "add", "--path", "packages/repo-policy"); code != 0 {
		t.Fatalf("package add --path: exit %d, stderr %q", code, errOut)
	}
	out, _, _ := run(t, "--dir", root, "package", "list")
	if !strings.Contains(out, "repo-policy") || !strings.Contains(out, "ok") {
		t.Errorf("package list = %q, want the path entry reported ok", out)
	}
}

// A `use:` entry can be written before the package is installed — a store config
// travels in git, so it legitimately names a package this machine lacks. The
// listing reports it, and the write warns rather than pretending it is fine.
func TestPackageAdd_MissingPackageIsWarnedAndListedMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TASKMGR_HOME", home)
	// An installed package listed first, so the warning cannot come from reading
	// some other entry's status.
	writeCmdPackage(t, home, "installed", `hooks:
  - id: gate
    event: pre-create
    run: ["/bin/true"]
`)
	root := newStore(t)
	if _, _, code := run(t, "--dir", root, "package", "add", "installed"); code != 0 {
		t.Fatal("setup: package add installed")
	}

	out, _, code := run(t, "--dir", root, "package", "add", "not-installed")
	if code != 0 {
		t.Fatalf("package add: exit %d", code)
	}
	if !strings.Contains(out, "missing") {
		t.Errorf("stdout = %q, want a warning that the package is missing", out)
	}

	out, _, _ = run(t, "--dir", root, "package", "list")
	if !strings.Contains(out, "missing") {
		t.Errorf("package list = %q, want the entry reported missing", out)
	}
}

func TestPackageAdd_RefusesAnInvalidNameAndWritesNothing(t *testing.T) {
	isolatedHome(t)
	root := newStore(t)

	_, errOut, code := run(t, "--dir", root, "package", "add", "../escape")
	if code == 0 {
		t.Fatal("an invalid package name must be refused")
	}
	if !strings.Contains(errOut, "../escape") {
		t.Errorf("stderr = %q, want it to name the offending entry", errOut)
	}
	out, _, _ := run(t, "--dir", root, "package", "list")
	if !strings.Contains(out, "no packages configured") {
		t.Errorf("package list = %q, want the refused entry not to have been written", out)
	}
}

func TestPackageAdd_RefusesADuplicate(t *testing.T) {
	isolatedHome(t)
	root := newStore(t)

	if _, _, code := run(t, "--dir", root, "package", "add", "p"); code != 0 {
		t.Fatal("setup: package add")
	}
	_, errOut, code := run(t, "--dir", root, "package", "add", "p")
	if code == 0 {
		t.Fatal("adding the same package twice must be refused")
	}
	if !strings.Contains(errOut, "already") {
		t.Errorf("stderr = %q, want it to say the entry is already there", errOut)
	}
}

// `hook list` is the authoritative reading of both files plus the manifests they
// name: the machine-wide package first, then the store's (HOOK-SPEC §3.5).
func TestHookList_ShowsTheEffectiveChainInOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TASKMGR_HOME", home)
	writeCmdPackage(t, home, "machine", `hooks:
  - id: g
    event: pre-create
    run: ["/bin/true"]
`)
	root := newStore(t)
	writeCmdPackage(t, storeDataDir(root), "project", `hooks:
  - id: s
    event: pre-close
    when: 'type == "feature"'
    run: ["/bin/true"]
`)

	if _, errOut, code := run(t, "package", "add", "--global", "machine"); code != 0 {
		t.Fatalf("package add --global: exit %d, stderr %q", code, errOut)
	}
	if _, errOut, code := run(t, "--dir", root, "package", "add", "--path", "packages/project"); code != 0 {
		t.Fatalf("package add --path: exit %d, stderr %q", code, errOut)
	}

	out, _, code := run(t, "--dir", root, "--json", "hook", "list")
	if code != 0 {
		t.Fatalf("hook list: exit %d", code)
	}
	var hooks []struct {
		ID      string   `json:"id"`
		Event   string   `json:"event"`
		When    string   `json:"when"`
		Run     []string `json:"run"`
		Package string   `json:"package"`
		Scope   string   `json:"scope"`
	}
	if err := json.Unmarshal([]byte(out), &hooks); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(hooks) != 2 {
		t.Fatalf("chain has %d hooks, want 2:\n%s", len(hooks), out)
	}
	if hooks[0].ID != "pkg:machine:g" || hooks[0].Scope != "global" {
		t.Errorf("hooks[0] = %+v, want the machine-wide hook first", hooks[0])
	}
	if hooks[1].ID != "pkg:project:s" || hooks[1].Scope != "store" || hooks[1].When == "" {
		t.Errorf("hooks[1] = %+v, want the store's hook second with its when", hooks[1])
	}
}

func TestHookList_EmptyChainSaysSo(t *testing.T) {
	isolatedHome(t)
	root := newStore(t)

	out, _, code := run(t, "--dir", root, "hook", "list")
	if code != 0 {
		t.Fatalf("hook list: exit %d", code)
	}
	if !strings.Contains(out, "no hooks") {
		t.Errorf("hook list = %q, want it to say nothing gates this store", out)
	}
}

// The same package named in both files contributes once, and the listing marks
// the second entry rather than dropping it silently (HOOK-SPEC §3.5).
func TestPackageList_MarksAShadowedEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TASKMGR_HOME", home)
	pkg := writeCmdPackage(t, home, "shared", `hooks:
  - id: g
    event: pre-create
    run: ["/bin/true"]
`)
	root := newStore(t)

	if _, _, code := run(t, "package", "add", "--global", "shared"); code != 0 {
		t.Fatal("setup: package add --global")
	}
	if _, _, code := run(t, "--dir", root, "package", "add", "--path", pkg); code != 0 {
		t.Fatal("setup: package add --path")
	}

	out, _, _ := run(t, "--dir", root, "package", "list")
	if !strings.Contains(out, "shadowed") {
		t.Errorf("package list = %q, want the duplicate marked shadowed", out)
	}

	chain, _, _ := run(t, "--dir", root, "--json", "hook", "list")
	if strings.Count(chain, "pkg:shared:g") != 1 {
		t.Errorf("hook list = %q, want the shadowed package's hook to run once", chain)
	}
}

// ── what the review reproduced ──────────────────────────────────────────────

// CLI-SPEC §2.3 says the package is "loaded and checked before the entry is
// written". It was written first, so a package that wedges every mutation was
// added with exit 0 and no warning — and, from a store config, travelled to
// every colleague in git.
func TestPackageAdd_RefusesAnUnusablePackageBeforeWriting(t *testing.T) {
	isolatedHome(t)
	root := newStore(t)
	writeCmdPackage(t, storeDataDir(root), "bad", `hooks:
  - id: nope
    event: not-an-event
    run: ["true"]
`)

	_, errOut, code := run(t, "--dir", root, "package", "add", "--path", "packages/bad")
	if code == 0 {
		t.Fatal("a package whose hooks do not compile must be refused")
	}
	if !strings.Contains(errOut, "not-an-event") {
		t.Errorf("stderr = %q, want the reason", errOut)
	}
	out, _, _ := run(t, "--dir", root, "package", "list")
	if !strings.Contains(out, "no packages configured") {
		t.Errorf("package list = %q, want nothing written", out)
	}
	// The store is still writable, which is the point of refusing.
	if _, _, code := run(t, "--dir", root, "create", "--title", "x"); code != 0 {
		t.Errorf("create must still work, exit %d", code)
	}
}

// A relative path entry promises the package travels with the file. One that
// reaches outside breaks for every colleague the moment the file is committed.
func TestPackageAdd_RefusesAPathThatLeavesTheStore(t *testing.T) {
	isolatedHome(t)
	root := newStore(t)

	_, errOut, code := run(t, "--dir", root, "package", "add", "--path", "../../outside")
	if code == 0 {
		t.Fatal("a relative path leaving the config directory must be refused")
	}
	if !strings.Contains(errOut, "leaves the directory") {
		t.Errorf("stderr = %q, want it to say why", errOut)
	}
}

// `package list` and `hook list` are what a reader reaches for when writes have
// stopped, so they must not report a wedged store as healthy.
func TestPackageList_ReportsAnUnusablePackageBroken(t *testing.T) {
	isolatedHome(t)
	root := newStore(t)
	writeCmdPackage(t, storeDataDir(root), "bad", `hooks:
  - id: nope
    event: not-an-event
    run: ["true"]
`)
	// Written by hand, the way a bad entry actually arrives: in git, from a
	// machine where it was fine, or from an edit.
	cfg := filepath.Join(storeDataDir(root), "config.yaml")
	if err := os.WriteFile(cfg, []byte("prefix: tst\nuse:\n    - path: packages/bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, code := run(t, "--dir", root, "package", "list")
	if code != 0 {
		t.Fatalf("package list must work when writes do not: exit %d", code)
	}
	if !strings.Contains(out, "broken") || !strings.Contains(out, "not-an-event") {
		t.Errorf("package list = %q, want it broken and the reason shown", out)
	}
	if _, _, code := run(t, "--dir", root, "create", "--title", "x"); code == 0 {
		t.Error("the bad package must still fail the write")
	}
}

// A failing per-user entry used to truncate the listing, dropping every store
// row — in exactly the state where the reader needs them.
func TestPackageList_ShowsStoreRowsWhenAGlobalEntryFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TASKMGR_HOME", home)
	root := newStore(t)
	writeCmdPackage(t, storeDataDir(root), "good", `hooks:
  - id: g
    event: pre-create
    run: ["/bin/true"]
`)
	if _, _, code := run(t, "--dir", root, "package", "add", "--path", "packages/good"); code != 0 {
		t.Fatal("setup: package add")
	}
	// A per-user entry naming a package nobody installed.
	gcfg := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(gcfg, []byte("version: 1\nuse:\n    - name: ghost\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, code := run(t, "--dir", root, "package", "list")
	if code != 0 {
		t.Fatalf("package list: exit %d", code)
	}
	if !strings.Contains(out, "ghost") {
		t.Errorf("package list = %q, want the missing per-user entry", out)
	}
	if !strings.Contains(out, "good") {
		t.Errorf("package list = %q, want the store row kept despite the failing global one", out)
	}
}

// Several spellings of one directory are one package. Writing them all left
// entries that contribute nothing, with no verb to remove them.
func TestPackageAdd_RefusesTheSameDirectorySpeltDifferently(t *testing.T) {
	isolatedHome(t)
	root := newStore(t)
	writeCmdPackage(t, storeDataDir(root), "p", `hooks:
  - id: g
    event: pre-create
    run: ["/bin/true"]
`)
	if _, _, code := run(t, "--dir", root, "package", "add", "--path", "packages/p"); code != 0 {
		t.Fatal("setup: package add")
	}
	for _, spelling := range []string{"./packages/p", "packages/p/"} {
		_, errOut, code := run(t, "--dir", root, "package", "add", "--path", spelling)
		if code == 0 {
			t.Errorf("%q names the same directory and must be refused", spelling)
		} else if !strings.Contains(errOut, "already") {
			t.Errorf("%q: stderr = %q", spelling, errOut)
		}
	}
}

// A hand-edited `- doc-policy` is the obvious thing to write. Failing the decode
// on it broke every command, including both documented recovery paths.
func TestPackageList_ScalarUseEntryKeepsReadsWorking(t *testing.T) {
	isolatedHome(t)
	root := newStore(t)
	cfg := filepath.Join(storeDataDir(root), "config.yaml")
	if err := os.WriteFile(cfg, []byte("prefix: tst\nuse:\n  - doc-policy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, errOut, code := run(t, "--dir", root, "list"); code != 0 {
		t.Errorf("list must still work: exit %d, stderr %q", code, errOut)
	}
	out, _, code := run(t, "--dir", root, "package", "list")
	if code != 0 {
		t.Fatalf("package list must still work: exit %d", code)
	}
	if !strings.Contains(out, "broken") || !strings.Contains(out, "name: doc-policy") {
		t.Errorf("package list = %q, want it broken and the mapping form shown", out)
	}
}
