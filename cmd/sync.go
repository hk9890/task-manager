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

// sync.go — `taskmgr sync`: publishing this machine's central stores to a git
// remote (CLI-SPEC §2.5).
//
// The git work tree is <central_root>/stores itself, on the branch
// machine/<machine-id>. Only this machine ever pushes that branch, so a push is
// always a fast-forward: there is never a merge, a rebase or a force-push, and a
// push that is not a fast-forward is reported as the misconfiguration it is.
//
// The SDK knows nothing of this. It supplies the directory
// (tasks.CentralStoresDir) and carries the machine_id key; git is spawned here,
// through gitOutput (pkgrepo.go).
package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// syncDTO is what `sync` prints (CLI-SPEC §6).
type syncDTO struct {
	Machine   string `json:"machine"`
	Branch    string `json:"branch"`
	Committed bool   `json:"committed"`
	Commit    string `json:"commit,omitempty"`
	Pushed    bool   `json:"pushed"`
}

// syncInitDTO is what `sync init` prints (CLI-SPEC §6).
type syncInitDTO struct {
	Machine string `json:"machine"`
	Branch  string `json:"branch"`
	Path    string `json:"path"`
	Remote  string `json:"remote"`
}

// syncRemote is the one remote sync publishes to.
const syncRemote = "origin"

// syncGitignore is written to <central_root>/stores/.gitignore by `sync init`.
// Every pattern names a file the storage engine creates and removes on its own,
// so publishing one would publish a state no reader can use.
const syncGitignore = `# Written by 'taskmgr sync init': the storage engine's transient files.
# Each store's advisory write lock.
/*/.lock
# The temp file of an atomic write, before its rename.
.tmp-*
# New body bytes staged before they replace a content sidecar.
/*/content/.*.incoming
# A store assembled by 'taskmgr store move' before it is published.
/.incoming-*/
`

var syncInitFlags struct{ remote string }

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Publish this machine's central stores to its git remote",
	Long: `Commit every change under <central_root>/stores and push it to the branch
machine/<machine-id> of the remote 'sync init' configured.

Each machine publishes its own branch and nothing else pushes it, so a push is
always a fast-forward. A push the remote rejects means another machine uses
this machine ID, or this machine was set up again: pick another machine_id.

A run with nothing new commits nothing and reaches no network. Project-local
.tasks stores are not published.`,
	Example: "taskmgr sync",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSync()
	},
}

var syncInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Make the central stores a git work tree that publishes to <url>",
	Long: `Make <central_root>/stores a git repository on the branch machine/<machine-id>,
with <url> as its remote 'origin', and write a .gitignore that keeps the
engine's transient files out of it.

The machine ID is the per-user config key machine_id, or the hostname when it is
unset. Running init again with the same remote changes nothing; a different
remote is refused rather than overwritten.`,
	Example: "taskmgr sync init --remote git@github.com:you/taskmgr-stores.git",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return initSync(syncInitFlags.remote)
	},
}

