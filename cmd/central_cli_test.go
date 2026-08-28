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

//go:build integration

// L4 CLI tests for central/global store management (CONFIG-SPEC, CLI-SPEC §1–§2):
// init --central, where, store list, and that ordinary commands resolve to a
// central store via the registry.
package cmd_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// taskmgrCentral runs the binary from working dir with TASKMGR_HOME set, so the
// per-user central root is an isolated temp dir (no real ~/.taskmgr touched).
func taskmgrCentral(t *testing.T, workDir, home string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return taskmgrCentralEnv(t, workDir, home, nil, args...)
}

// taskmgrCentralEnv is taskmgrCentral with extra environment entries appended
// after TASKMGR_HOME, for tests that need to set a variable themselves.
func taskmgrCentralEnv(t *testing.T, workDir, home string, extraEnv []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return taskmgrCentralIn(t, "", workDir, home, extraEnv, args...)
}

// taskmgrCentralCwd runs the binary with its process working directory set to
// cwd and --dir passed verbatim, so a test can exercise a *relative* --dir.
func taskmgrCentralCwd(t *testing.T, cwd, home, dirFlag string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return taskmgrCentralIn(t, cwd, dirFlag, home, nil, args...)
}

// taskmgrCentralIn is the shared core: cwd (empty → inherit), the --dir value,
// an isolated TASKMGR_HOME, and any extra environment.
func taskmgrCentralIn(t *testing.T, cwd, dirFlag, home string, extraEnv []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	bin := taskmgrBin(t)
	cmd := exec.Command(bin, append([]string{"--dir", dirFlag}, args...)...)
	cmd.Dir = cwd
	cmd.Env = append(append(os.Environ(), "TASKMGR_HOME="+home), extraEnv...)
	var o, e strings.Builder
	cmd.Stdout = &o
	cmd.Stderr = &e
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = 1
		}
	}
	return o.String(), e.String(), code
}

type whereJSON struct {
	Kind        string `json:"kind"`
	Store       string `json:"store"`
	StorePath   string `json:"store_path"`
	ProjectPath string `json:"project_path"`
}

type storeListJSON struct {
	Path      string `json:"path"`
	Store     string `json:"store"`
	StorePath string `json:"store_path"`
	Health    string `json:"health"`
}

func TestL4_Central_InitWhereListCreate(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	name := filepath.Base(proj)
	wantStoreDir := filepath.Join(home, "stores", name)

	// init --central
	out, errOut, code := taskmgrCentral(t, proj, home, "--json", "init", "--central")
	if code != 0 {
		t.Fatalf("init --central: code=%d stderr=%q", code, errOut)
	}
	var initRes struct {
		Dir    string `json:"dir"`
		Prefix string `json:"prefix"`
		Store  string `json:"store"`
	}
	if err := json.Unmarshal([]byte(out), &initRes); err != nil {
		t.Fatalf("init json: %v (%q)", err, out)
	}
	if initRes.Store != name || initRes.Dir != wantStoreDir {
		t.Errorf("init result = %+v, want store=%q dir=%q", initRes, name, wantStoreDir)
	}

	// where → central
	out, _, code = taskmgrCentral(t, proj, home, "--json", "where")
	if code != 0 {
		t.Fatalf("where: code=%d", code)
	}
	var w whereJSON
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatalf("where json: %v (%q)", err, out)
	}
	if w.Kind != "central" || w.StorePath != wantStoreDir {
		t.Errorf("where = %+v, want kind=central store_path=%q", w, wantStoreDir)
	}
	if w.Store != name {
		t.Errorf("where store = %q, want %q", w.Store, name)
	}

	// store list → one entry
	out, _, code = taskmgrCentral(t, proj, home, "--json", "store", "list")
	if code != 0 {
		t.Fatalf("store list: code=%d", code)
	}
	var entries []storeListJSON
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("store list json: %v (%q)", err, out)
	}
	if len(entries) != 1 || entries[0].Store != name || entries[0].StorePath != wantStoreDir {
		t.Errorf("store list = %+v", entries)
	}
	if entries[0].Health != "ok" {
		t.Errorf("store list health = %q, want ok", entries[0].Health)
	}

	// create resolves to the central store; the file lands under the central root.
	out, errOut, code = taskmgrCentral(t, proj, home, "--json", "create", "--title", "central task")
	if code != 0 {
		t.Fatalf("create: code=%d stderr=%q", code, errOut)
	}
	var created struct {
		ID    string `json:"id"`
		Store string `json:"store"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("create json: %v (%q)", err, out)
	}
	if !strings.HasPrefix(created.ID, initRes.Prefix+"-") {
		t.Errorf("id = %q, want store prefix %q-", created.ID, initRes.Prefix)
	}
	// An ID alone does not say where it landed: the prefix comes from the project
	// directory name, so two projects can share one (CONFIG-SPEC §5).
	if created.Store != name {
		t.Errorf("create store = %q, want %q", created.Store, name)
	}
	if _, err := os.Stat(filepath.Join(wantStoreDir, created.ID+".md")); err != nil {
		t.Errorf("task file not in central store: %v", err)
	}

	// list reads it back through the same resolution.
	out, _, code = taskmgrCentral(t, proj, home, "--json", "list")
	if code != 0 || !strings.Contains(out, created.ID) {
		t.Errorf("list did not return the created id %q: %q", created.ID, out)
	}
	var listed []struct {
		ID    string `json:"id"`
		Store string `json:"store"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("list json: %v (%q)", err, out)
	}
	if len(listed) != 1 || listed[0].Store != name {
		t.Errorf("list rows = %+v, want store=%q on every row", listed, name)
	}

	// show carries it too, so an id captured from any read command can be aimed
	// back at the store it came from.
	out, _, code = taskmgrCentral(t, proj, home, "--json", "show", created.ID)
	if code != 0 {
		t.Fatalf("show: code=%d", code)
	}
	var shown struct {
		Store string `json:"store"`
	}
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatalf("show json: %v (%q)", err, out)
	}
	if shown.Store != name {
		t.Errorf("show store = %q, want %q", shown.Store, name)
	}

	// import returns a source-ID → taskmgr-ID map an adapter keeps; without the
	// store name that map does not say which store it maps into.
	envelope := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(envelope, []byte(`{"source_id":"S-1","title":"imported","type":"task"}`), 0o644); err != nil {
		t.Fatalf("write envelope: %v", err)
	}
	out, errOut, code = taskmgrCentral(t, proj, home, "--json", "import", "--file", envelope)
	if code != 0 {
		t.Fatalf("import: code=%d stderr=%q", code, errOut)
	}
	var imported struct {
		SourceID string `json:"source_id"`
		ID       string `json:"id"`
		Store    string `json:"store"`
	}
	if err := json.Unmarshal([]byte(out), &imported); err != nil {
		t.Fatalf("import json: %v (%q)", err, out)
	}
	if imported.SourceID != "S-1" || imported.Store != name {
		t.Errorf("import = %+v, want source_id=S-1 store=%q", imported, name)
	}
}

