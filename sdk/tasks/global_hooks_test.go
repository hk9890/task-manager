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

// global_hooks_test.go — the hook chain a store actually runs: packages named by
// the per-user config, then packages named by the store's own (HOOK-SPEC §3.5),
// plus the public config API (Store.Config/SetConfig, LoadGlobalConfig).
//
// L2 on vfs.Mem throughout: the chain is read from files, so the seam is the
// subject rather than something to work around.
package tasks

import (
	"errors"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// globalHookStore builds a Mem-backed store whose taskmgr home is /hm, with the
// given per-user config.yaml body already in place.
func globalHookStore(t *testing.T, globalYAML string) (*Store, *exec.Fake) {
	t.Helper()
	fs := vfs.NewMem()
	fake := &exec.Fake{Func: func(exec.Spec) exec.Result { return exec.Allow("") }}
	s, err := InitWithVFS("/", "x", fs, withRunner(fake))
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}
	s.env = env.Fake{Vars: map[string]string{"TASKMGR_HOME": "/hm"}}
	if err := fs.MkdirAll("/hm", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if globalYAML != "" {
		if err := fs.WriteAtomic("/hm/config.yaml", []byte(globalYAML), 0o644); err != nil {
			t.Fatalf("write global config: %v", err)
		}
	}
	return s, fake
}

// ── the chain and its order ──────────────────────────────────────────────────

// The machine-wide packages are evaluated first, so their denial is the one that
// surfaces when both would deny (HOOK-SPEC §3.5 rule 1).
func TestPackageChain_GlobalPackagesRunBeforeStorePackages(t *testing.T) {
	s, _ := globalHookStore(t, "version: 1\nuse:\n    - name: machine\n")
	writePackage(t, s.fs, "/hm", "machine", []Hook{
		{ID: "g1", Event: "pre-create", Run: []string{"g1"}},
		{ID: "g2", Event: "pre-create", Run: []string{"g2"}},
	})
	storePackage(t, s, "project", []Hook{
		{ID: "s1", Event: "pre-create", Run: []string{"s1"}},
	})

	hs, err := s.hooks()
	if err != nil {
		t.Fatalf("hooks(): %v", err)
	}
	var got []string
	for _, h := range hs.forEvent("pre-create") {
		got = append(got, h.id)
	}
	want := []string{"pkg:machine:g1", "pkg:machine:g2", "pkg:project:s1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("chain = %v, want %v", got, want)
	}
}

// The same package named in both files contributes once, from the file that
// named it first. Erroring instead would let one person's machine-wide package
// break every colleague's repository (HOOK-SPEC §3.5).
func TestPackageChain_SamePackageInBothFilesRunsOnce(t *testing.T) {
	s, _ := globalHookStore(t, "version: 1\nuse:\n    - name: shared\n")
	writePackage(t, s.fs, "/hm", "shared", []Hook{
		{ID: "gate", Event: "pre-create", Run: []string{"gate"}},
	})
	// The store names the very same directory by path.
	useRef(s, PackageRef{Path: "/hm/packages/shared"})

	hs, err := s.hooks()
	if err != nil {
		t.Fatalf("hooks(): %v", err)
	}
	pre := hs.forEvent("pre-create")
	if len(pre) != 1 || pre[0].id != "pkg:shared:gate" {
		t.Fatalf("chain = %v, want one pkg:shared:gate", pre)
	}

	infos, err := s.Packages()
	if err != nil {
		t.Fatalf("Packages(): %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("Packages() = %d entries, want both listed", len(infos))
	}
	if infos[0].Shadowed {
		t.Error("the first entry must not be marked shadowed")
	}
	if !infos[1].Shadowed {
		t.Error("the second entry must be reported as shadowed, not silently dropped")
	}
}

func TestStoreHooks_InheritsGlobalPackages(t *testing.T) {
	s, fake := globalHookStore(t, "version: 1\nuse:\n    - name: docs\n")
	writePackage(t, s.fs, "/hm", "docs", []Hook{
		{ID: "doc-needs-path", Event: "pre-create", When: `type == "doc"`, Run: []string{"/bin/false"}},
	})
	fake.Func = func(exec.Spec) exec.Result { return exec.Deny(1, "a doc needs a path label") }

	if _, err := s.Create(CreateInput{Title: "a doc", Type: TypeDoc}); err == nil {
		t.Fatal("create of a doc: want the machine-wide hook to deny, got success")
	} else if !strings.Contains(err.Error(), "pkg:docs:doc-needs-path") {
		t.Errorf("denial %q does not name the hook by its effective id", err)
	}

	// The `when` scopes it: ordinary work is untouched by the same hook.
	if _, err := s.Create(CreateInput{Title: "work", Type: TypeTask}); err != nil {
		t.Errorf("create of a task: %v", err)
	}
}

// A relative argv[0] in a package is found inside that package, wherever it was
// installed, while the working directory stays the project root (HOOK-SPEC §3.6).
func TestStoreHooks_PackageArgvResolvesInsideThePackage(t *testing.T) {
	s, fake := globalHookStore(t, "version: 1\nuse:\n    - name: docs\n")
	writePackage(t, s.fs, "/hm", "docs", []Hook{
		{ID: "gate", Event: "pre-create", Run: []string{"./hooks/check.sh", "--strict"}},
	})
	var spec exec.Spec
	fake.Func = func(sp exec.Spec) exec.Result { spec = sp; return exec.Allow("") }

	if _, err := s.Create(CreateInput{Title: "x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if spec.Argv[0] != "/hm/packages/docs/hooks/check.sh" {
		t.Errorf("argv[0] = %q, want it resolved inside the package", spec.Argv[0])
	}
	if spec.Argv[1] != "--strict" {
		t.Errorf("argv[1] = %q, want it passed through verbatim", spec.Argv[1])
	}
	if spec.Dir != s.root {
		t.Errorf("cwd = %q, want the project root %q", spec.Dir, s.root)
	}
}

// A first element with no path separator is a PATH lookup and must be left
// alone, or the documented ["sh", "-c", …] idiom would search the package.
func TestStoreHooks_PathLookupArgvIsNotRewritten(t *testing.T) {
	s, fake := globalHookStore(t, "version: 1\nuse:\n    - name: docs\n")
	writePackage(t, s.fs, "/hm", "docs", []Hook{
		{ID: "gate", Event: "pre-create", Run: []string{"sh", "-c", "true"}},
	})
	var spec exec.Spec
	fake.Func = func(sp exec.Spec) exec.Result { spec = sp; return exec.Allow("") }

	if _, err := s.Create(CreateInput{Title: "x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if spec.Argv[0] != "sh" {
		t.Errorf("argv[0] = %q, want the PATH lookup left alone", spec.Argv[0])
	}
}

// TestStoreHooks_ReadsDoNotTouchTheGlobalConfig is the property that keeps the
// per-user config off the read path (CONFIG-SPEC §4): a global config so broken
// that it cannot be parsed must still leave every query working.
func TestStoreHooks_ReadsDoNotTouchTheGlobalConfig(t *testing.T) {
	s, _ := globalHookStore(t, "\t not: [valid: yaml\n")

	if _, err := s.All(); err != nil {
		t.Errorf("All() must not read the global config: %v", err)
	}
	if _, err := s.Ready(); err != nil {
		t.Errorf("Ready() must not read the global config: %v", err)
	}
	if _, err := s.List(Filter{Expr: `type == "task"`}); err != nil {
		t.Errorf("Query() must not read the global config: %v", err)
	}
	if _, err := s.Create(CreateInput{Title: "x"}); err == nil {
		t.Error("a write must fail closed on an unparseable global config")
	}
}

func TestStoreHooks_AbsentHomeIsNotAnError(t *testing.T) {
	s, _ := globalHookStore(t, "")
	s.env = env.Fake{} // no TASKMGR_HOME and no $HOME at all

	if _, err := s.Create(CreateInput{Title: "x"}); err != nil {
		t.Errorf("a machine with no home has nothing to inherit: %v", err)
	}
}

// ── the public config API ────────────────────────────────────────────────────

func TestStoreConfig_ReturnsACopy(t *testing.T) {
	s, _ := globalHookStore(t, "")
	s.cfg.Use = []PackageRef{{Name: "a"}}

	got := s.Config()
	got.Use[0].Name = "mutated"
	got.HookTimeout = "9s"

	if s.cfg.Use[0].Name != "a" {
		t.Error("Config() aliased the Use slice; editing the copy changed the store")
	}
	if s.cfg.HookTimeout != "" {
		t.Error("Config() returned a reference, not a copy")
	}
}

func TestSetConfig_RejectsAPrefixChange(t *testing.T) {
	s, _ := globalHookStore(t, "")

	var ve *ValidationError
	err := s.SetConfig(Config{Prefix: "other"})
	if !errors.As(err, &ve) || ve.Field != "prefix" {
		t.Fatalf("err = %v, want a ValidationError on prefix", err)
	}
	if err := s.SetConfig(Config{Prefix: ""}); err == nil {
		t.Error("empty prefix: want an error, got none")
	}
	if s.cfg.Prefix != "x" {
		t.Errorf("prefix = %q after two refusals, want it untouched", s.cfg.Prefix)
	}
}

// A `use:` entry the write introduces is checked before a byte lands, so a
// reference that could never resolve is refused here (HOOK-SPEC §3.4).
func TestSetConfig_RejectsAMalformedUseEntryAndWritesNothing(t *testing.T) {
	s, _ := globalHookStore(t, "")

	err := s.SetConfig(Config{Prefix: "x", Use: []PackageRef{{Name: "../escape"}}})
	if err == nil {
		t.Fatal("an invalid package name: want an error, got none")
	}
	cfg, readErr := s.readConfig()
	if readErr != nil {
		t.Fatalf("readConfig: %v", readErr)
	}
	if len(cfg.Use) != 0 {
		t.Errorf("config.yaml gained %d use entries from a refused write", len(cfg.Use))
	}
}

// TestSetConfig_TakesEffectOnTheSameHandle covers the hookBuilt reset: the
// compiled set is built once per Store, so without invalidation a long-lived
// handle would keep running the hooks it opened with.
func TestSetConfig_TakesEffectOnTheSameHandle(t *testing.T) {
	s, fake := globalHookStore(t, "")
	writePackage(t, s.fs, s.dir, "gatepkg", []Hook{
		{ID: "gate", Event: "pre-create", Run: []string{"/bin/false"}},
	})

	if _, err := s.Create(CreateInput{Title: "before"}); err != nil {
		t.Fatalf("create before: %v", err) // compiles the empty hook set
	}
	fake.Func = func(exec.Spec) exec.Result { return exec.Deny(1, "no new issues") }

	if err := s.SetConfig(Config{Prefix: "x", Use: []PackageRef{{Path: "packages/gatepkg"}}}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if _, err := s.Create(CreateInput{Title: "after"}); err == nil {
		t.Error("the package written by SetConfig did not take effect on this handle")
	}
}

func TestSetConfig_PersistsHookTimeout(t *testing.T) {
	s, _ := globalHookStore(t, "")

	if err := s.SetConfig(Config{Prefix: "x", HookTimeout: "5m"}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	cfg, err := s.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if cfg.HookTimeout != "5m" {
		t.Errorf("hook_timeout on disk = %q, want 5m", cfg.HookTimeout)
	}
	if s.Config().HookTimeout != "5m" {
		t.Errorf("hook_timeout in memory = %q, want 5m", s.Config().HookTimeout)
	}
}

// HookChain is the authoritative reading of both files plus the manifests they
// name — the answer neither config file gives on its own.
func TestStoreHookChain_ReportsOrderAndScope(t *testing.T) {
	s, _ := globalHookStore(t, "version: 1\nuse:\n    - name: machine\n")
	writePackage(t, s.fs, "/hm", "machine", []Hook{
		{ID: "g", Event: "pre-create", Run: []string{"g"}},
	})
	storePackage(t, s, "project", []Hook{
		{ID: "s", Event: "pre-close", When: `type == "feature"`, Run: []string{"s"}},
	})

	chain, err := s.HookChain()
	if err != nil {
		t.Fatalf("HookChain: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("chain has %d hooks, want 2", len(chain))
	}
	if chain[0].ID != "pkg:machine:g" || chain[0].Scope != scopeGlobal {
		t.Errorf("chain[0] = %+v, want the machine-wide hook first", chain[0])
	}
	if chain[1].ID != "pkg:project:s" || chain[1].Scope != scopeStore || chain[1].When == "" {
		t.Errorf("chain[1] = %+v, want the store's hook second with its when", chain[1])
	}
}
