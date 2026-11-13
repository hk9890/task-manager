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

// sync_cli_test.go — `taskmgr sync` end to end through cmd.Run, with real git
// and a bare repository in a temp directory as the remote: what the two
// commands print and how they exit. Behind the integration tag because every
// test spawns git; the git behaviour itself is the SDK's L3
// (sdk/tasks/publish_l3_test.go).
package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func syncJSON(t *testing.T) syncDTO {
	t.Helper()
	var got syncDTO
	if err := json.Unmarshal([]byte(mustRun(t, "--json", "sync")), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSyncInit_PrintsWhereAndWhatItPublishes(t *testing.T) {
	_, stores, remote := syncFixture(t)

	want := stores + " publishes to " + remote + " on branch machine/laptop\nnext: taskmgr sync\n"
	if out := mustRun(t, "sync", "init", "--remote", remote); out != want {
		t.Errorf("sync init printed %q, want %q", out, want)
	}
	var again syncInitDTO
	if err := json.Unmarshal([]byte(mustRun(t, "--json", "sync", "init", "--remote", remote)), &again); err != nil {
		t.Fatal(err)
	}
	if again != (syncInitDTO{Machine: "laptop", Branch: "machine/laptop", Path: stores, Remote: remote}) {
		t.Errorf("second init = %+v", again)
	}

	out, errOut, code := run(t, "sync", "init", "--remote", "/somewhere/else.git")
	if code != 1 || out != "" || !strings.Contains(errOut, "already publishes to "+remote) {
		t.Errorf("init with another remote: exit %d, stdout %q, stderr %q; want 1 naming the existing remote", code, out, errOut)
	}
}

// A relative remote means relative to where the user typed it, not to the
// stores directory git runs in.
func TestSyncInit_RelativeRemoteIsTheCallersPath(t *testing.T) {
	_, _, remote := syncFixture(t)
	t.Chdir(filepath.Dir(remote))
	var got syncInitDTO
	if err := json.Unmarshal([]byte(mustRun(t, "--json", "sync", "init", "--remote", "./"+filepath.Base(remote))), &got); err != nil {
		t.Fatal(err)
	}
	if got.Remote != remote {
		t.Errorf("remote = %q, want %q", got.Remote, remote)
	}
}

func TestSync_PrintsWhatItDid(t *testing.T) {
	project, _, remote := syncFixture(t)
	mustRun(t, "sync", "init", "--remote", remote)

	first := syncJSON(t)
	if !first.Committed || !first.Pushed || len(first.Commit) != 40 || first.Machine != "laptop" || first.Branch != "machine/laptop" {
		t.Fatalf("first sync = %+v, want a commit, pushed", first)
	}
	if got := git(t, remote, "rev-parse", "machine/laptop"); got != first.Commit {
		t.Errorf("remote branch at %s, want the commit sync reported, %s", got, first.Commit)
	}
	if out := mustRun(t, "sync"); out != "Nothing to publish: machine/laptop has nothing new since its last push to origin\n" {
		t.Errorf("sync with nothing new printed %q", out)
	}
	mustRun(t, "--dir", project, "create", "--title", "a change")
	if out := mustRun(t, "sync"); !strings.HasPrefix(out, "Committed ") || strings.Count(out, "\n") != 1 {
		t.Errorf("sync printed %q, want one 'Committed …' line", out)
	}
}

func TestSync_RejectedPushExitsOneNamingTheClash(t *testing.T) {
	project, _, remote := syncFixture(t)
	mustRun(t, "sync", "init", "--remote", remote)
	mustRun(t, "sync")

	other := filepath.Join(t.TempDir(), "other")
	git(t, "", "clone", "--quiet", "--branch", "machine/laptop", remote, other)
	if err := os.WriteFile(filepath.Join(other, "elsewhere"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, other, "add", "--all")
	git(t, other, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "other")
	git(t, other, "push", "--quiet", "origin", "machine/laptop")

	mustRun(t, "--dir", project, "create", "--title", "a change")
	out, errOut, code := run(t, "sync")
	if code != 1 || out != "" {
		t.Fatalf("sync after another push: exit %d, stdout %q; want 1 and no output", code, out)
	}
	for _, want := range []string{`another machine publishes under the machine ID "laptop"`, "taskmgr config set machine_id"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q does not say %q", errOut, want)
		}
	}
}
