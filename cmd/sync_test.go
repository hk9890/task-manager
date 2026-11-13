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

// sync_test.go — `taskmgr sync`, in-process through cmd.Run (CLI-SPEC §2.5):
// the summary line, and the refusals the SDK makes before it runs git. The
// operation itself is tested in the SDK; what needs real git here is in
// sync_cli_test.go, behind the integration tag.
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncSummary_OneLinePerOutcome(t *testing.T) {
	for _, tc := range []struct {
		in   syncDTO
		want string
	}{
		{syncDTO{Branch: "machine/laptop", Committed: true, Commit: "3f9c2a1e4b7d8c6a5f0e1d2c3b4a59687f6e5d4c", Pushed: true},
			"Committed 3f9c2a1e4b7d and pushed machine/laptop to origin"},
		{syncDTO{Branch: "machine/laptop", Pushed: true},
			"Nothing new to commit; pushed machine/laptop to origin"},
		{syncDTO{Branch: "machine/laptop"},
			"Nothing to publish: no change since the last sync"},
	} {
		if got := syncSummary(tc.in); got != tc.want {
			t.Errorf("syncSummary(%+v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSync_BeforeInitIsAnError(t *testing.T) {
	t.Setenv("TASKMGR_HOME", t.TempDir())
	mustRun(t, "config", "set", "machine_id", "laptop", "--global")
	out, errOut, code := run(t, "sync")
	if code != 1 || out != "" || !strings.Contains(errOut, "taskmgr sync init --remote <url>") {
		t.Errorf("sync before init: exit %d, stdout %q, stderr %q; want 1 pointing at sync init", code, out, errOut)
	}
}

func TestSyncInit_RemoteIsRequired(t *testing.T) {
	t.Setenv("TASKMGR_HOME", t.TempDir())
	if _, errOut, code := run(t, "sync", "init"); code != 1 || !strings.Contains(errOut, "remote") {
		t.Errorf("sync init without --remote: exit %d, stderr %q", code, errOut)
	}
}

func TestMachineID_GrammarIsEnforced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TASKMGR_HOME", home)

	_, errOut, code := run(t, "config", "set", "machine_id", "Lap_Top", "--global")
	if code != 1 || !strings.Contains(errOut, "machine_id") || !strings.Contains(errOut, `"Lap_Top"`) {
		t.Errorf("config set of a bad ID: exit %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(home, "config.yaml")); !os.IsNotExist(err) {
		t.Errorf("a refused machine_id was written: %v", err)
	}

	// A hand-edited value is refused where it is read.
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("version: 1\nmachine_id: Lap_Top\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, code = run(t, "sync", "init", "--remote", "/nowhere.git")
	if code != 1 || !strings.Contains(errOut, "machine_id") || !strings.Contains(errOut, `"Lap_Top"`) {
		t.Errorf("sync init with a bad ID: exit %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(home, "stores")); !os.IsNotExist(err) {
		t.Errorf("a refused init created %s: %v", filepath.Join(home, "stores"), err)
	}
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, code := run(t, args...)
	if code != 0 {
		t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
	}
	return out
}
