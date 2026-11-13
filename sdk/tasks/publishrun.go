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

// publishrun.go — publishing the central stores to a git remote, the
// imperative shell (SDK-SPEC §1).
//
// The git work tree is <central_root>/stores itself, on the branch
// machine/<machine-id>. Only this machine ever pushes that branch, so a push is
// always a fast-forward: there is never a merge, a rebase or a force-push, and a
// push that is not a fast-forward is reported as the misconfiguration it is.
//
// git reads every store file and writes the work tree's .git, so it runs here,
// in the engine, through the exec seam — never from a front end. The add runs
// under the central-root lock and the write lock of every registered central
// store, taken without waiting: a busy one makes the publish release them all
// and try again, for at most publishLockWaitMax. The commit reads only the
// index the add wrote, so it runs after every one of them is released, as does
// the push, the one network call, within publishPushTimeout. A
// publish lock no writer takes runs one publish at a time: a second one is
// refused with ErrPublishBusy rather than queued.
//
// Every stage, every git run and the outcome are logged (MONITORING.md).
package tasks

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// storeLockRetry is how long a publish waits, holding nothing, before it tries
// the store locks again after finding one busy.
const storeLockRetry = 50 * time.Millisecond

// publishLockWaitWarn is how long a publish waits for a busy store before it
// logs publish_lock_wait, once, naming the store.
const publishLockWaitWarn = 5 * time.Second

// publishLockWaitMax is how long a publish waits for a busy store, or a busy
// central root, before it gives up. A publish runs unattended, from a timer, so
// a store a hung writer holds must end the run, as a hung push does, rather
// than keep it retrying with the publish lock held.
const publishLockWaitMax = 2 * time.Minute

// InitPublish makes <central_root>/stores a git work tree on the branch
// machine/<machine-id> that publishes to remote as "origin" (SDK-SPEC §1). A
// relative local path is resolved against the working directory. The returned
// PublishInfo.Remote is the URL without its user and password. Running it
// again with the same remote changes nothing; a different one is refused.
// While a publish or another init runs it refuses with ErrPublishBusy. Of the
// options, WithLogger and WithClock apply.
func InitPublish(remote string, opts ...Option) (PublishInfo, error) {
	return initPublishWith(remote, vfs.NewOS(), env.NewOS(), exec.NewOS(), opts...)
}

func initPublishWith(remote string, fs vfs.FS, e env.Environment, runner exec.Runner, opts ...Option) (PublishInfo, error) {
	p := newPublisher(fs, e, runner, opts)
	start := p.now()
	info, err := p.initPublish(remote)
	if err != nil {
		p.logFailure(err)
		return PublishInfo{}, err
	}
	p.logger.LogAttrs(context.Background(), slog.LevelDebug, "publish_init",
		slog.String("machine", info.Machine),
		slog.String("branch", info.Branch),
		slog.String("remote", info.Remote),
		slog.Int64("duration_ms", p.now().Sub(start).Milliseconds()),
	)
	return info, nil
}

func (p *publisher) initPublish(remote string) (PublishInfo, error) {
	if err := p.load(); err != nil {
		return PublishInfo{}, err
	}
	if strings.TrimSpace(remote) == "" {
		return PublishInfo{}, invalid("remote", "a remote URL is required")
	}
	url := remote
	if localRelativeRemote(remote) {
		var err error
		if url, err = absViaSeam(remote, p.fs); err != nil {
			return PublishInfo{}, err
		}
	}
	if err := p.fs.MkdirAll(p.dir, 0o755); err != nil {
		return PublishInfo{}, err
	}
	p.enter(stageLock)
	unlock, err := p.lockPublish()
	if err != nil {
		return PublishInfo{}, err
	}
	defer func() { _ = unlock() }()

	p.enter(stageInit)
	if _, err := p.fs.Stat(filepath.Join(p.dir, ".git")); vfs.IsNotExist(err) {
		if _, err := p.git("init", "--quiet"); err != nil {
			return PublishInfo{}, err
		}
	} else if err != nil {
		return PublishInfo{}, err
	}
	if err := p.pointUnbornHead(); err != nil {
		return PublishInfo{}, err
	}
	if err := p.checkBranch(); err != nil {
		return PublishInfo{}, err
	}

	if cur, err := p.git("config", "--get", "remote."+publishRemote+".url"); err != nil {
		if !gitAbsent(err) {
			return PublishInfo{}, err
		}
		if _, err := p.git("remote", "add", publishRemote, url); err != nil {
			return PublishInfo{}, err
		}
	} else if cur != url {
		return PublishInfo{}, errRemoteChanged(p.dir, cur, url)
	}

	if err := p.writeExcludes(); err != nil {
		return PublishInfo{}, err
	}
	// Remote is for display, and nothing reads the credentials back from it.
	return PublishInfo{Machine: p.id, Branch: p.branch(), Path: p.dir, Remote: redactUserinfo(url), RemoteName: publishRemote}, nil
}

