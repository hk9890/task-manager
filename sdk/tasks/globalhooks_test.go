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

// globalhooks_test.go — hooks inherited from the per-user config (HOOK-SPEC
// §3.5) and the public config API (Store.Config/SetConfig, LoadGlobalConfig).
//
// L1 for the merge rules, which are pure; L2 on vfs.Mem for the parts that read
// or write a file.
package tasks

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// ── L1: merge rules ──────────────────────────────────────────────────────────

// TestBuildHookSet_GlobalHooksRunBeforeStoreHooks pins the order decision: the
// machine-wide gate is evaluated first, so its denial is the one that surfaces.
func TestBuildHookSet_GlobalHooksRunBeforeStoreHooks(t *testing.T) {
	hs, err := buildHookSet(
		GlobalConfig{Hooks: []Hook{
			{ID: "g1", Event: "pre-create", Run: []string{"g1"}},
			{ID: "g2", Event: "pre-create", Run: []string{"g2"}},
		}},
		Config{Prefix: "x", Hooks: []Hook{
			{ID: "s1", Event: "pre-create", Run: []string{"s1"}},
		}},
	)
	if err != nil {
		t.Fatalf("buildHookSet: %v", err)
	}
	var got []string
	for _, h := range hs.forEvent("pre-create") {
		got = append(got, h.id)
	}
	want := []string{"global:g1", "global:g2", "s1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("chain = %v, want %v", got, want)
	}
}

// TestBuildHookSet_GlobalIDsCarryTheScopePrefix covers both id forms: without it
// a defaulted "pre-create#0" from each file would be indistinguishable.
func TestBuildHookSet_GlobalIDsCarryTheScopePrefix(t *testing.T) {
	hs, err := buildHookSet(
		GlobalConfig{Hooks: []Hook{
			{ID: "named", Event: "pre-create", Run: []string{"x"}},
			{Event: "pre-create", Run: []string{"y"}},
		}},
		Config{Prefix: "x", Hooks: []Hook{
			{Event: "pre-create", Run: []string{"z"}},
		}},
	)
	if err != nil {
		t.Fatalf("buildHookSet: %v", err)
	}
	want := []string{"global:named", "global:pre-create#1", "pre-create#0"}
	for i, w := range want {
		if hs.hooks[i].id != w {
			t.Errorf("hook[%d].id = %q, want %q", i, hs.hooks[i].id, w)
		}
	}
}

