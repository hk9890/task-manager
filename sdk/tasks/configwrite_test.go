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

package tasks

// configwrite_test.go — L2: the configuration write path of both files. The
// property under test is that the read happens inside the lock, so two writers
// editing different keys keep both edits (TASK-STORAGE-SPEC §4.2, CONFIG-SPEC §2).

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// memHome returns a Mem filesystem with an empty taskmgr home, and the env that
// points at it.
func memHome(t *testing.T) (*vfs.Mem, env.Environment) {
	t.Helper()
	m := vfs.NewMem()
	if err := m.MkdirAll("/home", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return m, env.Fake{Vars: map[string]string{"TASKMGR_HOME": "/home"}}
}

// ── the store's config.yaml ──────────────────────────────────────────────────

// TestUpdateConfig_ReadsUnderTheLock is the store half of the lost-update bug: a
// handle opened before someone else's write must not carry its stale snapshot
// into its own.
func TestUpdateConfig_ReadsUnderTheLock(t *testing.T) {
	m := vfs.NewMem()
	first, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}
	// second snapshots the config as it is now — before first writes.
	second, err := openData("/p", "/p/.tasks", m, nil)
	if err != nil {
		t.Fatalf("openData: %v", err)
	}

	if err := first.UpdateConfig(func(c *Config) error {
		c.HookTimeout = "5m"
		return nil
	}); err != nil {
		t.Fatalf("first UpdateConfig: %v", err)
	}
	if err := second.UpdateConfig(func(c *Config) error {
		c.Hooks = append(c.Hooks, Hook{ID: "gate", Event: "pre-close", Run: []string{"true"}})
		return nil
	}); err != nil {
		t.Fatalf("second UpdateConfig: %v", err)
	}

	onDisk, err := second.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if onDisk.HookTimeout != "5m" {
		t.Errorf("hook_timeout = %q, want 5m — the second write discarded the first", onDisk.HookTimeout)
	}
	if len(onDisk.Hooks) != 1 {
		t.Fatalf("hooks = %d, want 1", len(onDisk.Hooks))
	}
}

// TestUpdateConfig_ConcurrentWritersAllSurvive reproduces the reported failure:
// twelve `taskmgr config hook add` at once left one hook and reported success
// twelve times.
func TestUpdateConfig_ConcurrentWritersAllSurvive(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}

	const writers = 12
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = s.UpdateConfig(func(c *Config) error {
				c.Hooks = append(c.Hooks, Hook{
					Event: "pre-close",
					Run:   []string{fmt.Sprintf("cmd%d", i)},
				})
				return nil
			})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}

	onDisk, err := s.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if len(onDisk.Hooks) != writers {
		t.Fatalf("config holds %d hooks, want %d — every writer reported success", len(onDisk.Hooks), writers)
	}
}

// TestUpdateConfig_RefusesAHookItWouldIntroduce keeps the validation the change
// preserves: a malformed hook still never reaches the file.
func TestUpdateConfig_RefusesAHookItWouldIntroduce(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}
	err = s.UpdateConfig(func(c *Config) error {
		c.Hooks = append(c.Hooks, Hook{Event: "pre-explode", Run: []string{"x"}})
		return nil
	})
	if err == nil {
		t.Fatal("a hook with an unknown event must be refused")
	}
	if !strings.Contains(err.Error(), "pre-explode") {
		t.Errorf("error %q does not name the offending event", err)
	}
	onDisk, _ := s.readConfig()
	if len(onDisk.Hooks) != 0 {
		t.Errorf("refused write left %d hooks on disk", len(onDisk.Hooks))
	}
}

// TestUpdateConfig_RemovesAHookThatIsAlreadyMalformed is the other half:
// validating the whole block meant a bad entry refused the write that deletes
// it, and the file could then only be repaired by hand.
func TestUpdateConfig_RemovesAHookThatIsAlreadyMalformed(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}
	// Two malformed hooks, written the way a hand edit puts them there.
	raw := "prefix: tst\nhooks:\n  - event: pre-delete\n    run: [\"a\"]\n  - event: pre-purge\n    run: [\"b\"]\n"
	if err := m.WriteAtomic("/p/.tasks/config.yaml", []byte(raw), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if err := s.UpdateConfig(func(c *Config) error {
		c.Hooks = c.Hooks[1:] // drop the first
		return nil
	}); err != nil {
		t.Fatalf("removing one of two malformed hooks must be allowed: %v", err)
	}
	onDisk, err := s.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if len(onDisk.Hooks) != 1 || onDisk.Hooks[0].Event != "pre-purge" {
		t.Fatalf("hooks after removal = %+v", onDisk.Hooks)
	}
}

// TestUpdateConfig_PrefixStaysImmutable holds the rule SetConfig used to check
// against the in-memory snapshot.
func TestUpdateConfig_PrefixStaysImmutable(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}
	for _, c := range []struct {
		name   string
		prefix string
	}{{"changed", "other"}, {"emptied", ""}} {
		t.Run(c.name, func(t *testing.T) {
			err := s.UpdateConfig(func(cfg *Config) error {
				cfg.Prefix = c.prefix
				return nil
			})
			var ve *ValidationError
			if err == nil || !errors.As(err, &ve) || ve.Field != "prefix" {
				t.Fatalf("err = %v, want a prefix ValidationError", err)
			}
		})
	}
}

