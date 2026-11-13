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

func TestGitAbsent_OnlyExitOne(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&gitError{ExitCode: 1}, true},
		{fmt.Errorf("wrapped: %w", &gitError{ExitCode: 1}), true},
		{&gitError{ExitCode: 128, Message: "fatal: bad config"}, false},
		{errGitMissing(errors.New("not found")), false},
		{nil, false},
	} {
		if got := gitAbsent(tc.err); got != tc.want {
			t.Errorf("gitAbsent(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestGitRejected_ReadsGitsMessage(t *testing.T) {
	rejected := &gitError{Args: []string{"push"}, ExitCode: 1, Message: " ! [rejected]        machine/laptop -> machine/laptop (fetch first)"}
	if !gitRejected(rejected) {
		t.Error("a [rejected] push is not reported as rejected")
	}
	if gitRejected(&gitError{Args: []string{"push"}, ExitCode: 128, Message: "fatal: could not read from remote repository"}) {
		t.Error("an unreachable remote is reported as a rejected push")
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
