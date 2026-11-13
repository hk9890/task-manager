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

// publish_test.go — L1 for the pure half of publishing (publish.go): the
// machine-ID rules (CONFIG-SPEC §2), the exclude patterns, remote
// classification, and how a git failure reads.
package tasks

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestDeriveMachineID_NormalisesHostnames(t *testing.T) {
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

func TestValidMachineID_EnforcesGrammar(t *testing.T) {
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

func TestMachineIDOf_ConfiguredWinsAndIsValidated(t *testing.T) {
	noHost := func() (string, error) { t.Fatal("hostname read although machine_id is set"); return "", nil }
	if got, err := machineIDOf("laptop", noHost); err != nil || got != "laptop" {
		t.Errorf("machineIDOf(laptop) = %q, %v", got, err)
	}
	_, err := machineIDOf("Lap_Top", noHost)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "machine_id" {
		t.Errorf("machineIDOf(Lap_Top) error = %v, want a *ValidationError on machine_id", err)
	}
}

func TestMachineIDOf_DerivesFromTheHostname(t *testing.T) {
	host := func(h string, err error) func() (string, error) {
		return func() (string, error) { return h, err }
	}
	if got, err := machineIDOf("", host("Hans-Laptop.local", nil)); err != nil || got != "hans-laptop" {
		t.Errorf("derived = %q, %v; want hans-laptop", got, err)
	}
	if _, err := machineIDOf("", host("___", nil)); err == nil || !strings.Contains(err.Error(), "taskmgr config set machine_id") {
		t.Errorf("an underivable hostname gave %v, want advice to set machine_id", err)
	}
}

// A hostname that cannot be read is that failure, not a hostname "".
func TestMachineIDOf_HostnameFailureCarriesTheCause(t *testing.T) {
	cause := errors.New("uname: operation not permitted")
	_, err := machineIDOf("", func() (string, error) { return "", cause })
	if !errors.Is(err, cause) || strings.Contains(err.Error(), `hostname ""`) || !strings.Contains(err.Error(), "taskmgr config set machine_id") {
		t.Errorf("unreadable hostname gave %v, want the cause and advice to set machine_id", err)
	}
}

// Each setting keeps a user's git config from changing the snapshot's bytes
// or running a program of the user's under the store locks.
func TestPublishGitSettings_NeutralizeTheUsersConfig(t *testing.T) {
	for _, want := range []string{
		"core.hooksPath=/dev/null",
		"core.excludesFile=/dev/null",
		"core.attributesFile=/dev/null",
		"core.autocrlf=false",
		"core.safecrlf=false",
		"core.fsmonitor=false",
		"commit.gpgSign=false",
		"push.gpgSign=false",
	} {
		i := slices.Index(publishGitSettings, want)
		if i < 1 || publishGitSettings[i-1] != "-c" {
			t.Errorf("publishGitSettings %q lack -c %s", publishGitSettings, want)
		}
	}
}

// A stalled HTTPS transfer must end after a minute, not hold the publish lock
// until the push timeout.
func TestPublishGitSettings_AbortAStalledHTTPTransfer(t *testing.T) {
	for _, want := range []string{
		"http.lowSpeedLimit=1000",
		"http.lowSpeedTime=60",
	} {
		i := slices.Index(publishGitSettings, want)
		if i < 1 || publishGitSettings[i-1] != "-c" {
			t.Errorf("publishGitSettings %q lack -c %s", publishGitSettings, want)
		}
	}
}

// A push that needs credentials must fail at once, not wait on a terminal
// until it times out, and the system attributes file is out of
// core.attributesFile's reach.
func TestPublishEnv_FixesTheIdentityAndTurnsOffPromptsAndSystemAttributes(t *testing.T) {
	env := publishEnv("laptop")
	for _, want := range []string{
		"GIT_AUTHOR_NAME=taskmgr", "GIT_AUTHOR_EMAIL=laptop@taskmgr.invalid",
		"GIT_COMMITTER_NAME=taskmgr", "GIT_COMMITTER_EMAIL=laptop@taskmgr.invalid",
		"GIT_TERMINAL_PROMPT=0", "GIT_ATTR_NOSYSTEM=1",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("publishEnv %q lacks %s", env, want)
		}
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_SSH") {
			t.Errorf("publishEnv %q sets %s, which would replace the user's ssh setup", env, kv)
		}
	}
}