// Publish commits what changed under <central_root>/stores and pushes it to
// machine/<machine-id> (SDK-SPEC §1). The add is taken under the central-root
// lock and the write lock of every registered central store, so the commit is
// of a settled state; the commit and the push run with those released, and the
// push ends after publishPushTimeout.
// While another publish or an init runs it refuses with ErrPublishBusy. A run
// with nothing new commits nothing, and pushes only a commit an earlier,
// failed push left behind. Of the options, WithLogger and WithClock apply.
func Publish(opts ...Option) (PublishResult, error) {
	return publishWith(vfs.NewOS(), env.NewOS(), exec.NewOS(), opts...)
}

func publishWith(fs vfs.FS, e env.Environment, runner exec.Runner, opts ...Option) (PublishResult, error) {
	p := newPublisher(fs, e, runner, opts)
	if err := p.load(); err != nil {
		p.logFailure(err)
		return PublishResult{}, err
	}
	return p.publish()
}

// publish runs one loaded publish and logs its outcome: the summary record on
// success, publish_error on a failure.
func (p *publisher) publish() (PublishResult, error) {
	start := p.now()
	res, err := p.commitAndPush()
	if err != nil {
		p.logFailure(err)
		return PublishResult{}, err
	}
	p.logger.LogAttrs(context.Background(), slog.LevelDebug, "publish",
		slog.String("machine", res.Machine),
		slog.String("branch", res.Branch),
		slog.Bool("committed", res.Committed),
		slog.Bool("pushed", res.Pushed),
		slog.String("commit", res.Commit),
		slog.Int("retries", p.retries),
		slog.Int64("lock_wait_ms", p.lockWait.Milliseconds()),
		slog.Int64("duration_ms", p.now().Sub(start).Milliseconds()),
	)
	return res, nil
}

func (p *publisher) commitAndPush() (PublishResult, error) {
	if _, err := p.fs.Stat(filepath.Join(p.dir, ".git")); vfs.IsNotExist(err) {
		return PublishResult{}, errNotSetUp(p.dir)
	} else if err != nil {
		return PublishResult{}, err
	}
	p.enter(stageLock)
	unlock, err := p.lockPublish()
	if err != nil {
		return PublishResult{}, err
	}
	defer func() { _ = unlock() }()

	p.enter(stageCheck)
	remote, err := p.git("config", "--get", "remote."+publishRemote+".url")
	if err != nil {
		if !gitAbsent(err) {
			return PublishResult{}, err
		}
		return PublishResult{}, errNoRemote(p.dir)
	}
	if err := p.checkBranch(); err != nil {
		return PublishResult{}, err
	}
	if err := p.writeExcludes(); err != nil {
		return PublishResult{}, err
	}

	p.enter(stageCommit)
	out := PublishResult{Machine: p.id, Branch: p.branch(), RemoteName: publishRemote}
	if out.Committed, err = p.commit(); err != nil {
		return PublishResult{}, err
	}

	// The remote-tracking ref is what the last successful push left. Only this
	// machine pushes the branch, so a HEAD equal to it is already published and
	// the push — the only network call — is skipped.
	head, err := p.revision("HEAD")
	if err != nil {
		return PublishResult{}, err
	}
	if out.Committed {
		out.Commit = head
	}
	p.committed, p.newCommit = out.Committed, out.Commit
	published, err := p.revision("refs/remotes/" + publishRemote + "/" + p.branch())
	if err != nil {
		return PublishResult{}, err
	}
	if head != "" && head != published {
		p.enter(stagePush)
		// The full ref on both sides: a tag of the branch's name makes the short
		// name ambiguous, and git refuses to push it.
		if _, err := p.gitWithin(publishPushTimeout, "push", "--quiet", "--no-verify", publishRemote, p.ref()+":"+p.ref()); err != nil {
			var g *gitError
			if gitRejected(err) && errors.As(err, &g) {
				return PublishResult{}, errNotFastForward(p.dir, p.id, g.Message)
			}
			if gitAuthFailed(err) && errors.As(err, &g) {
				return PublishResult{}, errPushAuth(remote, g.Message)
			}
			return PublishResult{}, err
		}
		out.Pushed = true
	}
	return out, nil
}

