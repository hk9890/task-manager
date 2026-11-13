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

// L3 for publishing (publishrun.go) against real git, a real temp dir and a
// bare repository as the remote, so the commit and push paths run without a
// network. What exec.Fake cannot show is here: that git reads the settings,
// environment and patterns the way the L2 tests assume it does.
package tasks

import (
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// publishL3 is one machine named "laptop" with one central store, "demo", and
// a bare remote.
type publishL3 struct {
	t      *testing.T
	fs     vfs.FS
	e      env.Fake
	stores string
	remote string
	store  *Store
}

func newPublishL3(t *testing.T) *publishL3 {
	t.Helper()
	base := t.TempDir()
	home, project := filepath.Join(base, "home"), filepath.Join(base, "project")
	for _, d := range []string{home, project} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := &publishL3{
		t:      t,
		fs:     vfs.NewOS(),
		e:      env.Fake{Vars: map[string]string{"TASKMGR_HOME": home}, Host: "laptop"},
		stores: filepath.Join(home, storesSubdir),
		remote: filepath.Join(base, "remote.git"),
	}
	s, err := initCentralWith(project, "demo", "dem", p.fs, p.e, nil)
	if err != nil {
		t.Fatalf("initCentralWith: %v", err)
	}
	p.store = s
	realGit(t, "", "init", "--quiet", "--bare", p.remote)
	return p
}

func (p *publishL3) init() (PublishInfo, error) {
	return initPublishWith(p.remote, p.fs, p.e, exec.NewOS())
}

func (p *publishL3) publish() (PublishResult, error) {
	return publishWith(p.fs, p.e, exec.NewOS())
}

func (p *publishL3) mustInit() {
	p.t.Helper()
	if _, err := p.init(); err != nil {
		p.t.Fatalf("initPublish: %v", err)
	}
}

func (p *publishL3) mustPublish() PublishResult {
	p.t.Helper()
	res, err := p.publish()
	if err != nil {
		p.t.Fatalf("publish: %v", err)
	}
	return res
}

func (p *publishL3) create(title string) string {
	p.t.Helper()
	res, err := p.store.Create(CreateInput{Title: title})
	if err != nil {
		p.t.Fatalf("Create: %v", err)
	}
	return res.Issue.ID
}

// realGit runs git in dir, fails the test on a non-zero exit, and returns the
// trimmed stdout.
func realGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := osexec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*osexec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr)
	}
	return strings.TrimSpace(string(out))
}