func TestBuildHookSet_TimeoutPrecedence(t *testing.T) {
	cases := []struct {
		name         string
		global, cfg  string
		want         time.Duration
		wantErrPiece string
	}{
		{name: "neither set", want: defaultHookTimeout},
		{name: "global only", global: "5m", want: 5 * time.Minute},
		{name: "store only", cfg: "10s", want: 10 * time.Second},
		{name: "store wins over global", global: "5m", cfg: "10s", want: 10 * time.Second},
		{name: "global is validated", global: "nonsense", wantErrPiece: `invalid hook_timeout "nonsense"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hs, err := buildHookSet(GlobalConfig{HookTimeout: c.global}, Config{Prefix: "x", HookTimeout: c.cfg})
			if c.wantErrPiece != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErrPiece) {
					t.Fatalf("error = %v, want it to contain %q", err, c.wantErrPiece)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildHookSet: %v", err)
			}
			if hs.timeout != c.want {
				t.Errorf("timeout = %v, want %v", hs.timeout, c.want)
			}
		})
	}
}

// TestBuildHookSet_MalformedGlobalHookIsAConfigError checks the error names the
// file to fix — the whole point of the scope prefix.
func TestBuildHookSet_MalformedGlobalHookIsAConfigError(t *testing.T) {
	_, err := buildHookSet(
		GlobalConfig{Hooks: []Hook{{Event: "pre-delete", Run: []string{"x"}}}},
		Config{Prefix: "x"},
	)
	if err == nil {
		t.Fatal("unknown global event: want error, got none")
	}
	if !strings.Contains(err.Error(), "global:pre-delete#0") {
		t.Errorf("error %q does not name the global hook", err)
	}
}

func TestHookID(t *testing.T) {
	cases := []struct {
		hook   Hook
		index  int
		global bool
		want   string
	}{
		{Hook{ID: "gate", Event: "pre-close"}, 0, false, "gate"},
		{Hook{Event: "pre-close"}, 3, false, "pre-close#3"},
		{Hook{ID: "gate", Event: "pre-close"}, 0, true, "global:gate"},
		{Hook{Event: "pre-close"}, 2, true, "global:pre-close#2"},
	}
	for _, c := range cases {
		if got := HookID(c.hook, c.index, c.global); got != c.want {
			t.Errorf("HookID(%+v, %d, %v) = %q, want %q", c.hook, c.index, c.global, got, c.want)
		}
	}
}

// ── L2: reading the per-user config through the seams ────────────────────────

// globalHookStore builds a Mem-backed store whose taskmgr home is /hm, with the
// given per-user config.yaml body already in place.
func globalHookStore(t *testing.T, globalYAML string) (*Store, *exec.Fake) {
	t.Helper()
	fs := vfs.NewMem()
	s, err := InitWithVFS("/", "x", fs)
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}
	s.env = env.Fake{Vars: map[string]string{"TASKMGR_HOME": "/hm"}}
	if globalYAML != "" {
		if err := fs.MkdirAll("/hm", 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := fs.WriteAtomic("/hm/config.yaml", []byte(globalYAML), 0o644); err != nil {
			t.Fatalf("write global config: %v", err)
		}
	}
	fake := &exec.Fake{Func: func(exec.Spec) exec.Result { return exec.Allow("") }}
	s.runner = fake
	return s, fake
}

func TestStoreHooks_InheritsGlobalHooks(t *testing.T) {
	s, fake := globalHookStore(t, `
version: 1
hooks:
  - id: doc-needs-path
    event: pre-create
    when: 'type == "doc"'
    run: ["/bin/false"]
`)
	fake.Func = func(exec.Spec) exec.Result { return exec.Deny(1, "a doc needs a path label") }

	if _, err := s.Create(CreateInput{Title: "a doc", Type: TypeDoc}); err == nil {
		t.Fatal("create of a doc: want the global hook to deny, got success")
	} else if !strings.Contains(err.Error(), "global:doc-needs-path") {
		t.Errorf("denial %q does not name the global hook", err)
	}

	// The `when` scopes it: ordinary work is untouched by the same hook.
	if _, err := s.Create(CreateInput{Title: "work", Type: TypeTask}); err != nil {
		t.Errorf("create of a task: %v", err)
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
	if _, err := s.Query(`type == "task"`); err != nil {
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

// ── L2: the public config API ────────────────────────────────────────────────

func TestStoreConfig_ReturnsACopy(t *testing.T) {
	s, _ := globalHookStore(t, "")
	s.cfg.Hooks = []Hook{{ID: "a", Event: "pre-create", Run: []string{"x"}}}

	got := s.Config()
	got.Hooks[0].ID = "mutated"
	got.HookTimeout = "9s"

	if s.cfg.Hooks[0].ID != "a" {
		t.Error("Config() aliased the Hooks slice; editing the copy changed the store")
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

func TestSetConfig_RejectsAMalformedHookAndWritesNothing(t *testing.T) {
	s, _ := globalHookStore(t, "")

	err := s.SetConfig(Config{Prefix: "x", Hooks: []Hook{{Event: "pre-delete", Run: []string{"x"}}}})
	if err == nil {
		t.Fatal("unknown event: want an error, got none")
	}
	cfg, readErr := s.readConfig()
	if readErr != nil {
		t.Fatalf("readConfig: %v", readErr)
	}
	if len(cfg.Hooks) != 0 {
		t.Errorf("config.yaml gained %d hooks from a refused write", len(cfg.Hooks))
	}
}

// TestSetConfig_TakesEffectOnTheSameHandle covers the hookOnce reset: the
// compiled set is built once per Store, so without invalidation a long-lived
// handle would keep running the hooks it opened with.
func TestSetConfig_TakesEffectOnTheSameHandle(t *testing.T) {
	s, fake := globalHookStore(t, "")

	if _, err := s.Create(CreateInput{Title: "before"}); err != nil {
		t.Fatalf("create before: %v", err) // compiles the empty hook set
	}
	fake.Func = func(exec.Spec) exec.Result { return exec.Deny(1, "no new issues") }

	if err := s.SetConfig(Config{Prefix: "x", Hooks: []Hook{
		{ID: "gate", Event: "pre-create", Run: []string{"/bin/false"}},
	}}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if _, err := s.Create(CreateInput{Title: "after"}); err == nil {
		t.Error("the hook written by SetConfig did not take effect on this handle")
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

func TestSaveGlobalConfig_RefusesAMalformedHooksBlock(t *testing.T) {
	// buildHookSet is the validation SaveGlobalConfig runs before writing; going
	// through it directly keeps this L1 while proving the same refusal.
	if _, err := buildHookSet(GlobalConfig{Hooks: []Hook{{Event: "pre-create"}}}, Config{Prefix: "x"}); err == nil {
		t.Fatal("a hook with no run must not be persistable")
	}
}
