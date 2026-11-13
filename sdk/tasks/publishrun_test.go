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

// publishrun_test.go — L2 for the publish orchestration (publishrun.go) on
// vfs.Mem, exec.Fake and env.Fake: the git it runs and how, the locks around
// the commit, and how each git answer is read. Real git is L3
// (publish_l3_test.go).
package tasks

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

const publishStores = "/home/stores"

// fakeGit answers the git a publish runs as git would for the state it holds,
// and logs each subcommand into the log the locks are logged into.
type fakeGit struct {
	m       *vfs.Mem
	log     *[]string
	remote  string // the URL of origin; "" = no origin
	head    string // the ref HEAD points at; "" = detached
	staged  bool   // add --all finds a change
	commits int
	pushed  string // refs/remotes/origin/<branch>; "" = never pushed
	reject  bool   // the remote refuses the push
	fail    map[string]exec.Result
}

func sha(n int) string { return fmt.Sprintf("%040d", n) }

func (g *fakeGit) run(spec exec.Spec) exec.Result {
	args := spec.Argv[1+len(publishGitSettings):]
	*g.log = append(*g.log, "git "+args[0])
	if r, ok := g.fail[args[0]]; ok {
		return r
	}
	switch args[0] {
	case "init":
		_ = g.m.MkdirAll(filepath.Join(spec.Dir, ".git"), 0o755)
	case "symbolic-ref":
		if args[1] == "HEAD" {
			g.head = args[2]
			return exec.Allow("")
		}
		if g.head == "" {
			return exec.Deny(1, "")
		}
		return exec.Allow(g.head + "\n")
	case "config":
		if g.remote == "" {
			return exec.Deny(1, "")
		}
		return exec.Allow(g.remote + "\n")
	case "remote":
		g.remote = args[3]
	case "diff":
		if g.staged {
			return exec.Deny(1, "")
		}
	case "commit":
		g.commits++
		g.staged = false
	case "rev-parse":
		if args[3] == "HEAD" {
			if g.commits == 0 {
				return exec.Deny(1, "")
			}
			return exec.Allow(sha(g.commits) + "\n")
		}
		if g.pushed == "" {
			return exec.Deny(1, "")
		}
		return exec.Allow(g.pushed + "\n")
	case "push":
		if g.reject {
			return exec.Deny(1, " ! [rejected]        machine/laptop -> machine/laptop (fetch first)\nerror: failed to push some refs")
		}
		g.pushed = sha(g.commits)
	}
	return exec.Allow("")
}

// lockLog is a Mem that logs every lock taken and released: a store's by the
// store name, the central-root lock as "central", the publish lock as
// "publish".
type lockLog struct {
	*vfs.Mem
	log *[]string
}

func (l lockLog) Lock(path string) (func() error, error) {
	unlock, err := l.Mem.Lock(path)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(filepath.Dir(path))
	switch path {
	case filepath.Join("/home", centralLockName):
		name = "central"
	case filepath.Join("/home", publishLockName):
		name = "publish"
	}
	*l.log = append(*l.log, "lock "+name)
	return func() error {
		*l.log = append(*l.log, "unlock "+name)
		return unlock()
	}, nil
}

// publishFixture registers two central stores, out of name order, on a Mem
// whose machine is "laptop" by hostname, and returns the publish seams with a
// fakeGit over them. initialized makes the stores directory an initialized
// work tree publishing to /remote.git.
func publishFixture(t *testing.T, initialized bool) (lockLog, env.Fake, *exec.Fake, *fakeGit) {
	t.Helper()
	m := vfs.NewMem()
	e := env.Fake{Vars: map[string]string{"TASKMGR_HOME": "/home"}, Host: "laptop"}
	for _, name := range []string{"beta", "alpha"} {
		project := "/projects/" + name
		if err := m.MkdirAll(project, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := initCentralWith(project, name, "", m, e, nil); err != nil {
			t.Fatalf("initCentralWith %s: %v", name, err)
		}
	}
	var log []string
	g := &fakeGit{m: m, log: &log}
	if initialized {
		if err := m.MkdirAll(filepath.Join(publishStores, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		g.remote, g.head, g.staged = "/remote.git", "refs/heads/machine/laptop", true
	}
	return lockLog{Mem: m, log: &log}, e, &exec.Fake{Func: g.run}, g
}

// subcommands returns each recorded call's git arguments after the fixed
// settings.
func subcommands(calls []exec.Spec) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, strings.Join(c.Argv[1+len(publishGitSettings):], " "))
	}
	return out
}

