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

// sync_test.go — `taskmgr sync`, in-process through cmd.Run (CLI-SPEC §2.5),
// and the machine-ID rules it reads (CONFIG-SPEC §2).
//
// Every remote here is a bare repository in a temp directory, so the tests
// exercise the real commit and push paths without reaching a network.
package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── L1: the machine-ID rules ─────────────────────────────────────────────────

func TestDeriveMachineID(t *testing.T) {
	for _, tc := range []struct{ host, want string }{
		{"laptop", "laptop"},
		{"Hans-Laptop.local", "hans-laptop"},
		{"build_01.example.com", "build-01"},
		{"-edge-", "edge"},
		{"my host", "my-host"},
		{"ünïcode", "n-code"},
		{"", ""},
		{".local", ""},
		{"___", ""},
	} {
		if got := deriveMachineID(tc.host); got != tc.want {
			t.Errorf("deriveMachineID(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

func TestValidMachineID(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"laptop", true},
		{"0", true},
		{"build-01", true},
		{"a" + strings.Repeat("b", 62), true},
		{"a" + strings.Repeat("b", 63), false},
		{"", false},
		{"-laptop", false},
		{"Laptop", false},
		{"lap_top", false},
		{"lap.top", false},
		{"lap/top", false},
	} {
		if got := validMachineID(tc.id); got != tc.want {
			t.Errorf("validMachineID(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

// ── L4: sync init and sync ───────────────────────────────────────────────────

// syncFixture isolates the home, names the machine, registers one central
// store, and returns the project directory, the stores directory and a bare
// remote.
func syncFixture(t *testing.T) (project, stores, remote string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("TASKMGR_HOME", home)
	mustRun(t, "config", "set", "machine_id", "laptop", "--global")
	project = t.TempDir()
	mustRun(t, "--dir", project, "init", "--central", "--store-name", "demo")
	remote = filepath.Join(t.TempDir(), "remote.git")
	git(t, "", "init", "--quiet", "--bare", remote)
	return project, filepath.Join(home, "stores"), remote
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, code := run(t, args...)
	if code != 0 {
		t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
	}
	return out
}

func syncJSON(t *testing.T) syncDTO {
	t.Helper()
	var got syncDTO
	if err := json.Unmarshal([]byte(mustRun(t, "--json", "sync")), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSyncInit_CreatesTheRepositoryAndIsIdempotent(t *testing.T) {
	_, stores, remote := syncFixture(t)

	mustRun(t, "sync", "init", "--remote", remote)
	if got := git(t, stores, "symbolic-ref", "--short", "HEAD"); got != "machine/laptop" {
		t.Errorf("branch = %q, want machine/laptop", got)
	}
	if got := git(t, stores, "remote", "get-url", "origin"); got != remote {
		t.Errorf("origin = %q, want %q", got, remote)
	}
	data, err := os.ReadFile(filepath.Join(stores, ".gitignore"))
	if err != nil || string(data) != syncGitignore {
		t.Errorf(".gitignore = %q (%v), want the engine's transient patterns", data, err)
	}

	var again syncInitDTO
	if err := json.Unmarshal([]byte(mustRun(t, "--json", "sync", "init", "--remote", remote)), &again); err != nil {
		t.Fatal(err)
	}
	want := syncInitDTO{Machine: "laptop", Branch: "machine/laptop", Path: stores, Remote: remote}
	if again != want {
		t.Errorf("second init = %+v, want %+v", again, want)
	}

	_, errOut, code := run(t, "sync", "init", "--remote", "/somewhere/else.git")
	if code != 1 || !strings.Contains(errOut, "already publishes to "+remote) {
		t.Errorf("init with another remote: exit %d, stderr %q; want 1 naming the existing remote", code, errOut)
	}
	if got := git(t, stores, "remote", "get-url", "origin"); got != remote {
		t.Errorf("origin = %q after a refused init, want it unchanged", got)
	}
}

// The patterns are checked against the files the engine really leaves behind,
// and against the names it uses for the ones it removes again.
func TestSyncInit_GitignoreKeepsTheEngineTransientFilesOut(t *testing.T) {
	project, stores, remote := syncFixture(t)
	mustRun(t, "sync", "init", "--remote", remote)
	mustRun(t, "--dir", project, "create", "--title", "takes the lock")
	if _, err := os.Stat(filepath.Join(stores, "demo", ".lock")); err != nil {
		t.Fatalf("a write left no lock file to ignore: %v", err)
	}

	for _, p := range []string{"demo/.tmp-123", "demo/content/.dem-0001.incoming", ".incoming-demo/config.yaml"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(stores, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stores, p), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, stores, "add", "--all")
	staged := git(t, stores, "diff", "--cached", "--name-only")
	for _, ignored := range []string{".lock", ".tmp-", ".incoming"} {
		if strings.Contains(staged, ignored) {
			t.Errorf("staged %q, which holds an engine transient file:\n%s", ignored, staged)
		}
	}
	for _, kept := range []string{".gitignore", "demo/config.yaml"} {
		if !strings.Contains(staged, kept) {
			t.Errorf("did not stage %s:\n%s", kept, staged)
		}
	}
}

func TestSync_CommitsPushesAndSkipsWhatIsPublished(t *testing.T) {
	project, stores, remote := syncFixture(t)
	mustRun(t, "sync", "init", "--remote", remote)

	first := syncJSON(t)
	if !first.Committed || !first.Pushed || first.Commit == "" || first.Machine != "laptop" || first.Branch != "machine/laptop" {
		t.Fatalf("first sync = %+v, want a commit, pushed", first)
	}
	if got := git(t, remote, "rev-parse", "machine/laptop"); got != first.Commit {
		t.Errorf("remote branch at %s, want the commit sync reported, %s", got, first.Commit)
	}
	if got := git(t, stores, "log", "-1", "--format=%an <%ae> %s"); got != "taskmgr <taskmgr@laptop> taskmgr sync: laptop" {
		t.Errorf("commit = %q", got)
	}

	if again := syncJSON(t); again != (syncDTO{Machine: "laptop", Branch: "machine/laptop"}) {
		t.Errorf("sync with nothing new = %+v, want no commit and no push", again)
	}

	mustRun(t, "--dir", project, "create", "--title", "a change")
	next := syncJSON(t)
	if !next.Committed || !next.Pushed || next.Commit == first.Commit {
		t.Fatalf("sync after a change = %+v, want a new commit, pushed", next)
	}
	if got := git(t, remote, "rev-parse", "machine/laptop"); got != next.Commit {
		t.Errorf("remote branch at %s, want %s", got, next.Commit)
	}

	// The work tree in stores/ does not disturb the registry or resolution.
	var entries []storeEntryDTO
	if err := json.Unmarshal([]byte(mustRun(t, "--json", "store", "list")), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Store != "demo" || entries[0].Health != "ok" {
		t.Errorf("store list = %+v, want the one store, ok", entries)
	}
}

func TestSync_HumanOutputIsOneLine(t *testing.T) {
	_, _, remote := syncFixture(t)
	mustRun(t, "sync", "init", "--remote", remote)
	if out := mustRun(t, "sync"); !strings.HasPrefix(out, "Committed ") || strings.Count(out, "\n") != 1 {
		t.Errorf("sync printed %q, want one 'Committed …' line", out)
	}
	if out := mustRun(t, "sync"); out != "Nothing to publish: machine/laptop has nothing new since its last push to origin\n" {
		t.Errorf("sync with nothing new printed %q", out)
	}
}

func TestSync_RejectedPushNamesTheMachineIDClash(t *testing.T) {
	project, _, remote := syncFixture(t)
	mustRun(t, "sync", "init", "--remote", remote)
	mustRun(t, "sync")

	// A second machine set up under the same ID publishes first.
	other := filepath.Join(t.TempDir(), "other")
	git(t, "", "clone", "--quiet", "--branch", "machine/laptop", remote, other)
	if err := os.WriteFile(filepath.Join(other, "elsewhere"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, other, "add", "--all")
	git(t, other, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "other")
	git(t, other, "push", "--quiet", "origin", "machine/laptop")
	pushed := git(t, remote, "rev-parse", "machine/laptop")

	mustRun(t, "--dir", project, "create", "--title", "a change")
	out, errOut, code := run(t, "sync")
	if code != 1 || out != "" {
		t.Fatalf("sync after another push: exit %d, stdout %q; want 1 and no output", code, out)
	}
	for _, want := range []string{"another machine publishes under the machine ID \"laptop\"", "taskmgr config set machine_id"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q does not say %q", errOut, want)
		}
	}
	if got := git(t, remote, "rev-parse", "machine/laptop"); got != pushed {
		t.Errorf("remote branch moved to %s; a rejected push must leave it at %s", got, pushed)
	}
}

func TestSync_BeforeInitIsAnError(t *testing.T) {
	syncFixture(t)
	_, errOut, code := run(t, "sync")
	if code != 1 || !strings.Contains(errOut, "taskmgr sync init --remote <url>") {
		t.Errorf("sync before init: exit %d, stderr %q; want 1 pointing at sync init", code, errOut)
	}
}

func TestSync_ChangedMachineIDIsRefused(t *testing.T) {
	_, _, remote := syncFixture(t)
	mustRun(t, "sync", "init", "--remote", remote)
	mustRun(t, "config", "set", "machine_id", "desktop", "--global")
	for _, args := range [][]string{{"sync"}, {"sync", "init", "--remote", remote}} {
		_, errOut, code := run(t, args...)
		if code != 1 || !strings.Contains(errOut, "machine_id changed") || !strings.Contains(errOut, "machine/desktop") {
			t.Errorf("%v on a renamed machine: exit %d, stderr %q", args, code, errOut)
		}
	}
}

// Once stores/ is a work tree, a missing git must still be reported as such,
// not as a missing remote or a detached work tree.
func TestSync_GitMissingIsReportedAsSuch(t *testing.T) {
	_, _, remote := syncFixture(t)
	mustRun(t, "sync", "init", "--remote", remote)
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{{"sync"}, {"sync", "init", "--remote", remote}} {
		_, errOut, code := run(t, args...)
		if code != 1 || !strings.Contains(errOut, "git is not on PATH") {
			t.Errorf("%v without git: exit %d, stderr %q", args, code, errOut)
		}
	}
}

// A hook from the user's global git config must not stop an unattended sync.
func TestSync_GlobalHooksDoNotRun(t *testing.T) {
	_, _, remote := syncFixture(t)
	mustRun(t, "sync", "init", "--remote", remote)
	hooks := t.TempDir()
	for _, hook := range []string{"prepare-commit-msg", "post-commit"} {
		if err := os.WriteFile(filepath.Join(hooks, hook), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[core]\n\thooksPath = "+hooks+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if got := syncJSON(t); !got.Committed || !got.Pushed {
		t.Errorf("sync with failing global hooks = %+v, want a commit, pushed", got)
	}
}

func TestMachineID_GrammarIsEnforced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TASKMGR_HOME", home)

	_, errOut, code := run(t, "config", "set", "machine_id", "Lap_Top", "--global")
	if code != 1 || !strings.Contains(errOut, `invalid machine_id "Lap_Top"`) {
		t.Errorf("config set of a bad ID: exit %d, stderr %q", code, errOut)
	}

	// A hand-edited value is refused where it is read.
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("version: 1\nmachine_id: Lap_Top\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, code = run(t, "sync", "init", "--remote", "/nowhere.git")
	if code != 1 || !strings.Contains(errOut, `invalid machine_id "Lap_Top"`) {
		t.Errorf("sync init with a bad ID: exit %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(home, "stores")); !os.IsNotExist(err) {
		t.Errorf("a refused init created %s: %v", filepath.Join(home, "stores"), err)
	}
}