// publisher is one publish's inputs and what it has done so far: the seams,
// the machine ID, the central root the stores directory is under, the logger
// and clock of the options, and the wait between two tries of the store locks,
// which an L2 test replaces so that it does not sleep.
type publisher struct {
	fs      vfs.FS
	env     env.Environment
	runner  exec.Runner
	logger  *slog.Logger
	now     func() time.Time
	backoff func()
	pid     int

	// Set by load.
	home  string
	croot string
	dir   string // <croot>/stores, the git work tree
	id    string

	// Set as the run goes, for its log records.
	stage     string        // the stage running
	retries   int           // tries of the store locks that found one busy
	lockWait  time.Duration // how long taking the store locks took
	committed bool          // this run made a commit
	newCommit string        // its full SHA; "" when committed is false
}

// newPublisher reads the options into a publisher. Option configures a Store,
// so they are applied to one that is never opened; only its logger and clock
// are read back. The clock defaults to the wall clock, not to the store's
// whole-second one, because it times the run as well as stamping the lock.
func newPublisher(fs vfs.FS, e env.Environment, runner exec.Runner, opts []Option) *publisher {
	o := Store{logger: discardLogger, now: time.Now}
	o.applyOptions(opts)
	return &publisher{
		fs: fs, env: e, runner: runner, logger: o.logger, now: o.now, pid: e.Getpid(),
		backoff: func() { time.Sleep(storeLockRetry) },
	}
}

// load reads the per-user config once for the machine ID and the central
// root. It writes nothing, so an invalid machine ID refuses before anything is
// created.
func (p *publisher) load() error {
	p.enter(stageConfig)
	home, cfg, croot, err := centralConfig(p.fs, p.env)
	if err != nil {
		return err
	}
	id, err := machineIDOf(cfg.MachineID, p.env.Hostname)
	if err != nil {
		return err
	}
	p.home, p.croot, p.dir, p.id = home, croot, filepath.Join(croot, storesSubdir), id
	return nil
}

// enter starts a stage, and logs that it has: the last publish_stage record
// of a run that hangs names where it hangs.
func (p *publisher) enter(stage string) {
	p.stage = stage
	p.logger.LogAttrs(context.Background(), slog.LevelDebug, "publish_stage", slog.String("stage", stage))
}

// logFailure records a failed publish or init with the stage it failed in. A
// busy publish lock is info, not error: a timer that fires while a manual sync
// runs expects it, and the next tick publishes. A failed push also says
// whether this run committed: that commit stays local until a push succeeds.
func (p *publisher) logFailure(err error) {
	level := slog.LevelError
	if errors.Is(err, ErrPublishBusy) {
		level = slog.LevelInfo
	}
	attrs := []slog.Attr{
		slog.String("stage", p.stage),
		slog.String("error", redactUserinfo(err.Error())),
	}
	if p.stage == stagePush {
		attrs = append(attrs, slog.Bool("committed", p.committed), slog.String("commit", p.newCommit))
	}
	p.logger.LogAttrs(context.Background(), level, "publish_error", attrs...)
}

func (p *publisher) branch() string { return machineBranch(p.id) }

// ref is the branch's full ref name.
func (p *publisher) ref() string { return "refs/heads/" + p.branch() }

// git runs one local git command in the work tree, without a time limit, and
// returns its stdout, trimmed.
func (p *publisher) git(args ...string) (string, error) { return p.gitWithin(0, args...) }

// gitWithin runs one git command in the work tree, killed after timeout when
// it is not zero, and returns its stdout, trimmed. stderr goes only into the
// error and the log, so a warning git prints never becomes a value the caller
// compares.
func (p *publisher) gitWithin(timeout time.Duration, args ...string) (string, error) {
	res := p.runner.Run(exec.Spec{
		Argv:    slices.Concat([]string{"git"}, publishGitSettings, args),
		Dir:     p.dir,
		Env:     publishEnv(p.id),
		Unset:   publishUnsetEnv,
		Timeout: timeout,
		// In taskmgr's process group, so a Ctrl-C ends the push with the
		// publish and its lock: a push left running would race the next one.
		// On Linux the seam also kills it when taskmgr dies by any signal.
		KeepGroup: true,
	})
	answer := res.Category == exec.Completed && res.ExitCode == 1 && gitAnswer(args)
	p.logGit(args, res, answer)
	switch res.Category {
	case exec.Completed:
		if res.ExitCode == 0 {
			return strings.TrimSpace(string(res.Stdout)), nil
		}
		msg := strings.TrimSpace(string(res.Stderr))
		if msg == "" {
			msg = strings.TrimSpace(string(res.Stdout))
		}
		return "", &gitError{Args: args, ExitCode: res.ExitCode, Message: msg, Answer: answer}
	case exec.SpawnError:
		if res.NotFound {
			return "", errGitMissing(res.Err)
		}
		return "", errGitSpawn(res.Err)
	case exec.Timeout:
		return "", errGitTimeout(args, timeout, res.Stderr)
	default:
		return "", errGitKilled(args, res.Err)
	}
}