func TestPublish_RunsGitInTheStoresWithTheFixedIdentityAndSettings(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	if _, err := publishWith(fs, e, runner); err != nil {
		t.Fatalf("publishWith: %v", err)
	}
	want := slices.Concat([]string{"git"}, publishGitSettings)
	for _, c := range runner.Calls() {
		if !slices.Equal(c.Argv[:len(want)], want) {
			t.Errorf("argv %q does not start with git and the fixed settings", c.Argv)
		}
		if c.Dir != publishStores {
			t.Errorf("git %q ran in %q, want %s", c.Argv, c.Dir, publishStores)
		}
		if !slices.Equal(c.Env, publishIdentity("laptop")) {
			t.Errorf("git %q env = %q, want the laptop identity", c.Argv, c.Env)
		}
		for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_CONFIG_PARAMETERS"} {
			if !slices.Contains(c.Unset, name) {
				t.Errorf("git %q inherits %s", c.Argv, name)
			}
		}
		if c.Timeout != 0 {
			t.Errorf("git %q has timeout %v, want none", c.Argv, c.Timeout)
		}
	}
	got := subcommands(runner.Calls())
	if !slices.Contains(got, "commit --quiet --no-verify -m taskmgr sync: laptop") ||
		!slices.Contains(got, "push --quiet --no-verify origin machine/laptop") {
		t.Errorf("git calls = %q, want a --no-verify commit and push", got)
	}
}

// The commit is a settled state only if no writer can run during add and
// commit; holding a lock across the push would stall every writer on the
// network.
func TestPublish_LocksEveryStoreAroundTheCommitAndNotThePush(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	res, err := publishWith(fs, e, runner)
	if err != nil {
		t.Fatalf("publishWith: %v", err)
	}
	if !res.Committed || !res.Pushed || res.Commit != sha(1) {
		t.Fatalf("result = %+v, want a commit, pushed", res)
	}
	log := *g.log
	idx := func(entry string) int {
		i := slices.Index(log, entry)
		if i < 0 {
			t.Fatalf("%q not in %q", entry, log)
		}
		return i
	}
	before := func(first, then string) bool { return idx(first) < idx(then) }
	if !before("lock alpha", "lock beta") {
		t.Errorf("locks taken out of name order: %q", log)
	}
	if !before("lock beta", "git add") || !before("git commit", "unlock alpha") || !before("git commit", "unlock beta") {
		t.Errorf("add and commit are not inside both locks: %q", log)
	}
	if !before("unlock alpha", "git push") || !before("unlock beta", "git push") {
		t.Errorf("a lock is held across the push: %q", log)
	}
}

// The registry is read under the central-root lock, so no store is created,
// moved in or renamed between the read and the commit. It comes before every
// store lock, the order every registry operation takes them in, and is
// released before the push like them.
func TestPublish_TakesTheCentralLockFirstAndNotAcrossThePush(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	if _, err := publishWith(fs, e, runner); err != nil {
		t.Fatalf("publishWith: %v", err)
	}
	log := *g.log
	central, unlock := slices.Index(log, "lock central"), slices.Index(log, "unlock central")
	if central < 0 || central > slices.Index(log, "lock alpha") {
		t.Errorf("central lock not taken before the store locks: %q", log)
	}
	if unlock < slices.Index(log, "unlock alpha") || unlock > slices.Index(log, "git push") {
		t.Errorf("central lock not released after the store locks and before the push: %q", log)
	}
}

