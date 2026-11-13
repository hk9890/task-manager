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
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

const publishStores = "/home/stores"

// fakeGit answers the git a publish runs as git would for the state it holds,
// and logs each subcommand into the log the locks are logged into.
type fakeGit struct {
	m        *vfs.Mem
	log      *[]string
	remote   string          // the URL of origin; "" = no origin
	head     string          // the ref HEAD points at; "" = detached
	branches map[string]bool // full refs of branches other than HEAD's that hold a commit
	staged   bool            // add --all finds a change
	commits  int             // commits on HEAD's branch; 0 = unborn
	pushed   string          // refs/remotes/origin/<branch>; "" = never pushed
	reject   bool            // the remote refuses the push
	fail     map[string]exec.Result
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
		g.head = "refs/heads/master"
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
		switch ref := args[3]; {
		case ref == "HEAD" || ref == g.head:
			if g.commits == 0 {
				return exec.Deny(1, "")
			}
			return exec.Allow(sha(g.commits) + "\n")
		case strings.HasPrefix(ref, "refs/heads/"):
			if !g.branches[ref] {
				return exec.Deny(1, "")
			}
			return exec.Allow(sha(1) + "\n")
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
// "publish". A lock named in busy is held by another writer: TryLock logs
// "busy <name>" and returns not ok, and Lock fails the call instead of
// waiting forever.
type lockLog struct {
	*vfs.Mem
	log  *[]string
	busy map[string]bool
}

func (l lockLog) lockName(path string) string {
	switch path {
	case filepath.Join("/home", centralLockName):
		return "central"
	case filepath.Join("/home", publishLockName):
		return "publish"
	}
	return filepath.Base(filepath.Dir(path))
}

func (l lockLog) logged(name string, unlock func() error) func() error {
	*l.log = append(*l.log, "lock "+name)
	return func() error {
		*l.log = append(*l.log, "unlock "+name)
		return unlock()
	}
}

func (l lockLog) Lock(path string) (func() error, error) {
	name := l.lockName(path)
	if l.busy[name] {
		*l.log = append(*l.log, "wait on "+name)
		return nil, fmt.Errorf("blocking lock on %s, which another writer holds", name)
	}
	unlock, err := l.Mem.Lock(path)
	if err != nil {
		return nil, err
	}
	return l.logged(name, unlock), nil
}

func (l lockLog) TryLock(path string) (func() error, bool, error) {
	name := l.lockName(path)
	if l.busy[name] {
		*l.log = append(*l.log, "busy "+name)
		return nil, false, nil
	}
	unlock, ok, err := l.Mem.TryLock(path)
	if err != nil || !ok {
		return nil, ok, err
	}
	return l.logged(name, unlock), true, nil
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
	return lockLog{Mem: m, log: &log, busy: map[string]bool{}}, e, &exec.Fake{Func: g.run}, g
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
		if !slices.Equal(c.Env, publishEnv("laptop")) {
			t.Errorf("git %q env = %q, want the laptop identity, no prompt and no system attributes", c.Argv, c.Env)
		}
		for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_CONFIG_PARAMETERS", "GIT_AUTHOR_DATE", "GIT_COMMITTER_DATE"} {
			if !slices.Contains(c.Unset, name) {
				t.Errorf("git %q inherits %s", c.Argv, name)
			}
		}
		want := time.Duration(0)
		if c.Argv[len(publishGitSettings)+1] == "push" {
			want = 10 * time.Minute
		}
		if c.Timeout != want {
			t.Errorf("git %q has timeout %v, want %v", c.Argv, c.Timeout, want)
		}
		if !c.KeepGroup {
			t.Errorf("git %q leaves taskmgr's process group: a Ctrl-C would leave it running", c.Argv)
		}
	}
	got := subcommands(runner.Calls())
	if !slices.Contains(got, "commit --quiet --no-verify -m taskmgr sync: laptop") {
		t.Errorf("git calls = %q, want a --no-verify commit", got)
	}
}

