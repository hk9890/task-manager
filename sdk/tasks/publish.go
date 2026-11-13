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
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// PublishInfo is what InitPublish set up (SDK-SPEC §1).
type PublishInfo struct {
	Machine    string // the machine ID
	Branch     string // machine/<Machine>, the branch published to
	Path       string // <central_root>/stores, the git work tree
	Remote     string // the URL the remote RemoteName publishes to, without its user and password
	RemoteName string // the git remote's name, "origin"
}

// PublishState is this machine's sync state, as PublishStatus reads it without
// running a sync (SDK-SPEC §1).
type PublishState struct {
	SetUp        bool      // stores/ holds a .git and an init or a publish recorded its remote
	Machine      string    // the machine ID
	Branch       string    // machine/<Machine>, the branch published to
	Remote       string    // origin's URL as the last init or successful publish read it, without its user and password; "" before either
	LastSync     time.Time // when the last successful publish of Branch ended, whole seconds; zero when none is recorded
	Commit       string    // HEAD after that publish; "" when the branch had no commit
	Running      bool      // a publish or an init holds the publish lock now
	RunningPID   int       // its process ID; 0 when the lock file does not name one
	RunningSince time.Time // when it took the lock; zero when the lock file does not name it
}

// PublishResult is what one Publish did (SDK-SPEC §1).
type PublishResult struct {
	Machine    string // the machine ID
	Branch     string // machine/<Machine>, the branch published to
	RemoteName string // the git remote's name, "origin"
	Committed  bool   // this run made a commit
	Commit     string // its full SHA; "" when Committed is false
	Pushed     bool   // this run pushed
}

// Errors InitPublish and Publish refuse with (SDK-SPEC §1, §6). Each message
// adds the directory, the branch and the command that fixes it.
var (
	// ErrNotPublishing is a stores directory with no git repository, or one
	// with no remote "origin": `taskmgr sync init` has not run, or its remote
	// was removed since.
	ErrNotPublishing = errors.New("not set up for sync")
	// ErrPublishBranch is a work tree whose HEAD is not on this machine's
	// branch machine/<machine-id>: detached, or on another branch.
	ErrPublishBranch = errors.New("not on this machine's branch")
	// ErrMachineIDClash is a failed push to a branch that holds a commit this
	// work tree never made: the branch has a second writer.
	ErrMachineIDClash = errors.New("another machine publishes under this machine ID")
	// ErrGitMissing is git not found on PATH.
	ErrGitMissing = errors.New("git is not on PATH")
	// ErrPublishBusy is another publish or init holding the publish lock. The
	// message names its process ID and start time when the lock file holds
	// them.
	ErrPublishBusy = errors.New("another taskmgr sync is running")
)

// publishRemote is the one remote a publish pushes to.
const publishRemote = "origin"

// publishPushTimeout ends a push that has not finished. A publish runs
// unattended, from a timer, so a dead connection must end the run rather than
// hold the publish lock until someone notices; every other git command but
// the ls-remote (publishQueryTimeout) is local and runs without a limit. git
// cannot resume a push, so the limit is generous: 10 minutes carries about
// 75 MB at 1 Mbit/s. A stalled HTTPS transfer ends sooner, through the
// low-speed abort in publishGitSettings; ssh has only this limit.
const publishPushTimeout = 10 * time.Minute

// publishQueryTimeout ends an ls-remote after a failed push, which carries
// one line per ref, not a transfer: a remote that does not answer it within a
// minute is not asked again, and the push failure is reported as git gave it.
const publishQueryTimeout = time.Minute

// The stages of a publish and an init, in the order they start. Each is
// logged when it starts, so the last publish_stage record of a hung run names
// where it hangs, and a failure is logged with the stage it happened in
// (MONITORING.md).
const (
	stageConfig = "config" // read the per-user config and the machine ID
	stageLock   = "lock"   // take the publish lock
	stageInit   = "init"   // init only: create the repository and its remote
	stageCheck  = "check"  // publish only: the remote, the branch, the excludes
	stageCommit = "commit" // take the store locks, add, release them, commit
	stagePush   = "push"   // push the branch
	stageRecord = "record" // publish only: record the run for PublishStatus
)