// logGit records one git run. A success, and an answer (gitAnswer), are
// debug; any other exit, and git that did not exit on its own — not started,
// timed out, killed — are warn with git's stderr, and exit -1 for the last
// three.
func (p *publisher) logGit(args []string, res exec.Result, answer bool) {
	exit := res.ExitCode
	if res.Category != exec.Completed {
		exit = -1
	}
	attrs := []slog.Attr{
		slog.String("cmd", args[0]),
		slog.Int("exit", exit),
		slog.Int64("duration_ms", res.Duration.Milliseconds()),
	}
	if exit == 0 || answer {
		p.logger.LogAttrs(context.Background(), slog.LevelDebug, "publish_git", attrs...)
		return
	}
	attrs = append(attrs, slog.String("stderr", logStderr(res.Stderr)))
	if res.Err != nil {
		attrs = append(attrs, slog.String("error", redactUserinfo(res.Err.Error())))
	}
	p.logger.LogAttrs(context.Background(), slog.LevelWarn, "publish_git", attrs...)
}

// pointUnbornHead points a HEAD with no commit yet at this machine's branch,
// whatever branch it names. `git init --initial-branch` needs git 2.28, and
// this works on every version; it also repairs an init that stopped after
// `git init`, which left HEAD at git's default branch. An unborn HEAD has no
// commit to lose, so re-pointing it is safe; a HEAD with commits is left to
// checkBranch.
func (p *publisher) pointUnbornHead() error {
	head, err := p.revision("HEAD")
	if err != nil || head != "" {
		return err
	}
	_, err = p.git("symbolic-ref", "HEAD", p.ref())
	return err
}

// checkBranch refuses a work tree whose branch is not this machine's.
func (p *publisher) checkBranch() error {
	// The full ref, not --short: a tag of the same name makes --short print
	// heads/machine/<id>.
	ref, err := p.git("symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		if !gitAbsent(err) {
			return err
		}
		return errDetached(p.dir, p.id)
	}
	if ref == p.ref() {
		return nil
	}
	head, err := p.revision("HEAD")
	if err != nil {
		return err
	}
	exists, err := p.revision(p.ref())
	if err != nil {
		return err
	}
	return errWrongBranch(p.dir, ref, p.id, head == "", exists != "")
}

// writeExcludes writes the work tree's .git/info/exclude from the engine's
// own transient-file names, so the rules are current after any rename of one,
// and writes nothing when the file already holds them. It is the repository's
// own file, never committed, so the patterns cannot be edited away in a commit
// or travel to a reader of the mirror.
func (p *publisher) writeExcludes() error {
	path := filepath.Join(p.dir, ".git", "info", "exclude")
	want := publishExcludes(vfs.TempPrefix)
	cur, err := p.fs.ReadFile(path)
	if err == nil && bytes.Equal(cur, want) {
		return nil
	}
	if err != nil && !vfs.IsNotExist(err) {
		return err
	}
	if err := p.fs.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return p.fs.WriteAtomic(path, want, 0o644)
}

// commit stages and commits everything under the work tree, and reports
// whether it made a commit. Only the add runs under the store locks (stageAll):
// diff --cached and commit read the index the add wrote, never the work tree,
// so no writer waits on them.
func (p *publisher) commit() (committed bool, err error) {
	if err := p.stageAll(); err != nil {
		return false, err
	}
	if _, err := p.git("diff", "--cached", "--quiet"); err == nil {
		return false, nil
	} else if !gitAbsent(err) {
		return false, err
	}
	// Exit 1 from diff --quiet: something is staged.
	if _, err := p.git("commit", "--quiet", "--no-verify", "-m", "taskmgr sync: "+p.id); err != nil {
		return false, err
	}
	return true, nil
}

