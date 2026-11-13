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

// publish.go — publishing the central stores to a git remote, the pure half:
// the machine-ID rules (CONFIG-SPEC §2), the branch, the exclude rules, the
// git invocation's fixed settings and how its failures read (SDK-SPEC §1). The
// git it runs, and the locks it takes, are in publishrun.go; this file is pure
// and filesystem-free, so it unit-tests at L1.
package tasks

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// PublishInfo is what InitPublish set up (SDK-SPEC §1).
type PublishInfo struct {
	Machine string // the machine ID
	Branch  string // machine/<Machine>, the branch published to
	Path    string // <central_root>/stores, the git work tree
	Remote  string // the URL the remote "origin" publishes to
}

// PublishResult is what one Publish did (SDK-SPEC §1).
type PublishResult struct {
	Machine   string // the machine ID
	Branch    string // machine/<Machine>, the branch published to
	Committed bool   // this run made a commit
	Commit    string // its full SHA; "" when Committed is false
	Pushed    bool   // this run pushed
}

// publishRemote is the one remote a publish pushes to.
const publishRemote = "origin"

// machineIDRe is the machine-ID grammar (CONFIG-SPEC §2). It is a git ref
// component and an email local part as it stands, so neither needs escaping.
var machineIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func validMachineID(id string) bool { return machineIDRe.MatchString(id) }

func invalidMachineID(id string) *ValidationError {
	return invalid("machine_id", "%q is not a valid machine ID: lowercase letters, digits and '-', starting with a letter or digit, at most 63 characters", id)
}

// nonMachineIDRe matches the characters a derived machine ID replaces.
var nonMachineIDRe = regexp.MustCompile(`[^a-z0-9-]`)

// deriveMachineID turns a hostname into a machine ID candidate (CONFIG-SPEC §2):
// lowercased, cut at the first '.', every character outside [a-z0-9-] replaced
// by '-', and leading and trailing '-' trimmed. The result may still fail the
// grammar — an empty or overlong hostname does — which machineIDOf reports.
func deriveMachineID(hostname string) string {
	h, _, _ := strings.Cut(strings.ToLower(hostname), ".")
	return strings.Trim(nonMachineIDRe.ReplaceAllString(h, "-"), "-")
}

// machineIDOf returns the machine ID (CONFIG-SPEC §2): configured, the per-user
// config's machine_id, when it is set, else one derived from hostname, which is
// called only then.
func machineIDOf(configured string, hostname func() (string, error)) (string, error) {
	if configured != "" {
		if !validMachineID(configured) {
			return "", invalidMachineID(configured)
		}
		return configured, nil
	}
	host, err := hostname()
	if id := deriveMachineID(host); err == nil && validMachineID(id) {
		return id, nil
	}
	return "", fmt.Errorf("no machine ID can be derived from the hostname %q; set one with 'taskmgr config set machine_id <id> --global'", host)
}

func machineBranch(id string) string { return "machine/" + id }

// publishExcludes is what every publish writes to the work tree's
// .git/info/exclude: one pattern per file the engine creates and removes on its
// own, because a snapshot of one is a state no reader can use. Each is built
// from the name the engine itself uses, so a rename here cannot leave the
// mirror publishing the file under its new name. tempPrefix is the vfs seam's,
// which this pure file cannot import.
func publishExcludes(tempPrefix string) []byte {
	return []byte(strings.Join([]string{
		"# Rewritten by taskmgr on every publish: the storage engine's transient files.",
		"# Each store's advisory write lock.",
		"/*/" + lockFileName,
		"# The temp file of an atomic write, before its rename.",
		tempPrefix + "*",
		"# New body bytes staged before they replace a content sidecar.",
		"/*/" + contentDirName + "/" + stagedContentPrefix + "*" + stagedContentSuffix,
		"# A store assembled by a move before it is published.",
		"/" + stagingPrefix + "*/",
		"",
	}, "\n"))
}

// localRelativeRemote reports whether url is a relative local path, which git
// would resolve against the work tree rather than where the caller meant. A URL
// with a scheme, or in scp form (a ':' before any '/'), is not one.
func localRelativeRemote(url string) bool {
	if filepath.IsAbs(url) || strings.Contains(url, "://") {
		return false
	}
	if colon := strings.Index(url, ":"); colon > 0 {
		if slash := strings.Index(url, "/"); slash < 0 || colon < slash {
			return false
		}
	}
	return true
}

// publishGitSettings neutralizes the user's git settings that would stop an
// unattended publish or change what it publishes. --no-verify skips only
// pre-commit, commit-msg and pre-push, so hooks are turned off whole, a global
// core.hooksPath included; a global excludes file could silently keep store
// files out of the mirror; signing would need a key or a prompt.
var publishGitSettings = []string{
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.excludesFile=/dev/null",
	"-c", "commit.gpgSign=false",
	"-c", "push.gpgSign=false",
}