// publishHolder is what the publish lock file holds while a publish runs: the
// holder's process ID and when it took the lock, so a refused publish can say
// which one is running. The lock, not this content, says whether one runs.
func publishHolder(pid int, since time.Time) []byte {
	return fmt.Appendf(nil, "pid: %d\nsince: %s\n", pid, since.UTC().Format(time.RFC3339))
}

// errPublishBusy is ErrPublishBusy naming the holder that content, the lock
// file's, describes. Content that does not name one — empty, half written,
// unreadable — still refuses, with the bare message.
func errPublishBusy(content []byte) error {
	pid, since, ok := parsePublishHolder(content)
	if !ok {
		return ErrPublishBusy
	}
	return fmt.Errorf("%w (pid %d, since %s)", ErrPublishBusy, pid, since.Format(time.RFC3339))
}

// parsePublishHolder reads the holder publishHolder wrote. Content that does
// not name one — empty, half written, unreadable — is not ok.
func parsePublishHolder(content []byte) (pid int, since time.Time, ok bool) {
	var rawPID, rawSince string
	for line := range strings.SplitSeq(string(content), "\n") {
		key, value, _ := strings.Cut(line, ": ")
		switch key {
		case "pid":
			rawPID = value
		case "since":
			rawSince = value
		}
	}
	pid, err := strconv.Atoi(rawPID)
	if err != nil || pid <= 0 {
		return 0, time.Time{}, false
	}
	since, err = time.Parse(time.RFC3339, rawSince)
	if err != nil {
		return 0, time.Time{}, false
	}
	return pid, since.UTC(), true
}

// publishRecord is the content of the publish state file (CONFIG-SPEC §3):
// what the last init or successful publish did.
type publishRecord struct {
	Remote   string    `yaml:"remote,omitempty"`
	Branch   string    `yaml:"branch,omitempty"`
	LastSync time.Time `yaml:"last_sync,omitempty"`
	Commit   string    `yaml:"commit,omitempty"`
}

// encodePublishRecord writes LastSync in UTC and whole seconds, as the publish
// lock holder writes its start time.
func encodePublishRecord(r publishRecord) ([]byte, error) {
	r.LastSync = r.LastSync.UTC().Truncate(time.Second)
	return yaml.Marshal(r)
}