// TestConfig_DeepCopiesHookArgv: the doc comment promises a caller cannot reach
// the running configuration through the returned copy. A shallow copy shares
// every Run slice with the compiled hook the next mutation executes.
func TestConfig_DeepCopiesHookArgv(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}
	if err := s.UpdateConfig(func(c *Config) error {
		c.Hooks = append(c.Hooks, Hook{ID: "gate", Event: "pre-close", Run: []string{"make", "test"}})
		return nil
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	cfg := s.Config()
	cfg.Hooks[0].Run[0] = "/bin/false"

	if got := s.Config().Hooks[0].Run[0]; got != "make" {
		t.Errorf("editing the returned copy changed the store's config: Run[0] = %q", got)
	}
	hs, err := s.hooks()
	if err != nil {
		t.Fatalf("hooks: %v", err)
	}
	if got := hs.hooks[0].run[0]; got != "make" {
		t.Errorf("editing the returned copy changed the compiled hook: run[0] = %q", got)
	}
}

// TestHooksAndConfig_RaceWithUpdateConfig is the -race guard. Config, Prefix and
// the lazy hook compile all read state that a configuration write replaces;
// zeroing it under the write lock alone raced with every one of them, and could
// hand the write path a nil *hookSet.
func TestHooksAndConfig_RaceWithUpdateConfig(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}

	// The readers spin until the writer is done rather than running a fixed
	// count: a fixed count finishes before the first write commits, and the two
	// accesses never overlap for the detector to see.
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		defer close(done)
		for i := range 50 {
			if err := s.UpdateConfig(func(c *Config) error {
				c.HookTimeout = fmt.Sprintf("%ds", i%5+1)
				return nil
			}); err != nil {
				t.Errorf("UpdateConfig: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_ = s.Config()
				_ = s.Prefix()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				if _, err := s.hooks(); err != nil {
					t.Errorf("hooks: %v", err)
					return
				}
			}
		}
	}()
	wg.Wait()
}

// ── the per-user config.yaml ─────────────────────────────────────────────────

func TestUpdateGlobalConfig_ConcurrentWritersAllSurvive(t *testing.T) {
	m, e := memHome(t)

	const writers = 12
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = updateGlobalConfig(m, e, func(g *GlobalConfig) error {
				g.Hooks = append(g.Hooks, Hook{
					Event: "pre-close",
					Run:   []string{fmt.Sprintf("g%d", i)},
				})
				return nil
			})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}

	got, err := loadGlobalConfig(m, "/home")
	if err != nil {
		t.Fatalf("loadGlobalConfig: %v", err)
	}
	if len(got.Hooks) != writers {
		t.Fatalf("per-user config holds %d hooks, want %d", len(got.Hooks), writers)
	}
}

// TestUpdateGlobalConfig_RemovesOneOfTwoMalformedHooks is the machine-wide
// version of the brick: a global hook that fails to compile blocks every write
// in every store, and validating the surviving entries meant the command that
// removes it was blocked too.
func TestUpdateGlobalConfig_RemovesOneOfTwoMalformedHooks(t *testing.T) {
	m, e := memHome(t)
	raw := "version: 1\nhooks:\n  - event: pre-delete\n    run: [\"a\"]\n  - event: pre-purge\n    run: [\"b\"]\n"
	if err := m.WriteAtomic("/home/config.yaml", []byte(raw), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if err := updateGlobalConfig(m, e, func(g *GlobalConfig) error {
		g.Hooks = g.Hooks[1:]
		return nil
	}); err != nil {
		t.Fatalf("removing one of two malformed global hooks must be allowed: %v", err)
	}
	got, err := loadGlobalConfig(m, "/home")
	if err != nil {
		t.Fatalf("loadGlobalConfig: %v", err)
	}
	if len(got.Hooks) != 1 || got.Hooks[0].Event != "pre-purge" {
		t.Fatalf("hooks after removal = %+v", got.Hooks)
	}
}

func TestUpdateGlobalConfig_RefusesAHookItWouldIntroduce(t *testing.T) {
	m, e := memHome(t)
	err := updateGlobalConfig(m, e, func(g *GlobalConfig) error {
		g.Hooks = append(g.Hooks, Hook{Event: "pre-explode", Run: []string{"x"}})
		return nil
	})
	if err == nil {
		t.Fatal("a global hook with an unknown event must be refused")
	}
	// The id is scoped to the file at fault, so the message never sends the
	// reader to the store's config.yaml.
	if !strings.Contains(err.Error(), globalHookIDPrefix) {
		t.Errorf("error %q does not scope the hook id to the per-user file", err)
	}
	if _, statErr := m.Stat("/home/config.yaml"); statErr == nil {
		t.Error("a refused write created the per-user config")
	}
}