// A tag named machine/laptop makes the short name ambiguous, and git refuses
// the push; the full ref on both sides of the refspec is not.
func TestPublish_PushesTheFullRefspec(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	if _, err := publishWith(fs, e, runner); err != nil {
		t.Fatalf("publishWith: %v", err)
	}
	want := "push --quiet --no-verify origin refs/heads/machine/laptop:refs/heads/machine/laptop"
	if got := subcommands(runner.Calls()); !slices.Contains(got, want) {
		t.Errorf("git calls = %q, want %q", got, want)
	}
}

// The commit is a settled state only if no writer can run during the add;
// diff --cached and commit read only the index the add wrote, so holding a
// lock across them, or across the push, would stall every writer for nothing.
func TestPublish_LocksEveryStoreAroundTheAddOnly(t *testing.T) {
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
	if !before("lock beta", "git add") || !before("git add", "unlock alpha") || !before("git add", "unlock beta") {
		t.Errorf("the add is not inside both locks: %q", log)
	}
	for _, lock := range []string{"central", "alpha", "beta"} {
		if !before("unlock "+lock, "git diff") {
			t.Errorf("lock %s is held across diff --cached, commit and push: %q", lock, log)
		}
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

// A publish that waited for a busy store while holding the locks before it
// would stall every writer of those stores, and the registry, for as long as
// the busy one is held. It must release everything, wait holding nothing,
// and try again.
func TestPublish_BusyStoreReleasesEveryLockBeforeWaiting(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	fs.busy["beta"] = true
	p, err := loadPublish(fs, e, runner)
	if err != nil {
		t.Fatal(err)
	}
	waits := 0
	p.backoff = func() {
		waits++
		*g.log = append(*g.log, "backoff")
		delete(fs.busy, "beta")
	}
	res, err := p.publish()
	if err != nil || !res.Committed || !res.Pushed {
		t.Fatalf("publish with a busy store = %+v, %v; want a commit, pushed", res, err)
	}
	if waits != 1 {
		t.Errorf("backed off %d times, want once", waits)
	}
	log := *g.log
	backoff := slices.Index(log, "backoff")
	if backoff < 0 || !slices.Contains(log[:backoff], "busy beta") {
		t.Fatalf("no back-off after beta was busy: %q", log)
	}
	held := map[string]bool{}
	for _, entry := range log[:backoff] {
		if name, ok := strings.CutPrefix(entry, "lock "); ok {
			held[name] = true
		} else if name, ok := strings.CutPrefix(entry, "unlock "); ok {
			delete(held, name)
		}
	}
	delete(held, "publish") // held by design across the whole run
	if len(held) != 0 {
		t.Errorf("held %v while waiting for beta: %q", held, log)
	}
	after := log[backoff:]
	if i, j := slices.Index(after, "lock beta"), slices.Index(after, "git add"); i < 0 || j < i || !slices.Contains(after[:j], "lock central") {
		t.Errorf("the retry did not take every lock before the add: %q", log)
	}
}

// Two publishes that push at once make the remote refuse one ref update; the
// publish lock, which no writer takes, lets only one of them run.
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
	if res != (PublishResult{Machine: "laptop", Branch: "machine/laptop", RemoteName: "origin"}) {
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

// The explanation is the engine's; which refs were refused is git's to say.
func TestPublish_RejectedPushIsTheMachineIDClashWithGitsMessage(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.reject = true
	_, err := publishWith(fs, e, runner)
	if !errors.Is(err, ErrMachineIDClash) {
		t.Fatalf("rejected push: %v, want ErrMachineIDClash", err)
	}
	if !strings.Contains(err.Error(), "[rejected]        machine/laptop -> machine/laptop (fetch first)") {
		t.Errorf("rejected push: %v, want git's own message kept", err)
	}
}

func TestPublish_OtherPushFailureCarriesGitsMessage(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.fail = map[string]exec.Result{"push": exec.Deny(128, "fatal: could not read from remote repository")}
	_, err := publishWith(fs, e, runner)
	if err == nil || !strings.Contains(err.Error(), "could not read from remote repository") || errors.Is(err, ErrMachineIDClash) {
		t.Errorf("unreachable remote: %v, want git's own message", err)
	}
}

// git's message names only the host; the engine names the remote, the fix,
// and never the token in the URL.
func TestPublish_UnauthenticatedPushIsErrPublishAuthWithTheFix(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.remote = "https://hans:s3cret@github.com/hk9890/stores.git"
	g.fail = map[string]exec.Result{"push": exec.Deny(128, "fatal: could not read Username for 'https://github.com': terminal prompts disabled")}
	_, err := publishWith(fs, e, runner)
	if !errors.Is(err, ErrPublishAuth) {
		t.Fatalf("unauthenticated push: %v, want ErrPublishAuth", err)
	}
	for _, want := range []string{"https://github.com/hk9890/stores.git", "gh auth setup-git", "could not read Username"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("unauthenticated push: %v, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Errorf("unauthenticated push: %v, carries the token", err)
	}
}

func TestPublish_GitMissingIsReportedAsSuch(t *testing.T) {
	fs, e, _, _ := publishFixture(t, true)
	missing := &exec.Fake{Func: func(exec.Spec) exec.Result {
		return exec.Result{Category: exec.SpawnError, NotFound: true, Err: errors.New(`exec: "git": executable file not found in $PATH`)}
	}}
	if _, err := publishWith(fs, e, missing); !errors.Is(err, ErrGitMissing) {
		t.Errorf("publish without git: %v, want ErrGitMissing", err)
	}
	if _, err := initPublishWith("/remote.git", fs, e, missing); !errors.Is(err, ErrGitMissing) {
		t.Errorf("init without git: %v, want ErrGitMissing", err)
	}
}

// git that is installed but cannot be started — a work tree it cannot enter,
// a platform with no runner — must not send the user to install it.
func TestPublish_OtherSpawnFailureIsNotGitMissing(t *testing.T) {
	fs, e, _, _ := publishFixture(t, true)
	cause := errors.New("chdir /home/stores: permission denied")
	spawn := &exec.Fake{Func: func(exec.Spec) exec.Result {
		return exec.Result{Category: exec.SpawnError, Err: cause}
	}}
	_, err := publishWith(fs, e, spawn)
	if errors.Is(err, ErrGitMissing) || !errors.Is(err, cause) || !strings.Contains(err.Error(), "could not run git") {
		t.Errorf("publish where git cannot start: %v, want 'could not run git' with the cause", err)
	}
}

// Only exit 1 means "absent": a broken config is not a missing remote.
func TestPublish_OnlyExitOneReadsAsAbsent(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.fail = map[string]exec.Result{"config": exec.Deny(128, "fatal: bad config line 3")}
	_, err := publishWith(fs, e, runner)
	if err == nil || !strings.Contains(err.Error(), "bad config line 3") || errors.Is(err, ErrNotPublishing) {
		t.Errorf("broken config: %v, want git's error, not a missing remote", err)
	}
}

func TestPublish_BeforeInitRunsNoGit(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, false)
	_, err := publishWith(fs, e, runner)
	if !errors.Is(err, ErrNotPublishing) || !strings.Contains(err.Error(), "taskmgr sync init --remote <url>") {
		t.Errorf("publish before init: %v, want ErrNotPublishing naming sync init", err)
	}
	if n := len(runner.Calls()); n != 0 {
		t.Errorf("ran git %d times before init", n)
	}
}

func TestPublish_NoRemoteNamesInit(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.remote = ""
	_, err := publishWith(fs, e, runner)
	if !errors.Is(err, ErrNotPublishing) || !strings.Contains(err.Error(), `has no remote "origin"`) {
		t.Errorf("publish without origin: %v, want ErrNotPublishing naming origin", err)
	}
}

func TestPublish_RefusesAnotherMachinesBranch(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.head, g.commits = "refs/heads/machine/desktop", 1
	_, err := publishWith(fs, e, runner)
	if !errors.Is(err, ErrPublishBranch) || !strings.Contains(err.Error(), "machine_id changed") || !strings.Contains(err.Error(), "branch -m machine/laptop") {
		t.Errorf("publish on another machine's branch: %v", err)
	}
	g.head = ""
	if _, err := publishWith(fs, e, runner); !errors.Is(err, ErrPublishBranch) || !strings.Contains(err.Error(), "is not on a branch") {
		t.Errorf("publish detached: %v", err)
	}
}

// A branch that is no machine's is not a changed machine ID: stores/ was under
// git before the init, or an init stopped after `git init`. The message says
// which branch it is on and the fix for the state it is in.
func TestPublish_NonMachineBranchNamesTheFixThatFits(t *testing.T) {
	for _, tc := range []struct {
		name     string
		commits  int
		branches map[string]bool
		fix      string
	}{
		{"commits, no machine branch", 1, nil, "git -C /home/stores branch -m machine/laptop"},
		{"commits, machine branch exists", 1, map[string]bool{"refs/heads/machine/laptop": true}, "git -C /home/stores checkout machine/laptop"},
		{"unborn", 0, nil, "run 'taskmgr sync init --remote <url>' again"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs, e, runner, g := publishFixture(t, true)
			g.head, g.commits, g.branches = "refs/heads/main", tc.commits, tc.branches
			_, err := publishWith(fs, e, runner)
			if !errors.Is(err, ErrPublishBranch) {
				t.Fatalf("publish on main: %v, want ErrPublishBranch", err)
			}
			msg := err.Error()
			for _, want := range []string{"on branch main, not machine/laptop", "sync publishes only from machine/laptop", tc.fix} {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q lacks %q", msg, want)
				}
			}
			if strings.Contains(msg, "machine_id changed") {
				t.Errorf("message %q blames a machine_id change", msg)
			}
		})
	}
}

// --short would print heads/machine/laptop when a tag of that name exists, so
// the comparison is on the full ref.
func TestPublish_ComparesTheFullRef(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.head = "refs/tags/machine/laptop"
	if _, err := publishWith(fs, e, runner); !errors.Is(err, ErrPublishBranch) {
		t.Errorf("HEAD at a tag of the branch's name: %v, want ErrPublishBranch", err)
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

// A no-op publish writes nothing, the exclude file included.
func TestPublish_CurrentExcludesAreNotRewritten(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	exclude := filepath.Join(publishStores, ".git", "info", "exclude")
	if err := fs.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteAtomic(exclude, publishExcludes(vfs.TempPrefix), 0o644); err != nil {
		t.Fatal(err)
	}
	fs.FailOn("WriteAtomic", exclude, errors.New("exclude rewritten"))
	if _, err := publishWith(fs, e, runner); err != nil {
		t.Errorf("publish with current excludes: %v, want no write", err)
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
	fs.FailOn("TryLock", filepath.Join(publishStores, "beta", lockFileName), errors.New("ENOLCK"))
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
	want := PublishInfo{Machine: "laptop", Branch: "machine/laptop", Path: publishStores, Remote: "/remote.git", RemoteName: "origin"}
	if info != want {
		t.Errorf("info = %+v, want %+v", info, want)
	}
	wantCalls := []string{
		"init --quiet",
		"rev-parse --verify --quiet HEAD",
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

// An init that stopped after `git init` left HEAD unborn on git's default
// branch; running init again re-points it instead of refusing.
func TestInitPublish_RepointsAnUnbornHead(t *testing.T) {
	fs, e, runner, g := publishFixture(t, false)
	if err := fs.MkdirAll(filepath.Join(publishStores, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	g.head = "refs/heads/master"
	if _, err := initPublishWith("/remote.git", fs, e, runner); err != nil {
		t.Fatalf("init over an unborn HEAD on master: %v", err)
	}
	if g.head != "refs/heads/machine/laptop" {
		t.Errorf("HEAD = %q, want refs/heads/machine/laptop", g.head)
	}
}

// A HEAD with commits is the user's: init refuses it as publish does.
func TestInitPublish_RefusesABranchWithCommits(t *testing.T) {
	fs, e, runner, g := publishFixture(t, false)
	if err := fs.MkdirAll(filepath.Join(publishStores, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	g.head, g.commits = "refs/heads/main", 1
	if _, err := initPublishWith("/remote.git", fs, e, runner); !errors.Is(err, ErrPublishBranch) {
		t.Fatalf("init over main with a commit: %v, want ErrPublishBranch", err)
	}
	if g.head != "refs/heads/main" {
		t.Errorf("HEAD = %q, want it left on main", g.head)
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

// The configured remote keeps its credentials, since git needs them to push;
// the returned Remote is for display and drops them.
func TestInitPublish_RemoteOmitsUserinfo(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://u:secret@127.0.0.1:9/x.git", "https://127.0.0.1:9/x.git"},
		{"https://127.0.0.1:9/x.git", "https://127.0.0.1:9/x.git"},
		{"git@github.com:you/stores.git", "git@github.com:you/stores.git"},
	} {
		fs, e, runner, g := publishFixture(t, false)
		info, err := initPublishWith(tc.in, fs, e, runner)
		if err != nil || info.Remote != tc.want {
			t.Errorf("init --remote %q: remote %q, %v; want %q", tc.in, info.Remote, err, tc.want)
		}
		if g.remote != tc.in {
			t.Errorf("init --remote %q configured origin %q, want the URL as given", tc.in, g.remote)
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

// loadPublish is a publisher with its configuration loaded, for a test that
// replaces its back-off or takes its publish lock itself.
func loadPublish(fs vfs.FS, e env.Environment, runner exec.Runner, opts ...Option) (*publisher, error) {
	p := newPublisher(fs, e, runner, opts)
	return p, p.load()
}

func TestLoadPublish_MachineIDConfiguredElseHostname(t *testing.T) {
	fs, e, _, _ := publishFixture(t, false)
	if p, err := loadPublish(fs, e, nil); err != nil || p.id != "laptop" {
		t.Errorf("machine ID from the hostname = %q, %v", p.id, err)
	}
	if err := updateGlobalConfig(fs, e, func(g *GlobalConfig) error { g.MachineID = "desk-1"; return nil }); err != nil {
		t.Fatal(err)
	}
	if p, err := loadPublish(fs, e, nil); err != nil || p.id != "desk-1" {
		t.Errorf("machine ID from the config = %q, %v", p.id, err)
	}
}

// holdPublishLock takes the publish lock as a publish with PID 4711, started
// at 14:02:00Z, would, and returns its release.
func holdPublishLock(t *testing.T, fs vfs.FS, e env.Fake) func() error {
	t.Helper()
	e.Pid = 4711
	at := time.Date(2026, 11, 13, 14, 2, 0, 0, time.UTC)
	holder, err := loadPublish(fs, e, nil, WithClock(func() time.Time { return at }))
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := holder.lockPublish()
	if err != nil {
		t.Fatalf("lockPublish: %v", err)
	}
	return unlock
}

// A timer that fires while another sync runs must not queue behind it: it
// refuses at once, naming the run that holds the lock.
func TestPublish_BusyPublishLockRefusesNamingTheHolder(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	unlock := holdPublishLock(t, fs, e)
	const want = "another taskmgr sync is running (pid 4711, since 2026-11-13T14:02:00Z)"
	if _, err := publishWith(fs, e, runner); !errors.Is(err, ErrPublishBusy) || err.Error() != want {
		t.Errorf("publish while another runs: %v, want %q", err, want)
	}
	if _, err := initPublishWith("/remote.git", fs, e, runner); !errors.Is(err, ErrPublishBusy) || err.Error() != want {
		t.Errorf("init while a publish runs: %v, want %q", err, want)
	}
	if n := len(runner.Calls()); n != 0 {
		t.Errorf("a refused publish ran git %d times", n)
	}

	// The lock, not the content, decides: the content the holder leaves
	// behind does not refuse the next publish.
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := publishWith(fs, e, runner); err != nil {
		t.Errorf("publish after the holder released: %v", err)
	}
}

// The holder's content is informative: a lock file that does not name one —
// not yet written, cut short — still refuses, with the bare message.
func TestPublish_BusyPublishLockWithoutAHolderStillRefuses(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	fs.busy["publish"] = true
	if err := fs.WriteInPlace(filepath.Join("/home", publishLockName), []byte("pid: 47"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := publishWith(fs, e, runner); !errors.Is(err, ErrPublishBusy) || err.Error() != ErrPublishBusy.Error() {
		t.Errorf("publish under a lock with no holder named: %v, want %q", err, ErrPublishBusy)
	}
}

func TestPublish_WritesTheHolderIntoTheLockFile(t *testing.T) {
	fs, e, _, _ := publishFixture(t, true)
	unlock := holdPublishLock(t, fs, e)
	defer func() { _ = unlock() }()
	got, err := fs.ReadFile(filepath.Join("/home", publishLockName))
	if err != nil || string(got) != "pid: 4711\nsince: 2026-11-13T14:02:00Z\n" {
		t.Errorf("lock file = %q, %v; want the holder's pid and start", got, err)
	}
}

// A dead connection must end an unattended run: the seam kills the push, and
// the error says so, with what git said before it was killed.
func TestPublish_PushTimeoutEndsTheRun(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.fail = map[string]exec.Result{"push": {Category: exec.Timeout, Stderr: []byte("Connecting to example.com...\n"), Err: errors.New("signal: terminated")}}
	_, err := publishWith(fs, e, runner)
	if err == nil || !strings.Contains(err.Error(), "push timed out after 10m0s: Connecting to example.com...") {
		t.Errorf("hung push: %v, want 'push timed out after 10m0s' with git's stderr", err)
	}
}

// A holder that ended leaves an empty lock file, so a refusal by a later
// holder that has not written its own content yet never names the one that
// is gone.
func TestPublish_ReleaseEmptiesTheLockFile(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	if _, err := publishWith(fs, e, runner); err != nil {
		t.Fatalf("publishWith: %v", err)
	}
	path := filepath.Join("/home", publishLockName)
	if got, err := fs.ReadFile(path); err != nil || len(got) != 0 {
		t.Errorf("lock file after the publish = %q, %v; want it empty", got, err)
	}
	fs.busy["publish"] = true
	if _, err := publishWith(fs, e, runner); !errors.Is(err, ErrPublishBusy) || err.Error() != ErrPublishBusy.Error() {
		t.Errorf("refusal after a holder ended: %v, want the bare %q", err, ErrPublishBusy)
	}
}

// Exit 1 is an answer only from the questions gitAnswer names: a commit that
// exits 1 failed, and the publish says so rather than reading it as "absent".
func TestPublish_CommitExitOneIsAFailure(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.fail = map[string]exec.Result{"commit": exec.Deny(1, "error: unable to write new index file")}
	res, err := publishWith(fs, e, runner)
	if err == nil || !strings.Contains(err.Error(), "unable to write new index file") {
		t.Errorf("commit exiting 1: %+v, %v; want git's error", res, err)
	}
	if got := subcommands(runner.Calls()); slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "push") }) {
		t.Errorf("pushed after a failed commit: %q", got)
	}
}