// Two publishes that push at once make the remote refuse one ref update; the
// publish lock, which no writer takes, makes them run one after the other.
func TestPublish_HoldsThePublishLockAroundEveryGitCommand(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	if _, err := publishWith(fs, e, runner); err != nil {
		t.Fatalf("publishWith: %v", err)
	}
	log := *g.log
	lock, unlock := slices.Index(log, "lock publish"), slices.Index(log, "unlock publish")
	firstGit := slices.IndexFunc(log, func(s string) bool { return strings.HasPrefix(s, "git ") })
	if lock < 0 || lock > firstGit || unlock < slices.Index(log, "git push") {
		t.Errorf("publish lock does not cover every git command: %q", log)
	}

	fs2, e2, runner2, g2 := publishFixture(t, false)
	if _, err := initPublishWith("/remote.git", fs2, e2, runner2); err != nil {
		t.Fatalf("initPublishWith: %v", err)
	}
	log = *g2.log
	if log[0] != "lock publish" || log[len(log)-1] != "unlock publish" {
		t.Errorf("init does not run under the publish lock: %q", log)
	}
}

func TestPublish_NothingNewCommitsAndPushesNothing(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.staged, g.commits, g.pushed = false, 3, sha(3)
	res, err := publishWith(fs, e, runner)
	if err != nil {
		t.Fatalf("publishWith: %v", err)
	}
	if res != (PublishResult{Machine: "laptop", Branch: "machine/laptop"}) {
		t.Errorf("result = %+v, want nothing done", res)
	}
	if got := subcommands(runner.Calls()); slices.ContainsFunc(got, func(s string) bool {
		return strings.HasPrefix(s, "commit") || strings.HasPrefix(s, "push")
	}) {
		t.Errorf("git calls = %q, want no commit and no push", got)
	}
}

// A commit an earlier, failed push left behind is pushed by the next run.
func TestPublish_PushesWhatAnEarlierRunLeftUnpushed(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.staged, g.commits, g.pushed = false, 2, sha(1)
	res, err := publishWith(fs, e, runner)
	if err != nil {
		t.Fatalf("publishWith: %v", err)
	}
	if res.Committed || res.Commit != "" || !res.Pushed {
		t.Errorf("result = %+v, want no commit, pushed", res)
	}
}

func TestPublish_RejectedPushIsTheMachineIDClash(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.reject = true
	_, err := publishWith(fs, e, runner)
	if err == nil || !strings.Contains(err.Error(), `another machine publishes under the machine ID "laptop"`) {
		t.Errorf("rejected push: %v, want the machine-ID clash", err)
	}
}

func TestPublish_OtherPushFailureCarriesGitsMessage(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.fail = map[string]exec.Result{"push": exec.Deny(128, "fatal: could not read from remote repository")}
	_, err := publishWith(fs, e, runner)
	if err == nil || !strings.Contains(err.Error(), "could not read from remote repository") || strings.Contains(err.Error(), "another machine") {
		t.Errorf("unreachable remote: %v, want git's own message", err)
	}
}

func TestPublish_GitMissingIsReportedAsSuch(t *testing.T) {
	fs, e, _, _ := publishFixture(t, true)
	missing := &exec.Fake{Func: func(exec.Spec) exec.Result {
		return exec.Result{Category: exec.SpawnError, Err: errors.New(`exec: "git": executable file not found in $PATH`)}
	}}
	if _, err := publishWith(fs, e, missing); err == nil || !strings.Contains(err.Error(), "git is not on PATH") {
		t.Errorf("publish without git: %v", err)
	}
	if _, err := initPublishWith("/remote.git", fs, e, missing); err == nil || !strings.Contains(err.Error(), "git is not on PATH") {
		t.Errorf("init without git: %v", err)
	}
}

// Only exit 1 means "absent": a broken config is not a missing remote.
func TestPublish_OnlyExitOneReadsAsAbsent(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.fail = map[string]exec.Result{"config": exec.Deny(128, "fatal: bad config line 3")}
	_, err := publishWith(fs, e, runner)
	if err == nil || !strings.Contains(err.Error(), "bad config line 3") || strings.Contains(err.Error(), "has no remote") {
		t.Errorf("broken config: %v, want git's error, not a missing remote", err)
	}
}

