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
// in the engine, through the exec seam — never from a front end. The add and
// the commit run under the central-root lock and the write lock of every
// registered central store; the push, the one network call, runs after all of
// them are released. A publish lock no writer takes runs one publish at a time.
package tasks

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// MachineID returns this machine's ID (CONFIG-SPEC §2): the per-user config's
// machine_id, else one derived from the hostname. A machine_id that fails the
// grammar is a *ValidationError naming the key.
func MachineID() (string, error) {
	return machineIDWith(vfs.NewOS(), env.NewOS())
}

func machineIDWith(fs vfs.FS, e env.Environment) (string, error) {
	p, err := loadPublish(fs, e, nil)
	return p.id, err
}

// InitPublish makes <central_root>/stores a git work tree on the branch
// machine/<machine-id> that publishes to remote as "origin" (SDK-SPEC §1). A
// relative local path is resolved against the working directory. Running it
// again with the same remote changes nothing; a different one is refused.
func InitPublish(remote string) (PublishInfo, error) {
	return initPublishWith(remote, vfs.NewOS(), env.NewOS(), exec.NewOS())
}

func initPublishWith(remote string, fs vfs.FS, e env.Environment, runner exec.Runner) (PublishInfo, error) {
	p, err := loadPublish(fs, e, runner)
	if err != nil {
		return PublishInfo{}, err
	}
	if strings.TrimSpace(remote) == "" {
		return PublishInfo{}, invalid("remote", "a remote URL is required")
	}
	url := remote
	if localRelativeRemote(remote) {
		if url, err = absViaSeam(remote, fs); err != nil {
			return PublishInfo{}, err
		}
	}
	if err := fs.MkdirAll(p.dir, 0o755); err != nil {
		return PublishInfo{}, err
	}
	unlock, err := p.lockPublish()
	if err != nil {
		return PublishInfo{}, err
	}
	defer func() { _ = unlock() }()

	// `git init --initial-branch` needs git 2.28; pointing an unborn HEAD at
	// the branch works on every version.
	if _, err := fs.Stat(filepath.Join(p.dir, ".git")); vfs.IsNotExist(err) {
		if _, err := p.git("init", "--quiet"); err != nil {
			return PublishInfo{}, err
		}
		if _, err := p.git("symbolic-ref", "HEAD", "refs/heads/"+p.branch()); err != nil {
			return PublishInfo{}, err
		}
	} else if err != nil {
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
	return PublishInfo{Machine: p.id, Branch: p.branch(), Path: p.dir, Remote: url}, nil
}

// Publish commits what changed under <central_root>/stores and pushes it to
// machine/<machine-id> (SDK-SPEC §1). The commit is taken under the central-root
// lock and the write lock of every registered central store, so it is a settled
// state; the push runs with those released. A second publish waits for the
// first to finish. A run with nothing new commits nothing, and pushes
// only a commit an earlier, failed push left behind.
func Publish() (PublishResult, error) {
	return publishWith(vfs.NewOS(), env.NewOS(), exec.NewOS())
}

func publishWith(fs vfs.FS, e env.Environment, runner exec.Runner) (PublishResult, error) {
	p, err := loadPublish(fs, e, runner)
	if err != nil {
		return PublishResult{}, err
	}
	if _, err := fs.Stat(filepath.Join(p.dir, ".git")); vfs.IsNotExist(err) {
		return PublishResult{}, errNotSetUp(p.dir)
	} else if err != nil {
		return PublishResult{}, err
	}
	unlock, err := p.lockPublish()
	if err != nil {
		return PublishResult{}, err
	}
	defer func() { _ = unlock() }()
	if _, err := p.git("config", "--get", "remote."+publishRemote+".url"); err != nil {
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

	out := PublishResult{Machine: p.id, Branch: p.branch()}
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
	published, err := p.revision("refs/remotes/" + publishRemote + "/" + p.branch())
	if err != nil {
		return PublishResult{}, err
	}
	if head != "" && head != published {
		if _, err := p.git("push", "--quiet", "--no-verify", publishRemote, p.branch()); err != nil {
			if gitRejected(err) {
				return PublishResult{}, errNotFastForward(p.dir, p.id)
			}
			return PublishResult{}, err
		}
		out.Pushed = true
	}
	return out, nil
}

// publisher is one publish's resolved inputs: the seams, the machine ID, and
// the central root the stores directory is under.
type publisher struct {
	fs     vfs.FS
	runner exec.Runner
	home   string
	croot  string
	dir    string // <croot>/stores, the git work tree
	id     string
}

// loadPublish reads the per-user config once for the machine ID and the
// central root. It writes nothing, so an invalid machine ID refuses before
// anything is created.
func loadPublish(fs vfs.FS, e env.Environment, runner exec.Runner) (publisher, error) {
	home, err := taskmgrHome(e)
	if err != nil {
		return publisher{}, err
	}
	cfg, err := loadGlobalConfig(fs, home)
	if err != nil {
		return publisher{}, err
	}
	id, err := machineIDOf(cfg.MachineID, e.Hostname)
	if err != nil {
		return publisher{}, err
	}
	croot := centralRoot(cfg, home)
	return publisher{
		fs: fs, runner: runner, home: home, croot: croot,
		dir: filepath.Join(croot, storesSubdir), id: id,
	}, nil
}

func (p publisher) branch() string { return machineBranch(p.id) }

// git runs one git command in the work tree and returns its stdout, trimmed.
// stderr goes only into the error, so a warning git prints never becomes a
// value the caller compares. No timeout: every command but the push is local,
// and a push that hangs on the network is the user's to interrupt, which a
// process left in the caller's process group allows.
func (p publisher) git(args ...string) (string, error) {
	res := p.runner.Run(exec.Spec{
		Argv:  slices.Concat([]string{"git"}, publishGitSettings, args),
		Dir:   p.dir,
		Env:   publishIdentity(p.id),
		Unset: gitRepoEnv,
	})
	switch res.Category {
	case exec.Completed:
		if res.ExitCode == 0 {
			return strings.TrimSpace(string(res.Stdout)), nil
		}
		msg := strings.TrimSpace(string(res.Stderr))
		if msg == "" {
			msg = strings.TrimSpace(string(res.Stdout))
		}
		return "", &gitError{Args: args, ExitCode: res.ExitCode, Message: msg}
	case exec.SpawnError:
		return "", errGitMissing(res.Err)
	default:
		return "", errGitKilled(args, res.Err)
	}
}

// checkBranch refuses a work tree whose branch is not this machine's.
func (p publisher) checkBranch() error {
	// The full ref, not --short: a tag of the same name makes --short print
	// heads/machine/<id>.
	ref, err := p.git("symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		if !gitAbsent(err) {
			return err
		}
		return errDetached(p.dir, p.id)
	}
	if ref != "refs/heads/"+p.branch() {
		return errWrongBranch(p.dir, ref, p.id)
	}
	return nil
}

// writeExcludes rewrites the work tree's .git/info/exclude from the engine's
// own transient-file names, so the rules are current after any rename of one.
// It is the repository's own file, never committed, so the patterns cannot be
// edited away in a commit or travel to a reader of the mirror.
func (p publisher) writeExcludes() error {
	info := filepath.Join(p.dir, ".git", "info")
	if err := p.fs.MkdirAll(info, 0o755); err != nil {
		return err
	}
	return p.fs.WriteAtomic(filepath.Join(info, "exclude"), publishExcludes(vfs.TempPrefix), 0o644)
}

// commit stages and commits everything under the work tree while holding the
// central-root lock and the write lock of every registered central store, and
// reports whether it made a commit. The locks cover two local git commands and
// nothing else.
func (p publisher) commit() (committed bool, err error) {
	unlock, err := p.lockStores()
	if err != nil {
		return false, err
	}
	defer unlock()

	if _, err := p.git("add", "--all"); err != nil {
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

// lockStores takes the central-root lock, then reads the registry and takes the
// write lock of every store it holds, in name order. The central lock comes
// first, as in every registry operation, so no store is created, moved in or
// renamed while the commit runs; the name order means two holders of store
// locks never each wait on the other. A registered store whose directory is
// gone has nothing to lock and is skipped. The returned func releases every
// lock taken, the central one last.
func (p publisher) lockStores() (unlock func(), err error) {
	central, err := lockCentral(p.fs, p.croot)
	if err != nil {
		return nil, err
	}
	held := []func() error{central}
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			_ = held[i]()
		}
	}

	entries, err := loadRegistry(p.fs, p.croot, p.home)
	if err != nil {
		release()
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, en := range entries {
		names = append(names, en.Store)
	}
	slices.Sort(names)
	for _, name := range names {
		u, err := lockStoreDir(p.fs, filepath.Join(p.dir, name))
		if vfs.IsNotExist(err) {
			continue
		}
		if err != nil {
			release()
			return nil, err
		}
		held = append(held, u)
	}
	return release, nil
}

// lockPublish takes the lock that runs one publish at a time, push included.
// Without it two publishes push the same branch at once and the remote refuses
// one ref update. No writer takes it, so it stalls nothing but another publish.
func (p publisher) lockPublish() (func() error, error) {
	return p.fs.Lock(filepath.Join(p.croot, publishLockName))
}

// revision returns the commit ref names, or "" when there is none — an unborn
// HEAD, or a branch never pushed.
func (p publisher) revision(ref string) (string, error) {
	sha, err := p.git("rev-parse", "--verify", "--quiet", ref)
	if gitAbsent(err) {
		return "", nil
	}
	return sha, err
}