// initSync makes the central stores directory a work tree publishing to url.
func initSync(url string) error {
	id, err := machineID()
	if err != nil {
		return err
	}
	branch := machineBranch(id)
	dir, err := tasks.CentralStoresDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	url, err = absRemote(url)
	if err != nil {
		return err
	}

	// `git init --initial-branch` needs git 2.28; pointing an unborn HEAD at
	// the branch works on every version.
	if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if _, err := syncGit(dir, id, "init", "--quiet"); err != nil {
			return err
		}
		if _, err := syncGit(dir, id, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := checkSyncBranch(dir, id); err != nil {
		return err
	}

	if cur, err := syncGit(dir, id, "config", "--get", "remote."+syncRemote+".url"); err != nil {
		if !gitAbsent(err) {
			return err
		}
		if _, err := syncGit(dir, id, "remote", "add", syncRemote, url); err != nil {
			return err
		}
	} else if cur != url {
		return fmt.Errorf("%s already publishes to %s, not %s; to publish to %s instead, run 'git -C %s remote remove %s' and then this command again",
			dir, cur, url, url, dir, syncRemote)
	}

	ignore := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(ignore); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(ignore, []byte(syncGitignore), 0o644); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	if flagJSON {
		return printJSON(syncInitDTO{Machine: id, Branch: branch, Path: dir, Remote: url})
	}
	_, _ = fmt.Fprintf(stdout, "%s publishes to %s on branch %s\n", dir, url, branch)
	_, _ = fmt.Fprintln(stdout, "next: taskmgr sync")
	return nil
}

// runSync commits what changed under the central stores directory and pushes
// it. No store lock is taken: holding one across a network call would stall
// every writer on it, and what a snapshot caught mid-write costs is stated in
// CLI-SPEC §2.5.
func runSync() error {
	id, err := machineID()
	if err != nil {
		return err
	}
	branch := machineBranch(id)
	dir, err := tasks.CentralStoresDir()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return fmt.Errorf("%s is not set up for sync; run 'taskmgr sync init --remote <url>' first", dir)
	}
	if _, err := syncGit(dir, id, "config", "--get", "remote."+syncRemote+".url"); err != nil {
		if !gitAbsent(err) {
			return err
		}
		return fmt.Errorf("%s has no remote %q; run 'taskmgr sync init --remote <url>' first", dir, syncRemote)
	}
	if err := checkSyncBranch(dir, id); err != nil {
		return err
	}

	out := syncDTO{Machine: id, Branch: branch}
	if _, err := syncGit(dir, id, "add", "--all"); err != nil {
		return err
	}
	if _, err := syncGit(dir, id, "diff", "--cached", "--quiet"); err != nil {
		if !gitAbsent(err) {
			return err
		}
		// Exit 1 from diff --quiet: something is staged.
		if _, err := syncGit(dir, id, "commit", "--quiet", "--no-verify", "-m", "taskmgr sync: "+id); err != nil {
			return err
		}
		out.Committed = true
	}

	// The remote-tracking ref is what the last successful push left. Only this
	// machine pushes the branch, so a HEAD equal to it is already published and
	// the push — the only network call — is skipped.
	head, err := revision(dir, id, "HEAD")
	if err != nil {
		return err
	}
	if out.Committed {
		out.Commit = head
	}
	published, err := revision(dir, id, "refs/remotes/"+syncRemote+"/"+branch)
	if err != nil {
		return err
	}
	if head != "" && head != published {
		if _, err := syncGit(dir, id, "push", "--quiet", "--no-verify", syncRemote, branch); err != nil {
			if strings.Contains(err.Error(), "[rejected]") {
				return errNotFastForward(dir, id)
			}
			return err
		}
		out.Pushed = true
	}

	if flagJSON {
		return printJSON(out)
	}
	_, _ = fmt.Fprintln(stdout, syncSummary(out))
	return nil
}

// syncGitConfig neutralizes the user's git settings that would stop an
// unattended sync or change what it publishes. --no-verify skips only
// pre-commit, commit-msg and pre-push, so hooks are turned off whole, a global
// core.hooksPath included; a global excludes file could silently keep store
// files out of the mirror; signing would need a key or a prompt.
var syncGitConfig = []string{
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.excludesFile=/dev/null",
	"-c", "commit.gpgSign=false",
	"-c", "push.gpgSign=false",
}

// gitRepoEnv is what `git rev-parse --local-env-vars` lists: the variables that
// point git at a repository, an index or a config other than the one in its
// working directory. A sync run from inside another repository's git hook
// inherits them.
var gitRepoEnv = map[string]bool{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true, "GIT_CONFIG": true, "GIT_CONFIG_PARAMETERS": true,
	"GIT_CONFIG_COUNT": true, "GIT_OBJECT_DIRECTORY": true, "GIT_DIR": true, "GIT_WORK_TREE": true,
	"GIT_IMPLICIT_WORK_TREE": true, "GIT_GRAFT_FILE": true, "GIT_INDEX_FILE": true,
	"GIT_NO_REPLACE_OBJECTS": true, "GIT_REPLACE_REF_BASE": true, "GIT_PREFIX": true,
	"GIT_SHALLOW_FILE": true, "GIT_COMMON_DIR": true,
}

// syncGit runs one git command in the stores work tree dir. Its environment
// is this process's without gitRepoEnv, with the fixed sync identity in the
// variables git ranks above any config (CLI-SPEC §2.5).
func syncGit(dir, id string, args ...string) (string, error) {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !gitRepoEnv[name] {
			env = append(env, kv)
		}
	}
	email := id + "@taskmgr.invalid"
	env = append(env,
		"GIT_AUTHOR_NAME=taskmgr", "GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME=taskmgr", "GIT_COMMITTER_EMAIL="+email)
	return gitOutput(dir, env, append(append([]string{}, syncGitConfig...), args...)...)
}

// gitAbsent reports whether err is git exiting 1: its answer "no such key" to
// config --get, "not a symbolic ref" to symbolic-ref --quiet, "no such ref" to
// rev-parse --verify --quiet, and "differences" to diff --quiet. Any other
// failure — git missing, a corrupt config, a broken repository — is a real one.
func gitAbsent(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == 1
}

// revision returns the commit ref names, or "" when there is none — an unborn
// HEAD, or a branch never pushed.
func revision(dir, id, ref string) (string, error) {
	sha, err := syncGit(dir, id, "rev-parse", "--verify", "--quiet", ref)
	if err != nil && gitAbsent(err) {
		return "", nil
	}
	return sha, err
}

