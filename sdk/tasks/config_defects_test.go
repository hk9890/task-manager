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

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// A defect in a config file's own package keys fails every mutation, is listed
// by `package list`, and never touches a read (HOOK-SPEC §3.4). These are the
// two shapes a hand edit or an upgrade produces.

// seedStoreConfig writes raw bytes over the store's config.yaml and re-reads
// them the way opening the store would, so a test can exercise the store that a
// hand-edited file produces.
func seedStoreConfig(t *testing.T, fs vfs.FS, s *Store, raw string) {
	t.Helper()
	if err := fs.WriteAtomic(filepath.Join(s.dir, ConfigFileName), []byte(raw), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	cfg, err := s.readConfig()
	if err != nil {
		t.Fatalf("readConfig must survive a hand-edited file: %v", err)
	}
	s.cfg = cfg
	s.hookBuilt, s.hookSet, s.hookErr = false, nil, nil
}

// readStoreConfig returns the config file's bytes as they stand on disk.
func readStoreConfig(t *testing.T, fs vfs.FS, s *Store) string {
	t.Helper()
	data, err := fs.ReadFile(filepath.Join(s.dir, ConfigFileName))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	return string(data)
}

// The `hooks:` key was withdrawn when hooks moved into packages. Ignoring it as
// an unknown key ran the store with every gate it declares silently absent —
// fail-open on the write path, which is the one outcome the whole arrangement
// exists to prevent.
func TestStoreConfig_WithdrawnHooksKeyFailsMutationsNotReads(t *testing.T) {
	s, fs := chainStore(t)
	seedStoreConfig(t, fs, s, "prefix: tst\nhooks:\n    - id: guard\n      event: pre-create\n      run: [\"sh\", \"-c\", \"exit 1\"]\n")

	if _, err := s.All(); err != nil {
		t.Errorf("reads must be unaffected: %v", err)
	}

	_, err := s.Create(CreateInput{Title: "x"})
	if err == nil {
		t.Fatal("a config still declaring hooks: must fail the write, not run without the gate")
	}
	if !strings.Contains(err.Error(), "hooks:") || !strings.Contains(err.Error(), "package add") {
		t.Errorf("error %q must name the key and how to migrate it", err)
	}

	infos, err := s.Packages()
	if err != nil {
		t.Fatalf("Packages(): %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "hooks:" || infos[0].Status != PackageBroken {
		t.Fatalf("package list must report the key: %+v", infos)
	}
	if !strings.HasSuffix(infos[0].Path, ConfigFileName) {
		t.Errorf("row path = %q, want the config file that carries the key", infos[0].Path)
	}
}

// `use: doc-policy` is a plausible hand edit. Failing the decode on it took down
// every command in the store — including `where`, `list` and `package list`,
// which are the way out.
func TestStoreConfig_UseThatIsNotAListKeepsReadsWorking(t *testing.T) {
	s, fs := chainStore(t)
	seedStoreConfig(t, fs, s, "prefix: tst\nuse: doc-policy\n")

	if _, err := s.All(); err != nil {
		t.Errorf("reads must be unaffected: %v", err)
	}
	if got := s.Config().Prefix; got != "tst" {
		t.Errorf("prefix = %q, want the rest of the file still decoded", got)
	}

	_, err := s.Create(CreateInput{Title: "x"})
	if err == nil {
		t.Fatal("a use: value that is not a list must fail the write")
	}
	if !strings.Contains(err.Error(), "list of package entries") {
		t.Errorf("error %q must say what the key should hold", err)
	}

	infos, _ := s.Packages()
	if len(infos) != 1 || infos[0].Name != "use:" || infos[0].Status != PackageBroken {
		t.Fatalf("package list must report the key: %+v", infos)
	}
}

// configdoc.go exists to keep a write from rewriting what it does not model. A
// `use:` value it cannot model is the one shape that fell through: rendering the
// empty model over it deleted the author's line from a write aimed elsewhere.
func TestUpdateConfig_LeavesAUseValueItCannotModel(t *testing.T) {
	s, fs := chainStore(t)
	seedStoreConfig(t, fs, s, "prefix: tst\nuse: doc-policy\n")

	if err := s.UpdateConfig(func(c *Config) error {
		c.HookTimeout = "5s"
		return nil
	}); err != nil {
		t.Fatalf("an unrelated key must stay writable: %v", err)
	}

	got := readStoreConfig(t, fs, s)
	if !strings.Contains(got, "use: doc-policy") {
		t.Errorf("the use: key was rewritten by a write that did not touch it:\n%s", got)
	}
	if !strings.Contains(got, "hook_timeout: 5s") {
		t.Errorf("the write did not land:\n%s", got)
	}
}

// yaml.v3 drops a null sequence element before any Unmarshaler runs, so an empty
// `-` was absent from the model and then deleted from the file by the next
// unrelated write. It is carried as a malformed entry instead.
func TestReadConfig_EmptyUseEntrySurvivesAnUnrelatedWrite(t *testing.T) {
	s, fs := chainStore(t)
	seedStoreConfig(t, fs, s, "prefix: tst\nuse:\n    - name: alpha\n    # a note the author left\n    -\n    - name: gamma\n")

	if n := len(s.Config().Use); n != 3 {
		t.Fatalf("use = %d entries, want the empty one carried: %+v", n, s.Config().Use)
	}
	if err := refShape(s.Config().Use[1]); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("the empty entry must be reported at the write, got %v", err)
	}

	if err := s.UpdateConfig(func(c *Config) error {
		c.HookTimeout = "5s"
		return nil
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	got := readStoreConfig(t, fs, s)
	if !strings.Contains(got, "a note the author left") {
		t.Errorf("the comment beside the entry was lost:\n%s", got)
	}
	if strings.Count(got, "-") < 3 {
		t.Errorf("the empty entry was erased by an unrelated write:\n%s", got)
	}
}

// The per-user file carries the same two keys, and a defect in it reaches every
// store on the machine (CONFIG-SPEC §2).
func TestGlobalConfig_WithdrawnHooksKeyFailsMutations(t *testing.T) {
	s, fs := chainStore(t)
	raw := "version: 1\nhooks:\n    - id: guard\n      event: pre-create\n      run: [\"true\"]\n"
	if err := fs.WriteAtomic("/hm/config.yaml", []byte(raw), 0o644); err != nil {
		t.Fatalf("seed global config: %v", err)
	}

	if _, err := s.All(); err != nil {
		t.Errorf("reads must be unaffected: %v", err)
	}
	_, err := s.Create(CreateInput{Title: "x"})
	if err == nil || !strings.Contains(err.Error(), "hooks:") {
		t.Fatalf("a per-user config declaring hooks: must fail the write, got %v", err)
	}
	if !strings.Contains(err.Error(), "/hm/config.yaml") {
		t.Errorf("error %q must name the file to edit", err)
	}
}