// gitRepoEnv is what `git rev-parse --local-env-vars` lists: the variables that
// point git at a repository, an index or a config other than the one in its
// working directory. A publish run from inside another repository's git hook
// inherits them, so every git command runs without them.
var gitRepoEnv = []string{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS",
	"GIT_CONFIG_COUNT", "GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE",
	"GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE", "GIT_INDEX_FILE",
	"GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX",
	"GIT_SHALLOW_FILE", "GIT_COMMON_DIR",
}

// publishIdentity is the fixed identity of every publish commit, in the
// variables git ranks above any config, so one the user exports is replaced.
func publishIdentity(id string) []string {
	email := id + "@taskmgr.invalid"
	return []string{
		"GIT_AUTHOR_NAME=taskmgr", "GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=taskmgr", "GIT_COMMITTER_EMAIL=" + email,
	}
}

// gitError is a git command that ran and exited non-zero. Message is git's own
// stderr — an unreachable host, a missing key, a rejected push are all its
// message to give — or its stdout when stderr is empty.
type gitError struct {
	Args     []string
	ExitCode int
	Message  string
}

func (e *gitError) Error() string {
	head := fmt.Sprintf("git %s: exit status %d", strings.Join(e.Args, " "), e.ExitCode)
	if e.Message == "" {
		return head
	}
	return head + ": " + e.Message
}

// gitAbsent reports whether err is git exiting 1: its answer "no such key" to
// config --get, "not a symbolic ref" to symbolic-ref --quiet, "no such ref" to
// rev-parse --verify --quiet, and "differences" to diff --quiet. Any other
// failure — git missing, a corrupt config, a broken repository — is a real one.
func gitAbsent(err error) bool {
	var g *gitError
	return errors.As(err, &g) && g.ExitCode == 1
}

// gitRejected reports whether err is a push the remote refused as not a
// fast-forward.
func gitRejected(err error) bool {
	var g *gitError
	return errors.As(err, &g) && strings.Contains(g.Message, "[rejected]")
}

// errGitMissing is git that could not be started at all, which in practice is
// git not installed.
func errGitMissing(cause error) error {
	return fmt.Errorf("git is not on PATH: publishing runs git, so git must be available (%v)", cause)
}

// errGitKilled is git that started and was killed by a signal.
func errGitKilled(args []string, cause error) error {
	return fmt.Errorf("git %s: %v", strings.Join(args, " "), cause)
}

func errNotSetUp(dir string) error {
	return fmt.Errorf("%s is not set up for sync; run 'taskmgr sync init --remote <url>' first", dir)
}

func errNoRemote(dir string) error {
	return fmt.Errorf("%s has no remote %q; run 'taskmgr sync init --remote <url>' first", dir, publishRemote)
}

// errRemoteChanged names `git remote remove`, not `set-url`: removing the remote
// also removes its remote-tracking ref, which a publish reads as what was last
// pushed, so the next one pushes the whole branch to the new remote.
func errRemoteChanged(dir, cur, url string) error {
	return fmt.Errorf("%s already publishes to %s, not %s; to publish to %s instead, run 'git -C %s remote remove %s' and then this command again",
		dir, cur, url, url, dir, publishRemote)
}

func errDetached(dir, id string) error {
	return fmt.Errorf("%s is not on a branch; check it out on %s", dir, machineBranch(id))
}

// errWrongBranch is a work tree on another machine's branch: the machine ID
// changed since the init, and publishing would push one machine's stores under
// another's name. ref is the full ref HEAD points at.
func errWrongBranch(dir, ref, id string) error {
	return fmt.Errorf("%s is on branch %s, but this machine's ID is %q (branch %s): machine_id changed since 'taskmgr sync init'. "+
		"Set machine_id back, or move the work tree to the new ID with 'git -C %s branch -m %s'",
		dir, strings.TrimPrefix(ref, "refs/heads/"), id, machineBranch(id), dir, machineBranch(id))
}

// errNotFastForward explains a rejected push. The branch has one writer by
// design, so a remote that holds commits this clone lacks has a second one.
func errNotFastForward(dir, id string) error {
	return fmt.Errorf("the remote rejected the push of %s: it holds commits this machine never made, so another machine publishes under the machine ID %q, or this machine was set up again. "+
		"Pick another ID with 'taskmgr config set machine_id <id> --global', move the work tree to it with 'git -C %s branch -m machine/<id>', and run 'taskmgr sync' again",
		machineBranch(id), id, dir)
}