// stageAll runs git add --all while holding the central-root lock and the
// write lock of every registered central store, so the index holds a settled
// state of every store, and releases them all when the add ends.
func (p *publisher) stageAll() error {
	unlock, err := p.lockStores()
	if err != nil {
		return err
	}
	defer unlock()
	_, err = p.git("add", "--all")
	return err
}

// lockStores takes the central-root lock and the write lock of every
// registered central store, and returns the func that releases them all. It
// never waits while it holds one: when any is busy it releases every lock it
// took, waits holding nothing, and tries again, so a long write to one store
// does not stall the writers of every store locked before it. A wait that
// reaches publishLockWaitWarn is logged once, naming the busy store; one that
// reaches publishLockWaitMax gives up, naming it too.
func (p *publisher) lockStores() (unlock func(), err error) {
	start := p.now()
	warned := false
	for {
		unlock, busy, err := p.tryLockStores()
		waited := p.now().Sub(start)
		if unlock != nil || err != nil {
			p.lockWait = waited
			return unlock, err
		}
		if !warned && waited >= publishLockWaitWarn {
			warned = true
			p.logger.LogAttrs(context.Background(), slog.LevelWarn, "publish_lock_wait",
				slog.String("store", busy),
				slog.Int64("waited_ms", waited.Milliseconds()),
			)
		}
		if waited >= publishLockWaitMax {
			p.lockWait = waited
			return nil, errLockWaitOver(busy)
		}
		p.retries++
		p.backoff()
	}
}

// tryLockStores is one try of lockStores: the central-root lock, then the
// registry read under it, then each store's lock in name order. The central
// lock comes first, as in every registry operation, so no store is created,
// moved in or renamed while the add runs. A registered store whose
// directory is gone has nothing to lock and is skipped. When a lock is busy it
// returns no unlock and no error, holding nothing, and busy names the store,
// or busyCentralRoot; the returned func releases every lock taken, the central
// one last.
func (p *publisher) tryLockStores() (unlock func(), busy string, err error) {
	var held []func() error
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			_ = held[i]()
		}
	}
	central, ok, err := p.fs.TryLock(filepath.Join(p.croot, centralLockName))
	if err != nil || !ok {
		return nil, busyCentralRoot, err
	}
	held = append(held, central)

	entries, err := loadRegistry(p.fs, p.croot, p.home)
	if err != nil {
		release()
		return nil, "", err
	}
	names := make([]string, 0, len(entries))
	for _, en := range entries {
		names = append(names, en.Store)
	}
	slices.Sort(names)
	for _, name := range names {
		u, ok, err := p.fs.TryLock(filepath.Join(p.dir, name, lockFileName))
		if vfs.IsNotExist(err) {
			continue
		}
		if err != nil || !ok {
			release()
			return nil, name, err
		}
		held = append(held, u)
	}
	return release, "", nil
}

// lockPublish takes the lock that runs one publish at a time, push included,
// without waiting: when another publish or init holds it, it refuses with
// ErrPublishBusy, naming the holder the lock file describes. Without it two
// publishes push the same branch at once and the remote refuses one ref
// update; refusing rather than queueing keeps a timer from stacking runs
// behind a hung one. No writer takes it, so it stalls nothing but another
// publish.
//
// The holder writes its PID and start time into the lock file in place, which
// keeps the lock it holds on the file, and empties the file before it releases
// the lock, so a refusal never names a holder that has already ended. The
// lock, not the content, says whether a publish runs: a holder killed before
// it emptied the file leaves its content, which the next holder overwrites.
func (p *publisher) lockPublish() (func() error, error) {
	path := filepath.Join(p.croot, publishLockName)
	unlock, ok, err := p.fs.TryLock(path)
	if err != nil {
		return nil, err
	}
	if !ok {
		content, _ := p.fs.ReadFile(path)
		return nil, errPublishBusy(content)
	}
	if err := p.fs.WriteInPlace(path, publishHolder(p.pid, p.now()), 0o644); err != nil {
		_ = unlock()
		return nil, err
	}
	return func() error {
		cleared := p.fs.WriteInPlace(path, nil, 0o644)
		return errors.Join(cleared, unlock())
	}, nil
}

// revision returns the commit ref names, or "" when there is none — an unborn
// HEAD, or a branch never pushed.
func (p *publisher) revision(ref string) (string, error) {
	sha, err := p.git("rev-parse", "--verify", "--quiet", ref)
	if gitAbsent(err) {
		return "", nil
	}
	return sha, err
}
