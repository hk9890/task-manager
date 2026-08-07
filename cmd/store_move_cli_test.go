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

// L4 CLI tests for `store move` (CLI-SPEC §2.1, CONFIG-SPEC §5): promoting a
// local store to central, renaming a central store, and re-pointing an entry at
// a moved project. Uses taskmgrCentral from central_cli_test.go, which isolates
// TASKMGR_HOME in a temp dir.
package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type storeMoveJSON struct {
	Store       string `json:"store"`
	StorePath   string `json:"store_path"`
	ProjectPath string `json:"project_path"`
}

// seedLocal creates a local store in proj with one issue and returns its ID.
func seedLocal(t *testing.T, proj, home string) string {
	t.Helper()
	if _, errOut, code := taskmgrCentral(t, proj, home, "init", "--prefix", "prj"); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	out, errOut, code := taskmgrCentral(t, proj, home, "--json", "create", "--title", "before the move")
	if code != 0 {
		t.Fatalf("create: %s", errOut)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("create json: %v (%q)", err, out)
	}
	return created.ID
}

func TestL4_StoreMove_Central(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	id := seedLocal(t, proj, home)

	out, errOut, code := taskmgrCentral(t, proj, home, "--json", "store", "move", "--central", "--to", "promoted")
	if code != 0 {
		t.Fatalf("store move --central: code=%d stderr=%q", code, errOut)
	}
	var res storeMoveJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("move json: %v (%q)", err, out)
	}
	wantDir := filepath.Join(home, "stores", "promoted")
	if res.Store != "promoted" || res.StorePath != wantDir {
		t.Errorf("move result = %+v, want store=promoted store_path=%q", res, wantDir)
	}

	// Files moved; the local store is gone.
	if _, err := os.Stat(filepath.Join(wantDir, id+".md")); err != nil {
		t.Errorf("task file not in the central store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(proj, ".tasks")); !os.IsNotExist(err) {
		t.Errorf("local .tasks must be gone after a promote: %v", err)
	}

	// The project now resolves centrally, and the issue reads back with its
	// original ID — the prefix travelled with the store.
	out, _, code = taskmgrCentral(t, proj, home, "--json", "where")
	if code != 0 {
		t.Fatalf("where: code=%d", code)
	}
	var w whereJSON
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatalf("where json: %v (%q)", err, out)
	}
	if w.Kind != "central" || w.StorePath != wantDir || w.ProjectPath != proj {
		t.Errorf("where = %+v, want central at %q for %q", w, wantDir, proj)
	}
	if out, _, code := taskmgrCentral(t, proj, home, "--json", "show", id); code != 0 || !strings.Contains(out, id) {
		t.Errorf("issue %s not readable after the promote: code=%d out=%q", id, code, out)
	}
	// A new issue keeps allocating under the same prefix.
	out, errOut, code = taskmgrCentral(t, proj, home, "--json", "create", "--title", "after the move")
	if code != 0 {
		t.Fatalf("create after move: %s", errOut)
	}
	if !strings.Contains(out, `"id":"prj-`) && !strings.Contains(out, `"id": "prj-`) {
		t.Errorf("new issue should keep the prj- prefix: %q", out)
	}
}

// TestL4_StoreMove_CentralDefaultName checks that --to defaults to the project
// directory name, matching `init --central`.
func TestL4_StoreMove_CentralDefaultName(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	seedLocal(t, proj, home)

	out, errOut, code := taskmgrCentral(t, proj, home, "--json", "store", "move", "--central")
	if code != 0 {
		t.Fatalf("store move --central: %s", errOut)
	}
	var res storeMoveJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("move json: %v (%q)", err, out)
	}
	if res.Store != filepath.Base(proj) {
		t.Errorf("store = %q, want the project dir name %q", res.Store, filepath.Base(proj))
	}
}

func TestL4_StoreMove_Rename(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	id := seedLocal(t, proj, home)
	if _, errOut, code := taskmgrCentral(t, proj, home, "store", "move", "--central", "--to", "before"); code != 0 {
		t.Fatalf("promote: %s", errOut)
	}

	out, errOut, code := taskmgrCentral(t, proj, home, "--json", "store", "move", "--rename", "--to", "after")
	if code != 0 {
		t.Fatalf("store move --rename: code=%d stderr=%q", code, errOut)
	}
	var res storeMoveJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("rename json: %v (%q)", err, out)
	}
	wantDir := filepath.Join(home, "stores", "after")
	if res.Store != "after" || res.StorePath != wantDir {
		t.Errorf("rename result = %+v, want store=after store_path=%q", res, wantDir)
	}
	if _, err := os.Stat(filepath.Join(home, "stores", "before")); !os.IsNotExist(err) {
		t.Errorf("old store folder must be gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wantDir, id+".md")); err != nil {
		t.Errorf("task file not in the renamed store: %v", err)
	}
	// The project still resolves, now to the new name.
	out, _, code = taskmgrCentral(t, proj, home, "--json", "where")
	if code != 0 {
		t.Fatalf("where: code=%d", code)
	}
	var w whereJSON
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatalf("where json: %v", err)
	}
	if w.StorePath != wantDir {
		t.Errorf("where store_path = %q, want %q", w.StorePath, wantDir)
	}
}