func TestPublish_BeforeInitRunsNoGit(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, false)
	_, err := publishWith(fs, e, runner)
	if err == nil || !strings.Contains(err.Error(), "taskmgr sync init --remote <url>") {
		t.Errorf("publish before init: %v", err)
	}
	if n := len(runner.Calls()); n != 0 {
		t.Errorf("ran git %d times before init", n)
	}
}

func TestPublish_NoRemoteNamesInit(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.remote = ""
	if _, err := publishWith(fs, e, runner); err == nil || !strings.Contains(err.Error(), `has no remote "origin"`) {
		t.Errorf("publish without origin: %v", err)
	}
}

func TestPublish_RefusesAnotherMachinesBranch(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.head = "refs/heads/machine/desktop"
	_, err := publishWith(fs, e, runner)
	if err == nil || !strings.Contains(err.Error(), "machine_id changed") || !strings.Contains(err.Error(), "branch -m machine/laptop") {
		t.Errorf("publish on another branch: %v", err)
	}
	g.head = ""
	if _, err := publishWith(fs, e, runner); err == nil || !strings.Contains(err.Error(), "is not on a branch") {
		t.Errorf("publish detached: %v", err)
	}
}

// --short would print heads/machine/laptop when a tag of that name exists, so
// the comparison is on the full ref.
func TestPublish_ComparesTheFullRef(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.head = "refs/tags/machine/laptop"
	if _, err := publishWith(fs, e, runner); err == nil || !strings.Contains(err.Error(), "machine_id changed") {
		t.Errorf("HEAD at a tag of the branch's name: %v, want refused", err)
	}
}

func TestPublish_RewritesTheExcludes(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	exclude := filepath.Join(publishStores, ".git", "info", "exclude")
	if err := fs.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteAtomic(exclude, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := publishWith(fs, e, runner); err != nil {
		t.Fatalf("publishWith: %v", err)
	}
	got, err := fs.ReadFile(exclude)
	if err != nil || string(got) != string(publishExcludes(vfs.TempPrefix)) {
		t.Errorf("exclude = %q (%v), want the engine's patterns", got, err)
	}
	if _, err := fs.Stat(filepath.Join(publishStores, ".gitignore")); !vfs.IsNotExist(err) {
		t.Errorf("publish wrote a tracked .gitignore: %v", err)
	}
}

// A registered store whose directory is gone has nothing to lock and must not
// stop every other store from publishing.
func TestPublish_SkipsTheLockOfADanglingStore(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	if err := fs.RemoveAll(filepath.Join(publishStores, "beta")); err != nil {
		t.Fatal(err)
	}
	if _, err := publishWith(fs, e, runner); err != nil {
		t.Fatalf("publishWith with a dangling entry: %v", err)
	}
	if slices.Contains(*g.log, "lock beta") || !slices.Contains(*g.log, "lock alpha") {
		t.Errorf("log = %q, want alpha locked and beta skipped", *g.log)
	}
}

func TestPublish_LockFailureCommitsNothing(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	fs.FailOn("Lock", filepath.Join(publishStores, "beta", lockFileName), errors.New("ENOLCK"))
	if _, err := publishWith(fs, e, runner); err == nil || !strings.Contains(err.Error(), "ENOLCK") {
		t.Fatalf("publish with an unlockable store: %v", err)
	}
	if got := subcommands(runner.Calls()); slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "add") }) {
		t.Errorf("git calls = %q, want nothing staged without the locks", got)
	}
}