func TestInitPublish_L3_CreatesTheRepositoryAndIsIdempotent(t *testing.T) {
	p := newPublishL3(t)
	info, err := p.init()
	if err != nil {
		t.Fatalf("initPublish: %v", err)
	}
	want := PublishInfo{Machine: "laptop", Branch: "machine/laptop", Path: p.stores, Remote: p.remote, RemoteName: "origin"}
	if info != want {
		t.Errorf("info = %+v, want %+v", info, want)
	}
	if got := realGit(t, p.stores, "symbolic-ref", "HEAD"); got != "refs/heads/machine/laptop" {
		t.Errorf("HEAD = %q, want refs/heads/machine/laptop", got)
	}
	if got := realGit(t, p.stores, "remote", "get-url", "origin"); got != p.remote {
		t.Errorf("origin = %q, want %q", got, p.remote)
	}
	if _, err := os.Stat(filepath.Join(p.stores, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf("init wrote a .gitignore: %v", err)
	}

	if again, err := p.init(); err != nil || again != want {
		t.Errorf("second init = %+v, %v; want %+v", again, err, want)
	}
	_, err = initPublishWith(filepath.Join(t.TempDir(), "else.git"), p.fs, p.e, exec.NewOS())
	if err == nil || !strings.Contains(err.Error(), "already publishes to "+p.remote) {
		t.Errorf("init with another remote: %v", err)
	}
	if got := realGit(t, p.stores, "remote", "get-url", "origin"); got != p.remote {
		t.Errorf("origin = %q after a refused init, want it unchanged", got)
	}
}

// The patterns are checked by git itself against the names the engine uses
// for its transient files, so a pattern git reads differently from what it
// says fails here. check-ignore matches a path whether or not it exists.
func TestPublish_L3_ExcludesKeepTheEngineTransientFilesOut(t *testing.T) {
	p := newPublishL3(t)
	p.mustInit()
	rel := func(path string) string {
		r, err := filepath.Rel(p.stores, path)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	ignored := []string{
		rel(filepath.Join(p.store.Dir(), lockFileName)),
		rel(filepath.Join(p.store.Dir(), vfs.TempPrefix+"123")),
		rel(filepath.Join(p.store.Dir(), closedDirName, vfs.TempPrefix+"456")),
		rel(p.store.stagedContentPath("dem-0001")),
		stagingPrefix + "demo/" + ConfigFileName,
	}
	kept := []string{
		rel(filepath.Join(p.store.Dir(), ConfigFileName)),
		rel(p.store.filePath("dem-0001")),
		rel(p.store.contentPath("dem-0001")),
	}
	got := realGit(t, p.stores, append([]string{"check-ignore", "--no-index"}, append(ignored, kept...)...)...)
	lines := strings.Split(got, "\n")
	if strings.Join(lines, "\n") != strings.Join(ignored, "\n") {
		t.Errorf("git ignores %q, want exactly %q", lines, ignored)
	}
}

func TestPublish_L3_CommitsPushesAndSkipsWhatIsPublished(t *testing.T) {
	p := newPublishL3(t)
	p.mustInit()

	first := p.mustPublish()
	if !first.Committed || !first.Pushed || first.Commit == "" || first.Machine != "laptop" || first.Branch != "machine/laptop" {
		t.Fatalf("first publish = %+v, want a commit, pushed", first)
	}
	if got := realGit(t, p.remote, "rev-parse", "machine/laptop"); got != first.Commit {
		t.Errorf("remote branch at %s, want the commit publish reported, %s", got, first.Commit)
	}
	if got := realGit(t, p.stores, "log", "-1", "--format=%an <%ae> %s"); got != "taskmgr <laptop@taskmgr.invalid> taskmgr sync: laptop" {
		t.Errorf("commit = %q", got)
	}
	if got := realGit(t, p.stores, "ls-files"); strings.Contains(got, lockFileName) {
		t.Errorf("published a lock file:\n%s", got)
	}

	if again := p.mustPublish(); again != (PublishResult{Machine: "laptop", Branch: "machine/laptop", RemoteName: "origin"}) {
		t.Errorf("publish with nothing new = %+v, want no commit and no push", again)
	}

	p.create("a change")
	next := p.mustPublish()
	if !next.Committed || !next.Pushed || next.Commit == first.Commit {
		t.Fatalf("publish after a change = %+v, want a new commit, pushed", next)
	}
	if got := realGit(t, p.remote, "rev-parse", "machine/laptop"); got != next.Commit {
		t.Errorf("remote branch at %s, want %s", got, next.Commit)
	}

	// The work tree in stores/ does not disturb the registry.
	entries, err := storesWith(p.fs, p.e)
	if err != nil || len(entries) != 1 || entries[0].Health != StoreOK {
		t.Errorf("stores = %+v, %v; want the one store, ok", entries, err)
	}
}

// Two publishes on one machine — a timer and a manual run — must not push the
// branch at once: the remote would refuse one ref update with a raw git error.
// The second refuses while the first holds the publish lock. The test holds
// that lock itself, through a real flock, so the two certainly overlap.
func TestPublish_L3_ConcurrentPublishRefusesWhileOneRuns(t *testing.T) {
	p := newPublishL3(t)
	p.mustInit()
	p.create("a change")

	e := p.e
	e.Pid = os.Getpid()
	running, err := loadPublish(p.fs, e, exec.NewOS())
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := running.lockPublish()
	if err != nil {
		t.Fatalf("lockPublish: %v", err)
	}
	holder := fmt.Sprintf("(pid %d, since ", os.Getpid())
	if _, err := p.publish(); !errors.Is(err, ErrPublishBusy) || !strings.Contains(err.Error(), holder) {
		t.Errorf("publish while another runs: %v, want ErrPublishBusy naming %s…)", err, holder)
	}
	if _, err := p.init(); !errors.Is(err, ErrPublishBusy) {
		t.Errorf("init while a publish runs: %v, want ErrPublishBusy", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}

	// The holder's content stays in the file; the free lock lets the next run.
	if res := p.mustPublish(); !res.Committed || !res.Pushed {
		t.Errorf("publish after the first ended = %+v, want a commit, pushed", res)
	}
}

func TestPublish_L3_RejectedPushNamesTheMachineIDClash(t *testing.T) {
	p := newPublishL3(t)
	p.mustInit()
	p.mustPublish()

	// A second machine set up under the same ID publishes first.
	other := filepath.Join(t.TempDir(), "other")
	realGit(t, "", "clone", "--quiet", "--branch", "machine/laptop", p.remote, other)
	if err := os.WriteFile(filepath.Join(other, "elsewhere"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, other, "add", "--all")
	realGit(t, other, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "other")
	realGit(t, other, "push", "--quiet", "origin", "machine/laptop")
	pushed := realGit(t, p.remote, "rev-parse", "machine/laptop")

	p.create("a change")
	_, err := p.publish()
	if !errors.Is(err, ErrMachineIDClash) || !strings.Contains(err.Error(), "machine/laptop") {
		t.Fatalf("publish after another push: %v, want the clash naming machine/laptop", err)
	}
	if got := realGit(t, p.remote, "rev-parse", "machine/laptop"); got != pushed {
		t.Errorf("remote branch moved to %s; a rejected push must leave it at %s", got, pushed)
	}
}

// A branch amended here after it was published is this machine's own
// rewrite, not a second writer; the soft reset the error names makes the
// next publish succeed and keeps the store files.
func TestPublish_L3_RewrittenBranchNamesTheSoftReset(t *testing.T) {
	p := newPublishL3(t)
	p.mustInit()
	p.create("one")
	p.mustPublish()
	published := realGit(t, p.stores, "rev-parse", "HEAD")
	realGit(t, p.stores, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false",
		"commit", "--quiet", "--amend", "-m", "rewritten")

	p.create("two")
	_, err := p.publish()
	if err == nil || errors.Is(err, ErrMachineIDClash) || !strings.Contains(err.Error(), "reset --soft "+published) {
		t.Fatalf("publish after an amend: %v, want the soft reset to %s, not the clash", err, published)
	}
	realGit(t, p.stores, "reset", "--quiet", "--soft", published)
	p.mustPublish()
	if got := realGit(t, p.remote, "rev-parse", "machine/laptop^"); got != published {
		t.Errorf("remote branch parent = %s, want the published %s", got, published)
	}
}

func TestPublish_L3_ChangedMachineIDIsRefused(t *testing.T) {
	p := newPublishL3(t)
	p.mustInit()
	p.mustPublish() // a branch with a commit: init re-points only an unborn HEAD
	p.e.Host = "desktop"
	if _, err := p.publish(); !errors.Is(err, ErrPublishBranch) || !strings.Contains(err.Error(), "machine_id changed") || !strings.Contains(err.Error(), "machine/desktop") {
		t.Errorf("publish on a renamed machine: %v", err)
	}
	if _, err := p.init(); !errors.Is(err, ErrPublishBranch) || !strings.Contains(err.Error(), "machine_id changed") {
		t.Errorf("init on a renamed machine: %v", err)
	}
}

// An init that stopped after `git init` left HEAD unborn on git's default
// branch; running init again moves it to the machine's branch.
func TestInitPublish_L3_RepairsAnInterruptedInit(t *testing.T) {
	p := newPublishL3(t)
	realGit(t, p.stores, "-c", "init.defaultBranch=main", "init", "--quiet")
	p.mustInit()
	if got := realGit(t, p.stores, "symbolic-ref", "HEAD"); got != "refs/heads/machine/laptop" {
		t.Errorf("HEAD = %q after init over an interrupted one, want refs/heads/machine/laptop", got)
	}
	if res := p.mustPublish(); !res.Pushed {
		t.Errorf("publish after the repair = %+v, want pushed", res)
	}
}

// A tag named like the branch makes `git push origin machine/laptop`
// ambiguous; the full refspec still pushes the branch.
func TestPublish_L3_TagOfTheBranchNameDoesNotStopThePush(t *testing.T) {
	p := newPublishL3(t)
	p.mustInit()
	p.mustPublish()
	realGit(t, p.stores, "tag", "machine/laptop")
	p.create("a change")
	res := p.mustPublish()
	if !res.Pushed {
		t.Fatalf("publish with a tag of the branch's name = %+v, want pushed", res)
	}
	if got := realGit(t, p.remote, "rev-parse", "refs/heads/machine/laptop"); got != res.Commit {
		t.Errorf("remote branch at %s, want %s", got, res.Commit)
	}
}

// A store whose write lock another writer holds makes the publish wait for
// it holding nothing: the central-root lock stays free for every registry
// operation meanwhile.
func TestPublish_L3_BusyStoreHoldsNoLockWhileWaiting(t *testing.T) {
	p := newPublishL3(t)
	p.mustInit()
	unlock, err := p.fs.Lock(filepath.Join(p.store.Dir(), lockFileName))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := p.publish()
		done <- err
	}()
	time.Sleep(4 * storeLockRetry)
	select {
	case err := <-done:
		t.Fatalf("publish finished while the store was locked: %v", err)
	default:
	}
	// Each retry holds the central-root lock for a moment, so one busy probe can
	// land inside a retry; a publish that blocks under the lock is busy on every probe.
	centralLock := filepath.Join(filepath.Dir(p.stores), centralLockName)
	var central func() error
	var ok bool
	for range 10 {
		if central, ok, err = p.fs.TryLock(centralLock); err != nil || ok {
			break
		}
		time.Sleep(storeLockRetry / 5)
	}
	if err != nil || !ok {
		t.Errorf("central-root lock busy on every probe while the publish waits for a store: ok %v, %v", ok, err)
	} else if err := central(); err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("publish after the store was released: %v", err)
	}
}

// The snapshot is the engine's bytes: no line-ending conversion, no filter a
// global attributes file names, and no fsmonitor program, which would run
// under the store locks.
func TestPublish_L3_GlobalConversionsAndProgramsDoNotApply(t *testing.T) {
	p := newPublishL3(t)
	p.mustInit()
	crlf := []byte("one\r\ntwo\r\n")
	if err := os.WriteFile(filepath.Join(p.stores, "notes.txt"), crlf, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	monitor := filepath.Join(dir, "fsmonitor")
	if err := os.WriteFile(monitor, []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	attrs := filepath.Join(dir, "attributes")
	if err := os.WriteFile(attrs, []byte("* filter=upper\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(dir, "gitconfig")
	config := "[core]\n\tautocrlf = true\n\tsafecrlf = true\n\tattributesFile = " + attrs + "\n\tfsmonitor = " + monitor +
		"\n[filter \"upper\"]\n\tclean = touch " + marker + " && tr a-z A-Z\n"
	if err := os.WriteFile(global, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if res := p.mustPublish(); !res.Committed {
		t.Fatalf("publish = %+v, want a commit", res)
	}
	if got := realGit(t, p.stores, "cat-file", "blob", "HEAD:notes.txt"); got != strings.TrimSpace(string(crlf)) {
		t.Errorf("snapshot of notes.txt = %q, want the file's own bytes %q", got, crlf)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("a program from the user's git config ran during the publish: %v", err)
	}
}

// A publish run from a commit or rebase hook inherits that commit's dates;
// the snapshot must take the clock's instead.
func TestPublish_L3_InheritedCommitDatesAreIgnored(t *testing.T) {
	p := newPublishL3(t)
	p.mustInit()
	t.Setenv("GIT_AUTHOR_DATE", "2001-02-03T04:05:06Z")
	t.Setenv("GIT_COMMITTER_DATE", "2001-02-03T04:05:06Z")
	if res := p.mustPublish(); !res.Committed {
		t.Fatalf("publish = %+v, want a commit", res)
	}
	if got := realGit(t, p.stores, "log", "-1", "--format=%ad %cd", "--date=format:%Y"); strings.Contains(got, "2001") {
		t.Errorf("commit dates = %q, want the clock's, not the inherited 2001", got)
	}
}

// Once stores/ is a work tree, a missing git must still be reported as such,
// not as a missing remote or a detached work tree.
func TestPublish_L3_GitMissingIsReportedAsSuch(t *testing.T) {
	p := newPublishL3(t)
	p.mustInit()
	t.Setenv("PATH", t.TempDir())
	if _, err := p.publish(); !errors.Is(err, ErrGitMissing) {
		t.Errorf("publish without git: %v", err)
	}
	if _, err := p.init(); !errors.Is(err, ErrGitMissing) {
		t.Errorf("init without git: %v", err)
	}
}

// The user's global git config must neither stop an unattended publish (a
// failing hook) nor change what it publishes (a global excludes file).
func TestPublish_L3_GlobalGitConfigDoesNotApply(t *testing.T) {
	p := newPublishL3(t)
	p.mustInit()
	id := p.create("kept")
	hooks := t.TempDir()
	for _, hook := range []string{"prepare-commit-msg", "post-commit", "pre-push"} {
		if err := os.WriteFile(filepath.Join(hooks, hook), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	excludes := filepath.Join(t.TempDir(), "ignore")
	if err := os.WriteFile(excludes, []byte("*"+FileExt+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[core]\n\thooksPath = "+hooks+"\n\texcludesFile = "+excludes+"\n[commit]\n\tgpgSign = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if got := p.mustPublish(); !got.Committed || !got.Pushed {
		t.Errorf("publish with failing global hooks = %+v, want a commit, pushed", got)
	}
	if got := realGit(t, p.stores, "ls-files", "demo/"+id+FileExt); got == "" {
		t.Errorf("demo/%s%s is not in the snapshot: the global excludes file applied", id, FileExt)
	}
}

// A publish run from inside another repository's git hook inherits GIT_DIR and
// GIT_INDEX_FILE, and a shell profile can export the user's identity; neither
// may redirect the snapshot or sign it with the user's name.
func TestPublish_L3_InheritedGitEnvironmentIsIgnored(t *testing.T) {
	p := newPublishL3(t)
	p.mustInit()
	other := t.TempDir()
	realGit(t, other, "init", "--quiet")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))
	t.Setenv("GIT_AUTHOR_EMAIL", "someone@example.com")
	t.Setenv("GIT_COMMITTER_EMAIL", "someone@example.com")
	got, err := p.publish()
	for _, name := range []string{"GIT_DIR", "GIT_INDEX_FILE", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	if err != nil || !got.Committed || !got.Pushed {
		t.Fatalf("publish = %+v, %v; want a commit, pushed", got, err)
	}
	if log := realGit(t, p.stores, "log", "-1", "--format=%ae %ce"); log != "laptop@taskmgr.invalid laptop@taskmgr.invalid" {
		t.Errorf("commit identity = %q, want the fixed publish identity", log)
	}
	if _, err := os.Stat(filepath.Join(other, ".git", "index")); !os.IsNotExist(err) {
		t.Errorf("publish wrote the other repository's index: %v", err)
	}
}