// TestL4_LocalStore_OmitsStoreName checks that a local store reports no name:
// it has no registry entry, so there is nothing to report, and an invented one
// would not select anything with --store-name.
func TestL4_LocalStore_OmitsStoreName(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	if _, errOut, code := taskmgrCentral(t, proj, home, "init"); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	out, errOut, code := taskmgrCentral(t, proj, home, "--json", "create", "--title", "local task")
	if code != 0 {
		t.Fatalf("create: code=%d stderr=%q", code, errOut)
	}
	if strings.Contains(out, `"store"`) {
		t.Errorf("create output carries a store field for a local store: %q", out)
	}
	out, _, code = taskmgrCentral(t, proj, home, "--json", "where")
	if code != 0 {
		t.Fatalf("where: code=%d", code)
	}
	var w whereJSON
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatalf("where json: %v (%q)", err, out)
	}
	if w.Kind != "local" || w.Store != "" {
		t.Errorf("where = %+v, want kind=local with no store name", w)
	}
	out, _, code = taskmgrCentral(t, proj, home, "--json", "list")
	if code != 0 {
		t.Fatalf("list: code=%d", code)
	}
	if strings.Contains(out, `"store"`) {
		t.Errorf("list output carries a store field for a local store: %q", out)
	}
}

func TestL4_Central_LocalWinsAndNoStore(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()

	// Register a central store for proj, then also create a local .tasks in proj.
	if _, errOut, code := taskmgrCentral(t, proj, home, "init", "--central"); code != 0 {
		t.Fatalf("init --central: %s", errOut)
	}
	if _, errOut, code := taskmgrCentral(t, proj, home, "init"); code != 0 {
		t.Fatalf("init (local): %s", errOut)
	}
	out, _, code := taskmgrCentral(t, proj, home, "--json", "where")
	if code != 0 {
		t.Fatalf("where: code=%d", code)
	}
	var w whereJSON
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatalf("where json: %v", err)
	}
	if w.Kind != "local" || w.StorePath != filepath.Join(proj, ".tasks") {
		t.Errorf("local should win: %+v", w)
	}

	// where in an unrelated dir with an empty home → none, exit 0.
	other := t.TempDir()
	emptyHome := t.TempDir()
	out, _, code = taskmgrCentral(t, other, emptyHome, "--json", "where")
	if code != 0 {
		t.Fatalf("where none: code=%d", code)
	}
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatalf("where none json: %v (%q)", err, out)
	}
	if w.Kind != "none" {
		t.Errorf("kind = %q, want none", w.Kind)
	}
}