// absRemote makes a local-path remote absolute: git runs in the stores
// directory, so it would resolve a relative one there, not where the user typed
// it. A URL with a scheme, or in scp form (a ':' before any '/'), is kept as is.
func absRemote(url string) (string, error) {
	if filepath.IsAbs(url) || strings.Contains(url, "://") {
		return url, nil
	}
	if colon := strings.Index(url, ":"); colon > 0 {
		if slash := strings.Index(url, "/"); slash < 0 || colon < slash {
			return url, nil
		}
	}
	return filepath.Abs(url)
}

// syncSummary is the one line `sync` prints.
func syncSummary(r syncDTO) string {
	switch {
	case r.Committed && r.Pushed:
		return fmt.Sprintf("Committed %s and pushed %s to %s", shortSHA(r.Commit), r.Branch, syncRemote)
	case r.Pushed:
		return fmt.Sprintf("Nothing new to commit; pushed %s to %s", r.Branch, syncRemote)
	default:
		return fmt.Sprintf("Nothing to publish: %s has nothing new since its last push to %s", r.Branch, syncRemote)
	}
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// errNotFastForward explains a rejected push. The branch has one writer by
// design, so a remote that holds commits this clone lacks has a second one.
func errNotFastForward(dir, id string) error {
	return fmt.Errorf("the remote rejected the push of %s: it holds commits this machine never made, so another machine publishes under the machine ID %q, or this machine was set up again. "+
		"Pick another ID with 'taskmgr config set machine_id <id> --global', move the work tree to it with 'git -C %s branch -m machine/<id>', and run 'taskmgr sync' again",
		machineBranch(id), id, dir)
}

// checkSyncBranch refuses a work tree whose branch is not this machine's: the
// machine ID changed since `sync init`, and publishing would push one machine's
// stores under another's name.
func checkSyncBranch(dir, id string) error {
	// The full ref, not --short: a tag of the same name makes --short print
	// heads/machine/<id>.
	ref, err := syncGit(dir, id, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		if !gitAbsent(err) {
			return err
		}
		return fmt.Errorf("%s is not on a branch; check it out on %s", dir, machineBranch(id))
	}
	cur := strings.TrimPrefix(ref, "refs/heads/")
	if ref == "refs/heads/"+machineBranch(id) {
		return nil
	}
	return fmt.Errorf("%s is on branch %s, but this machine's ID is %q (branch %s): machine_id changed since 'taskmgr sync init'. "+
		"Set machine_id back, or move the work tree to the new ID with 'git -C %s branch -m %s'",
		dir, cur, id, machineBranch(id), dir, machineBranch(id))
}

func machineBranch(id string) string { return "machine/" + id }

// machineID returns this machine's ID (CONFIG-SPEC §2): the per-user config's
// machine_id, else one derived from the hostname.
func machineID() (string, error) {
	cfg, err := tasks.LoadGlobalConfig()
	if err != nil {
		return "", err
	}
	if cfg.MachineID != "" {
		if !validMachineID(cfg.MachineID) {
			return "", errInvalidMachineID(cfg.MachineID)
		}
		return cfg.MachineID, nil
	}
	host, err := os.Hostname()
	if id := deriveMachineID(host); err == nil && validMachineID(id) {
		return id, nil
	}
	return "", fmt.Errorf("no machine ID can be derived from the hostname %q; set one with 'taskmgr config set machine_id <id> --global'", host)
}

// machineIDRe is the machine-ID grammar (CONFIG-SPEC §2). It is a git ref
// component and an email local part as it stands, so neither needs escaping.
var machineIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func validMachineID(id string) bool { return machineIDRe.MatchString(id) }

func errInvalidMachineID(id string) error {
	return fmt.Errorf("invalid machine_id %q: lowercase letters, digits and '-', starting with a letter or digit, at most 63 characters", id)
}

// nonMachineIDRe matches the characters a derived machine ID replaces.
var nonMachineIDRe = regexp.MustCompile(`[^a-z0-9-]`)

// deriveMachineID turns a hostname into a machine ID candidate (CONFIG-SPEC §2):
// lowercased, cut at the first '.', every character outside [a-z0-9-] replaced
// by '-', and leading and trailing '-' trimmed. The result may still fail the
// grammar — an empty or overlong hostname does — which the caller reports.
func deriveMachineID(hostname string) string {
	h, _, _ := strings.Cut(strings.ToLower(hostname), ".")
	return strings.Trim(nonMachineIDRe.ReplaceAllString(h, "-"), "-")
}

func init() {
	syncInitCmd.Flags().StringVar(&syncInitFlags.remote, "remote", "", "the git URL to publish to")
	_ = syncInitCmd.MarkFlagRequired("remote")

	syncCmd.AddCommand(syncInitCmd)
	rootCmd.AddCommand(syncCmd)
}