// decodePublishRecord reads a publish state file. Content that is not a record
// is an error naming the file, never a zero record: a front end that shows
// "never synced" for a damaged file hides the damage.
func decodePublishRecord(path string, content []byte) (publishRecord, error) {
	var r publishRecord
	if err := yaml.Unmarshal(content, &r); err != nil {
		return publishRecord{}, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// userinfoRe matches the user and password of a URL with a scheme, as in
// https://user:token@host/path.
var userinfoRe = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/@\s]+@`)

// redactUserinfo removes the user and password from every URL with a scheme
// in s. A remote URL may carry a token, and an error or a log record must not
// repeat it. An scp-form address (git@host:path) names a user and no secret,
// and is left as it is.
func redactUserinfo(s string) string { return userinfoRe.ReplaceAllString(s, "$1") }

// gitAnswer reports whether exit 1 from git args is an answer rather than a
// failure: "no such key" from config --get, "differences" from diff --quiet,
// "no such ref" from rev-parse --verify, "not a symbolic ref" from
// symbolic-ref --quiet, and "not an ancestor" from merge-base --is-ancestor.
// It is the one place that says so: gitWithin marks
// such an exit gitError.Answer, which gitAbsent reads, and logs it at debug,
// not as a failed git run.
func gitAnswer(args []string) bool {
	if len(args) < 2 {
		return false
	}
	var flag string
	switch args[0] {
	case "config":
		flag = "--get"
	case "diff":
		flag = "--quiet"
	case "rev-parse":
		flag = "--verify"
	case "symbolic-ref":
		flag = "--quiet"
	case "merge-base":
		flag = "--is-ancestor"
	default:
		return false
	}
	return slices.Contains(args[1:], flag)
}

// logStderrMax bounds the git stderr a publish_git record carries: enough for
// git's message, not a whole transfer log.
const logStderrMax = 1024

// logStderr is git's stderr as a log field: trimmed, redacted, and cut at
// logStderrMax bytes.
func logStderr(stderr []byte) string {
	s := redactUserinfo(strings.TrimSpace(string(stderr)))
	if len(s) > logStderrMax {
		s = strings.ToValidUTF8(s[:logStderrMax], "") + "…"
	}
	return s
}

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
	const advice = "set one with 'taskmgr config set machine_id <id> --global'"
	host, err := hostname()
	if err != nil {
		return "", fmt.Errorf("no machine ID can be derived: reading the hostname failed: %w; %s", err, advice)
	}
	if id := deriveMachineID(host); validMachineID(id) {
		return id, nil
	}
	return "", fmt.Errorf("no machine ID can be derived from the hostname %q; %s", host, advice)
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
// unattended publish, change what it publishes, or run a program of the
// user's while the store locks are held. --no-verify skips only pre-commit,
// commit-msg and pre-push, so hooks are turned off whole, a global
// core.hooksPath included; a global excludes file could silently keep store
// files out of the mirror; line-ending conversion and the filters a global
// attributes file names would make the snapshot differ from the engine's
// bytes, and a filter runs a program; an fsmonitor is a program too; signing
// would need a key or a prompt; git's advice on a rejected push is to pull,
// which a publish must never do. The HTTP low-speed abort ends an HTTPS
// transfer that moves under 1000 bytes/s for 60 seconds, so a dead connection
// ends after a minute while a slow one goes on to publishPushTimeout.
var publishGitSettings = []string{
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.excludesFile=/dev/null",
	"-c", "core.attributesFile=/dev/null",
	"-c", "core.autocrlf=false",
	"-c", "core.safecrlf=false",
	"-c", "core.fsmonitor=false",
	"-c", "commit.gpgSign=false",
	"-c", "push.gpgSign=false",
	"-c", "advice.pushUpdateRejected=false",
	"-c", "http.lowSpeedLimit=1000",
	"-c", "http.lowSpeedTime=60",
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

// gitDateEnv is the dates git gives a commit in place of the clock. A publish
// run from a commit or rebase hook inherits the dates of that commit, and the
// snapshot would take them over.
var gitDateEnv = []string{"GIT_AUTHOR_DATE", "GIT_COMMITTER_DATE"}

// publishUnsetEnv is every inherited variable git runs without.
var publishUnsetEnv = slices.Concat(gitRepoEnv, gitDateEnv)

// publishEnv is what every publish git runs with on top of the inherited
// environment. The identity of every publish commit is fixed, in the variables
// git ranks above any config, so one the user exports is replaced.
// GIT_TERMINAL_PROMPT=0 makes a push that needs credentials fail at once with
// git's message, not wait on a terminal nobody watches until the push times
// out. GIT_ATTR_NOSYSTEM=1 drops the system attributes file, which
// core.attributesFile=/dev/null does not reach.
func publishEnv(id string) []string {
	email := id + "@taskmgr.invalid"
	return []string{
		"GIT_AUTHOR_NAME=taskmgr", "GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=taskmgr", "GIT_COMMITTER_EMAIL=" + email,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ATTR_NOSYSTEM=1",
	}
}

// gitError is a git command that ran and exited non-zero. Message is git's own
// stderr — an unreachable host, a missing key, a rejected push are all its
// message to give — or its stdout when stderr is empty. Answer is an exit 1
// that gitAnswer reads as an answer, not a failure.
type gitError struct {
	Args     []string
	ExitCode int
	Message  string
	Answer   bool
}

func (e *gitError) Error() string {
	head := fmt.Sprintf("git %s: exit status %d", strings.Join(e.Args, " "), e.ExitCode)
	if e.Message == "" {
		return redactUserinfo(head)
	}
	return redactUserinfo(head + ": " + e.Message)
}

// gitAbsent reports whether err is an exit 1 that gitAnswer reads as an
// answer: "no such key", "not a symbolic ref", "no such ref", "differences",
// "not an ancestor".
// Any other failure — an exit 1 from commit or push, git missing, a corrupt
// config, a broken repository — is a real one.
func gitAbsent(err error) bool {
	var g *gitError
	return errors.As(err, &g) && g.Answer
}

// errGitMissing is git that could not be started because it is not on PATH.
func errGitMissing(cause error) error {
	return fmt.Errorf("%w: publishing runs git, so git must be available (%w)", ErrGitMissing, cause)
}

// errGitSpawn is git that could not be started for another reason: a work
// tree it cannot enter, or a platform with no process runner.
func errGitSpawn(cause error) error {
	return fmt.Errorf("could not run git: %w", cause)
}

// errGitKilled is git that started and was killed by a signal.
func errGitKilled(args []string, cause error) error {
	return errors.New(redactUserinfo(fmt.Sprintf("git %s: %v", strings.Join(args, " "), cause)))
}

// errGitTimeout is git that the seam killed when timeout ran out, with what
// git wrote to stderr before then.
func errGitTimeout(args []string, timeout time.Duration, stderr []byte) error {
	msg := fmt.Sprintf("%s timed out after %s", args[0], timeout)
	if s := strings.TrimSpace(string(stderr)); s != "" {
		msg += ": " + s
	}
	return errors.New(redactUserinfo(msg))
}

// busyCentralRoot is the publish_lock_wait store value when the busy lock is
// the central root's, which registry writes hold. No store name has
// parentheses, so it cannot name a store.
const busyCentralRoot = "(central root)"

// errLockWaitOver is a publish that gave up after waiting for the lock of
// busy, a store name or busyCentralRoot. It names the limit, not the measured
// wait, which overshoots it by up to one back-off and try.
func errLockWaitOver(busy string) error {
	if busy == busyCentralRoot {
		return fmt.Errorf("gave up after waiting %s for the central-root lock, which a registry change holds; run 'taskmgr sync' again", publishLockWaitMax)
	}
	return fmt.Errorf("gave up after waiting %s for the write lock of store %q, which another writer holds; run 'taskmgr sync' again", publishLockWaitMax, busy)
}

func errNotSetUp(dir string) error {
	return fmt.Errorf("%s is %w; run 'taskmgr sync init --remote <url>' first", dir, ErrNotPublishing)
}

func errNoRemote(dir string) error {
	return fmt.Errorf("%s is %w: it has no remote %q; run 'taskmgr sync init --remote <url>' first", dir, ErrNotPublishing, publishRemote)
}

// errRemoteChanged names `git remote remove`, not `set-url`: removing the remote
// also removes its remote-tracking ref, which a publish reads as what was last
// pushed, so the next one pushes the whole branch to the new remote. URLs that
// differ only in their credentials name one remote, so `set-url` keeps that ref;
// the message cannot show either credential, so it says that they differ.
func errRemoteChanged(dir, cur, url string) error {
	cur, url = redactUserinfo(cur), redactUserinfo(url)
	if cur == url {
		return fmt.Errorf("%s already publishes to %s with other credentials in the URL; to use the new ones, run 'git -C %s remote set-url %s <url>' with the new URL",
			dir, url, dir, publishRemote)
	}
	return fmt.Errorf("%s already publishes to %s, not %s; to publish to %s instead, run 'git -C %s remote remove %s' and then this command again",
		dir, cur, url, url, dir, publishRemote)
}

func errDetached(dir, id string) error {
	return fmt.Errorf("%s is %w: it is not on a branch; check out %s with 'git -C %s checkout %s'",
		dir, ErrPublishBranch, machineBranch(id), dir, machineBranch(id))
}

// errWrongBranch is a work tree on a branch other than this machine's. ref is
// the full ref HEAD points at.
//
// On another machine's branch, the machine ID changed since the init, and
// publishing would push one machine's stores under another's name. On any
// other branch, stores/ was under git before the init, or an init stopped
// after `git init`: either way sync publishes only from machine/<id>, and the
// fix depends on the state. An unborn HEAD is what InitPublish re-points, so
// the fix is to run it again; otherwise machine/<id> is checked out when it
// exists (branchExists), and the current branch renamed to it when not.
func errWrongBranch(dir, ref, id string, unborn, branchExists bool) error {
	branch := strings.TrimPrefix(ref, "refs/heads/")
	if strings.HasPrefix(ref, "refs/heads/"+machineBranch("")) {
		return fmt.Errorf("%s is %w: it is on branch %s, but this machine's ID is %q (branch %s): machine_id changed since 'taskmgr sync init'. "+
			"Set machine_id back, or move the work tree to the new ID with 'git -C %s branch -m %s'",
			dir, ErrPublishBranch, branch, id, machineBranch(id), dir, machineBranch(id))
	}
	var fix string
	switch {
	case unborn:
		fix = fmt.Sprintf("it has no commit yet, so run 'taskmgr sync init --remote <url>' again, which moves it to %s", machineBranch(id))
	case branchExists:
		fix = fmt.Sprintf("check out that branch with 'git -C %s checkout %s'", dir, machineBranch(id))
	default:
		fix = fmt.Sprintf("rename the branch with 'git -C %s branch -m %s'", dir, machineBranch(id))
	}
	return fmt.Errorf("%s is %w: it is on branch %s, not %s, and sync publishes only from %s; %s",
		dir, ErrPublishBranch, branch, machineBranch(id), machineBranch(id), fix)
}

// errNotFastForward explains a push the remote's branch refused because it
// holds a commit this work tree never made (remoteDiverged), and adds git's
// own message, which names the refs. The branch has one writer by design, so
// such a commit has a second one.
func errNotFastForward(dir, id, gitMsg string) error {
	return fmt.Errorf("%w %q: the remote's %s holds a commit this machine never made, so another machine uses this ID, or this machine was set up again. "+
		"Pick another ID with 'taskmgr config set machine_id <id> --global', move the work tree to it with 'git -C %s branch -m machine/<id>', and run 'taskmgr sync' again. git said: %s",
		ErrMachineIDClash, id, machineBranch(id), dir, redactUserinfo(gitMsg))
}

// errPushFailed explains a push that git ended with an exit status and that
// is not a clash. Its cause — no network, a login git cannot make without a
// prompt, a remote that refuses — is in git's message, which this passes on
// and never reads. A sync runs without a terminal, so the hint is the push to
// run by hand, where git can ask.
//
// ref is the branch's full ref: the printed push names it on both sides, as
// the publish does, because a tag of the branch's name makes the short name
// ambiguous. --no-verify keeps the user's pre-push hook out of it, as well.
func errPushFailed(dir, ref string, urls []string, err error) error {
	redacted := make([]string, len(urls))
	for i, url := range urls {
		redacted[i] = redactUserinfo(url)
	}
	return fmt.Errorf("push of %s to %s failed. A sync never lets git prompt, so git must reach the remote and log in on its own; "+
		"run 'git -C %s push --no-verify %s %s:%s' to see what git needs. The commit is kept, and the next sync pushes it: %w",
		strings.TrimPrefix(ref, "refs/heads/"), strings.Join(redacted, ", "), dir, publishRemote, ref, ref, err)
}

// errBranchRewritten explains a push the remote's branch refused because its
// tip is a commit this work tree has but HEAD does not descend from: the
// branch was amended or reset here after it was published. A publish never
// forces. A soft reset to the tip makes the branch descend from it again and
// keeps every store file as it is, so the next sync commits the difference.
func errBranchRewritten(dir, id, tip, gitMsg string) error {
	return fmt.Errorf("%s was rewritten in %s after it was published: the remote's tip %s is not in its history, and a sync never forces. "+
		"Run 'git -C %s reset --soft %s', which keeps the store files, then 'taskmgr sync'. git said: %s",
		machineBranch(id), dir, tip, dir, tip, redactUserinfo(gitMsg))
}
