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
	second, err := openData("/p", "/p/.tasks", m, env.NewOS(), nil)
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
		c.Use = append(c.Use, PackageRef{Name: "gate"})
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
	if len(onDisk.Use) != 1 {
		t.Fatalf("use entries = %d, want 1", len(onDisk.Use))
	}
}

// TestUpdateConfig_ConcurrentWritersAllSurvive reproduces the reported failure:
// twelve `taskmgr package add` at once left one entry and reported success
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
				c.Use = append(c.Use, PackageRef{Name: fmt.Sprintf("cmd%d", i)})
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
	if len(onDisk.Use) != writers {
		t.Fatalf("config holds %d use entries, want %d — every writer reported success", len(onDisk.Use), writers)
	}
}

// TestUpdateConfig_RefusesAUseEntryItWouldIntroduce keeps the validation the
// change preserves: a reference that could never resolve never reaches the file.
func TestUpdateConfig_RefusesAUseEntryItWouldIntroduce(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}
	err = s.UpdateConfig(func(c *Config) error {
		c.Use = append(c.Use, PackageRef{Name: "../escape"})
		return nil
	})
	if err == nil {
		t.Fatal("a use entry with an invalid package name must be refused")
	}
	if !strings.Contains(err.Error(), "../escape") {
		t.Errorf("error %q does not name the offending entry", err)
	}
	onDisk, _ := s.readConfig()
	if len(onDisk.Use) != 0 {
		t.Errorf("refused write left %d use entries on disk", len(onDisk.Use))
	}
}

// TestUpdateConfig_RemovesAUseEntryThatIsAlreadyMalformed is the other half:
// validating the whole list meant a bad entry refused the write that deletes it,
// and the file could then only be repaired by hand.
func TestUpdateConfig_RemovesAUseEntryThatIsAlreadyMalformed(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}
	// Two malformed entries, written the way a hand edit puts them there.
	raw := "prefix: tst\nuse:\n  - name: \"../a\"\n  - name: \"../b\"\n"
	if err := m.WriteAtomic("/p/.tasks/config.yaml", []byte(raw), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if err := s.UpdateConfig(func(c *Config) error {
		c.Use = c.Use[1:] // drop the first
		return nil
	}); err != nil {
		t.Fatalf("removing one of two malformed entries must be allowed: %v", err)
	}
	onDisk, err := s.readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if len(onDisk.Use) != 1 || onDisk.Use[0].Name != "../b" {
		t.Fatalf("use entries after removal = %+v", onDisk.Use)
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

// TestConfig_CopiesTheUseList: the doc comment promises a caller cannot reach
// the running configuration through the returned copy.
func TestConfig_CopiesTheUseList(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}
	if err := s.UpdateConfig(func(c *Config) error {
		c.Use = append(c.Use, PackageRef{Name: "policy"})
		return nil
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	cfg := s.Config()
	cfg.Use[0].Name = "mutated"

	if got := s.Config().Use[0].Name; got != "policy" {
		t.Errorf("editing the returned copy changed the store's config: Name = %q", got)
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
				g.Use = append(g.Use, PackageRef{Name: fmt.Sprintf("g%d", i)})
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
	if len(got.Use) != writers {
		t.Fatalf("per-user config holds %d use entries, want %d", len(got.Use), writers)
	}
}

// TestUpdateGlobalConfig_RemovesOneOfTwoMalformedEntries is the machine-wide
// version of the brick: an unusable package here blocks every write in every
// store, and validating the surviving entries meant the command that removes it
// was blocked too.
func TestUpdateGlobalConfig_RemovesOneOfTwoMalformedEntries(t *testing.T) {
	m, e := memHome(t)
	raw := "version: 1\nuse:\n  - name: \"../a\"\n  - name: \"../b\"\n"
	if err := m.WriteAtomic("/home/config.yaml", []byte(raw), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if err := updateGlobalConfig(m, e, func(g *GlobalConfig) error {
		g.Use = g.Use[1:]
		return nil
	}); err != nil {
		t.Fatalf("removing one of two malformed entries must be allowed: %v", err)
	}
	got, err := loadGlobalConfig(m, "/home")
	if err != nil {
		t.Fatalf("loadGlobalConfig: %v", err)
	}
	if len(got.Use) != 1 || got.Use[0].Name != "../b" {
		t.Fatalf("use entries after removal = %+v", got.Use)
	}
}

func TestUpdateGlobalConfig_RefusesAUseEntryItWouldIntroduce(t *testing.T) {
	m, e := memHome(t)
	err := updateGlobalConfig(m, e, func(g *GlobalConfig) error {
		g.Use = append(g.Use, PackageRef{Name: "../escape"})
		return nil
	})
	if err == nil {
		t.Fatal("a use entry with an invalid package name must be refused")
	}
	// The message is scoped to the file at fault, so it never sends the reader
	// to the store's config.yaml.
	if !strings.Contains(err.Error(), "per-user config") {
		t.Errorf("error %q does not scope the refusal to the per-user file", err)
	}
	if _, statErr := m.Stat("/home/config.yaml"); statErr == nil {
		t.Error("a refused write created the per-user config")
	}
}