func TestL4_StoreMove_Relink(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	id := seedLocal(t, proj, home)
	if _, errOut, code := taskmgrCentral(t, proj, home, "store", "move", "--central", "--to", "moved"); code != 0 {
		t.Fatalf("promote: %s", errOut)
	}

	// The project moves to a new directory.
	newProj := t.TempDir()
	out, errOut, code := taskmgrCentral(t, newProj, home, "--json", "store", "move", "--relink", "--to", "moved")
	if code != 0 {
		t.Fatalf("store move --relink: code=%d stderr=%q", code, errOut)
	}
	var res storeMoveJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("relink json: %v (%q)", err, out)
	}
	if res.ProjectPath != newProj {
		t.Errorf("project_path = %q, want %q", res.ProjectPath, newProj)
	}

	// The new location resolves and can read the issue; the old one resolves to
	// nothing.
	if out, _, code := taskmgrCentral(t, newProj, home, "--json", "show", id); code != 0 || !strings.Contains(out, id) {
		t.Errorf("issue not readable from the new project path: code=%d out=%q", code, out)
	}
	out, _, code = taskmgrCentral(t, proj, home, "--json", "where")
	if code != 0 {
		t.Fatalf("where at old path: code=%d", code)
	}
	var w whereJSON
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatalf("where json: %v", err)
	}
	if w.Kind != "none" {
		t.Errorf("old project path should no longer resolve, got %+v", w)
	}
}

func TestL4_StoreMove_Rejections(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	seedLocal(t, proj, home)

	// No mode flag.
	if _, _, code := taskmgrCentral(t, proj, home, "store", "move"); code == 0 {
		t.Error("store move without a mode flag should fail")
	}
	// Two modes at once.
	if _, _, code := taskmgrCentral(t, proj, home, "store", "move", "--central", "--rename", "--to", "x"); code == 0 {
		t.Error("mutually exclusive mode flags should fail")
	}
	// --rename with no --to.
	if _, _, code := taskmgrCentral(t, proj, home, "store", "move", "--rename"); code == 0 {
		t.Error("--rename without --to should fail")
	}
	// --rename on a local store.
	if _, errOut, code := taskmgrCentral(t, proj, home, "store", "move", "--rename", "--to", "x"); code == 0 {
		t.Error("--rename on a local store should fail")
	} else if !strings.Contains(errOut, "central") {
		t.Errorf("stderr should explain a central store is needed: %q", errOut)
	}
	// --relink naming a store that is not registered.
	if _, _, code := taskmgrCentral(t, proj, home, "store", "move", "--relink", "--to", "ghost"); code == 0 {
		t.Error("--relink with an unregistered name should fail")
	}

	// Promote, then promote again: the second must refuse.
	if _, errOut, code := taskmgrCentral(t, proj, home, "store", "move", "--central", "--to", "once"); code != 0 {
		t.Fatalf("promote: %s", errOut)
	}
	if _, errOut, code := taskmgrCentral(t, proj, home, "store", "move", "--central", "--to", "twice"); code == 0 {
		t.Error("promoting an already-central store should fail")
	} else if !strings.Contains(errOut, "already uses a central store") {
		t.Errorf("stderr = %q, want it to say the project is already central", errOut)
	}
}

// TestL4_StorePathFlagRemoved pins the removal of --store-path / TASKMGR_DIR:
// the flag is rejected and the environment variable has no effect.
func TestL4_StorePathFlagRemoved(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	seedLocal(t, proj, home)

	if _, errOut, code := taskmgrCentral(t, proj, home, "--store-path", filepath.Join(proj, ".tasks"), "where"); code == 0 {
		t.Error("--store-path should no longer be a recognised flag")
	} else if !strings.Contains(errOut, "store-path") {
		t.Errorf("stderr = %q, want it to name the unknown flag", errOut)
	}

	// TASKMGR_DIR pointing elsewhere must not divert resolution.
	other := t.TempDir()
	if _, errOut, code := taskmgrCentral(t, other, home, "init", "--prefix", "oth"); code != 0 {
		t.Fatalf("init other: %s", errOut)
	}
	out, _, code := taskmgrCentralEnv(t, proj, home, []string{"TASKMGR_DIR=" + filepath.Join(other, ".tasks")}, "--json", "where")
	if code != 0 {
		t.Fatalf("where with TASKMGR_DIR set: code=%d", code)
	}
	var w whereJSON
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatalf("where json: %v (%q)", err, out)
	}
	if w.Kind != "local" || w.StorePath != filepath.Join(proj, ".tasks") {
		t.Errorf("TASKMGR_DIR must be ignored, got %+v", w)
	}
}