func TestInitPublish_CreatesTheWorkTreeOnTheMachineBranch(t *testing.T) {
	fs, e, runner, g := publishFixture(t, false)
	info, err := initPublishWith("/remote.git", fs, e, runner)
	if err != nil {
		t.Fatalf("initPublishWith: %v", err)
	}
	want := PublishInfo{Machine: "laptop", Branch: "machine/laptop", Path: publishStores, Remote: "/remote.git"}
	if info != want {
		t.Errorf("info = %+v, want %+v", info, want)
	}
	wantCalls := []string{
		"init --quiet",
		"symbolic-ref HEAD refs/heads/machine/laptop",
		"symbolic-ref --quiet HEAD",
		"config --get remote.origin.url",
		"remote add origin /remote.git",
	}
	if got := subcommands(runner.Calls()); !slices.Equal(got, wantCalls) {
		t.Errorf("git calls = %q, want %q", got, wantCalls)
	}
	if got, err := fs.ReadFile(filepath.Join(publishStores, ".git", "info", "exclude")); err != nil || string(got) != string(publishExcludes(vfs.TempPrefix)) {
		t.Errorf("exclude = %q (%v)", got, err)
	}

	again, err := initPublishWith("/remote.git", fs, e, runner)
	if err != nil || again != want {
		t.Errorf("second init = %+v, %v; want %+v", again, err, want)
	}
	if g.remote != "/remote.git" {
		t.Errorf("origin = %q after a second init", g.remote)
	}
}

func TestInitPublish_RefusesAnotherRemote(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	_, err := initPublishWith("/elsewhere.git", fs, e, runner)
	if err == nil || !strings.Contains(err.Error(), "already publishes to /remote.git") || !strings.Contains(err.Error(), "remote remove origin") {
		t.Errorf("init with another remote: %v", err)
	}
	if g.remote != "/remote.git" {
		t.Errorf("origin = %q, want it unchanged", g.remote)
	}
}

// git runs in the stores directory, so a relative path must be resolved
// before git sees it; the Mem's working directory is "/".
func TestInitPublish_ResolvesOnlyRelativeLocalRemotes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"./remote.git", "/remote.git"},
		{"../a/b.git", "/a/b.git"},
		{"git@github.com:you/stores.git", "git@github.com:you/stores.git"},
		{"https://github.com/you/stores.git", "https://github.com/you/stores.git"},
	} {
		fs, e, runner, _ := publishFixture(t, false)
		info, err := initPublishWith(tc.in, fs, e, runner)
		if err != nil || info.Remote != tc.want {
			t.Errorf("init --remote %q: remote %q, %v; want %q", tc.in, info.Remote, err, tc.want)
		}
	}
}

func TestInitPublish_InvalidMachineIDCreatesNothing(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, false)
	if err := fs.WriteAtomic("/home/config.yaml", []byte("version: 1\nmachine_id: Lap_Top\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := initPublishWith("/remote.git", fs, e, runner)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "machine_id" {
		t.Fatalf("init with a bad machine_id: %v, want a *ValidationError on machine_id", err)
	}
	if _, err := fs.Stat(filepath.Join(publishStores, ".git")); !vfs.IsNotExist(err) {
		t.Errorf("a refused init created .git: %v", err)
	}
	if n := len(runner.Calls()); n != 0 {
		t.Errorf("a refused init ran git %d times", n)
	}
}

func TestInitPublish_EmptyRemoteIsRefused(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, false)
	if _, err := initPublishWith("", fs, e, runner); err == nil || len(runner.Calls()) != 0 {
		t.Errorf("init with an empty remote: %v after %d git calls, want refused before git", err, len(runner.Calls()))
	}
}

func TestMachineID_ConfiguredElseHostname(t *testing.T) {
	fs, e, _, _ := publishFixture(t, false)
	if id, err := machineIDWith(fs, e); err != nil || id != "laptop" {
		t.Errorf("machine ID from the hostname = %q, %v", id, err)
	}
	if err := updateGlobalConfig(fs, e, func(g *GlobalConfig) error { g.MachineID = "desk-1"; return nil }); err != nil {
		t.Fatal(err)
	}
	if id, err := machineIDWith(fs, e); err != nil || id != "desk-1" {
		t.Errorf("machine ID from the config = %q, %v", id, err)
	}
}