func TestErrLockWaitOver_NamesTheBusyLockAndTheWait(t *testing.T) {
	if msg := errLockWaitOver("beta").Error(); !strings.Contains(msg, `waiting 2m0s for the write lock of store "beta"`) {
		t.Errorf("busy store = %q, want the store and the wait named", msg)
	}
	if msg := errLockWaitOver(busyCentralRoot).Error(); !strings.Contains(msg, "waiting 2m0s for the central-root lock") {
		t.Errorf("busy central root = %q, want the central-root lock and the wait named", msg)
	}
}

func TestPublishUnsetEnv_DropsTheRepositoryAndTheDates(t *testing.T) {
	for _, name := range []string{"GIT_DIR", "GIT_INDEX_FILE", "GIT_AUTHOR_DATE", "GIT_COMMITTER_DATE"} {
		if !slices.Contains(publishUnsetEnv, name) {
			t.Errorf("publishUnsetEnv %q lacks %s", publishUnsetEnv, name)
		}
	}
}

func TestLocalRelativeRemote_ClassifiesURLs(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"./stores.git", true},
		{"stores.git", true},
		{"../a/b.git", true},
		{"./host:path", true},
		{"/srv/stores.git", false},
		{"git@github.com:you/stores.git", false},
		{"host:path", false},
		{"https://github.com/you/stores.git", false},
		{"file:///srv/stores.git", false},
	} {
		if got := localRelativeRemote(tc.url); got != tc.want {
			t.Errorf("localRelativeRemote(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

// gitAbsent reads the answer gitWithin marked, never the exit code alone: an
// exit 1 from commit or push is a failure.
func TestGitAbsent_OnlyAnAnswer(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&gitError{Args: []string{"config", "--get", "x"}, ExitCode: 1, Answer: true}, true},
		{fmt.Errorf("wrapped: %w", &gitError{ExitCode: 1, Answer: true}), true},
		{&gitError{Args: []string{"commit", "--quiet"}, ExitCode: 1}, false},
		{&gitError{Args: []string{"push", "--quiet"}, ExitCode: 1}, false},
		{&gitError{ExitCode: 128, Message: "fatal: bad config"}, false},
		{errGitMissing(errors.New("not found")), false},
		{nil, false},
	} {
		if got := gitAbsent(tc.err); got != tc.want {
			t.Errorf("gitAbsent(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// Each pattern is built from the engine's own name for the file, so the list
// follows a rename; tempPrefix is taken as given, not copied from the vfs seam.
func TestPublishExcludes_BuiltFromTheEngineNames(t *testing.T) {
	lines := strings.Split(string(publishExcludes("TMP")), "\n")
	for _, want := range []string{
		"/*/" + lockFileName,
		"TMP*",
		"/*/" + contentDirName + "/" + stagedContentPrefix + "*" + stagedContentSuffix,
		"/" + stagingPrefix + "*/",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("excludes %q lack the pattern %q", lines, want)
		}
	}
	for _, l := range lines {
		if l != "" && !strings.HasPrefix(l, "#") && strings.Contains(l, ".tmp-") {
			t.Errorf("pattern %q copies the vfs temp prefix instead of taking it", l)
		}
	}
}

func TestRedactUserinfo_StripsCredentialsFromSchemeURLsOnly(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://user:token@github.com/you/stores.git", "https://github.com/you/stores.git"},
		{"https://token@github.com/x.git", "https://github.com/x.git"},
		{"ssh://git@host:22/x.git", "ssh://host:22/x.git"},
		{"fatal: unable to access 'https://u:p@h/x/': 403", "fatal: unable to access 'https://h/x/': 403"},
		{"a https://u:p@h/1 and http://v@k/2", "a https://h/1 and http://k/2"},
		{"git@github.com:you/stores.git", "git@github.com:you/stores.git"},
		{"/srv/remote.git", "/srv/remote.git"},
		{"https://github.com/a@b", "https://github.com/a@b"},
	} {
		if got := redactUserinfo(tc.in); got != tc.want {
			t.Errorf("redactUserinfo(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestErrRemoteChanged_CredentialsOnly_SaysSoWithoutEither(t *testing.T) {
	msg := errRemoteChanged("/c/stores", "https://u:old@h/x.git", "https://u:new@h/x.git").Error()
	for _, secret := range []string{"old", "new@"} {
		if strings.Contains(msg, secret) {
			t.Errorf("message %q repeats a credential", msg)
		}
	}
	for _, want := range []string{"https://h/x.git with other credentials", "remote set-url origin <url>"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, "not https://h/x.git") {
		t.Errorf("message %q names the same URL as old and new", msg)
	}
}

func TestErrRemoteChanged_OtherURL_NamesBoth(t *testing.T) {
	msg := errRemoteChanged("/c/stores", "https://u:t@h/a.git", "https://h/b.git").Error()
	if !strings.Contains(msg, "already publishes to https://h/a.git, not https://h/b.git") {
		t.Errorf("message %q does not name both remotes", msg)
	}
}

func TestErrPublishBusy_NamesTheHolderTheContentDescribes(t *testing.T) {
	at := time.Date(2026, 11, 13, 15, 2, 0, 0, time.FixedZone("CET", 3600))
	if got, want := errPublishBusy(publishHolder(4711, at)).Error(), "another taskmgr sync is running (pid 4711, since 2026-11-13T14:02:00Z)"; got != want {
		t.Errorf("busy with a holder = %q, want %q", got, want)
	}
	for _, content := range []string{"", "pid: 4711\n", "since: 2026-11-13T14:02:00Z\n", "pid: 0\nsince: 2026-11-13T14:02:00Z\n", "pid: x\nsince: 2026-11-13T14:02:00Z\n", "pid: 4711\nsince: yesterday\n"} {
		err := errPublishBusy([]byte(content))
		if !errors.Is(err, ErrPublishBusy) || err.Error() != "another taskmgr sync is running" {
			t.Errorf("busy with content %q = %q, want the bare ErrPublishBusy", content, err)
		}
	}
}

func TestGitAnswer_OnlyTheQuestionsThatExitOne(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"config", "--get", "remote.origin.url"}, true},
		{[]string{"diff", "--cached", "--quiet"}, true},
		{[]string{"rev-parse", "--verify", "--quiet", "HEAD"}, true},
		{[]string{"symbolic-ref", "--quiet", "HEAD"}, true},
		{[]string{"merge-base", "--is-ancestor", "abc", "HEAD"}, true},
		{[]string{"symbolic-ref", "HEAD", "refs/heads/machine/laptop"}, false},
		{[]string{"ls-remote", "origin", "refs/heads/machine/laptop"}, false},
		{[]string{"push", "--quiet", "--no-verify", "origin", "x:x"}, false},
		{[]string{"commit", "--quiet", "--no-verify", "-m", ""}, false},
		{[]string{"add", "--all"}, false},
		{[]string{"config"}, false},
	} {
		if got := gitAnswer(tc.args); got != tc.want {
			t.Errorf("gitAnswer(%q) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestLogStderr_TrimsRedactsAndBounds(t *testing.T) {
	if got := logStderr([]byte("  fatal: 'https://u:p@h/x' failed\n")); got != "fatal: 'https://h/x' failed" {
		t.Errorf("logStderr = %q", got)
	}
	long := logStderr([]byte(strings.Repeat("é", logStderrMax)))
	if !strings.HasSuffix(long, "…") || len(long) > logStderrMax+len("…") || !utf8.ValidString(long) {
		t.Errorf("long stderr cut to %d bytes, valid %v; want at most %d and a marker", len(long), utf8.ValidString(long), logStderrMax)
	}
}

func TestErrGitTimeout_NamesTheCommandTheLimitAndGitsStderr(t *testing.T) {
	err := errGitTimeout([]string{"push", "origin"}, publishPushTimeout, []byte("fatal: https://u:p@h/x hangs\n"))
	if got, want := err.Error(), "push timed out after 10m0s: fatal: https://h/x hangs"; got != want {
		t.Errorf("errGitTimeout = %q, want %q", got, want)
	}
	if got := errGitTimeout([]string{"push"}, publishPushTimeout, nil).Error(); got != "push timed out after 10m0s" {
		t.Errorf("errGitTimeout without stderr = %q", got)
	}
}
