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

package tasks

// publish_status_test.go — L2 for PublishStatus and the publish state file a
// successful publish writes (SDK-SPEC §1, CONFIG-SPEC §3), on the publish
// fixture of publishrun_test.go.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

func clockAt(at time.Time) Option { return WithClock(func() time.Time { return at }) }

func publishStatus(t *testing.T, fs vfs.FS, e env.Environment) PublishState {
	t.Helper()
	st, err := publishStatusWith(fs, e)
	if err != nil {
		t.Fatalf("publishStatusWith: %v", err)
	}
	return st
}

func TestPublishStatus_AfterAPublish_ReportsItsTimeCommitAndRemote(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	at := time.Date(2026, 11, 13, 15, 0, 0, 0, time.FixedZone("CET", 3600))
	if _, err := publishWith(fs, e, runner, clockAt(at)); err != nil {
		t.Fatalf("publishWith: %v", err)
	}

	st := publishStatus(t, fs, e)
	want := PublishState{SetUp: true, Machine: "laptop", Branch: "machine/laptop", Remote: "/remote.git", LastSync: at.UTC(), Commit: sha(1)}
	if st != want {
		t.Errorf("status = %+v, want %+v", st, want)
	}
}

// A run with nothing new commits nothing, and is still a successful sync: the
// time moves, so a working timer never looks like a dead one.
func TestPublishStatus_PublishWithNothingNew_MovesTheTimeKeepsTheCommit(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	first := time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC)
	if _, err := publishWith(fs, e, runner, clockAt(first)); err != nil {
		t.Fatal(err)
	}
	second := first.Add(time.Hour)
	res, err := publishWith(fs, e, runner, clockAt(second))
	if err != nil {
		t.Fatal(err)
	}
	if res.Committed {
		t.Fatal("the fixture staged a change on the second run")
	}

	st := publishStatus(t, fs, e)
	if !st.LastSync.Equal(second) || st.Commit != sha(1) {
		t.Errorf("LastSync, Commit = %s, %q, want %s, %q", st.LastSync, st.Commit, second, sha(1))
	}
}

func TestPublishStatus_FailedPublish_LeavesTheRecordedStateAsItWas(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	first := time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC)
	if _, err := publishWith(fs, e, runner, clockAt(first)); err != nil {
		t.Fatal(err)
	}
	g.staged, g.reject = true, true
	if _, err := publishWith(fs, e, runner, clockAt(first.Add(time.Hour))); err == nil {
		t.Fatal("a rejected push succeeded")
	}

	st := publishStatus(t, fs, e)
	if !st.LastSync.Equal(first) || st.Commit != sha(1) {
		t.Errorf("after a failed publish LastSync, Commit = %s, %q, want %s, %q", st.LastSync, st.Commit, first, sha(1))
	}
}

func TestPublishStatus_NotSetUpAndNothingRecorded_ZeroStateNoError(t *testing.T) {
	fs, e, _, _ := publishFixture(t, false)

	st := publishStatus(t, fs, e)
	want := PublishState{Machine: "laptop", Branch: "machine/laptop"}
	if st != want {
		t.Errorf("status = %+v, want %+v", st, want)
	}
}

func TestPublishStatus_Init_RecordsTheRemoteWithoutItsCredentials(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, false)
	if _, err := initPublishWith("https://user:token@example.com/mirror.git", fs, e, runner); err != nil {
		t.Fatalf("initPublishWith: %v", err)
	}

	st := publishStatus(t, fs, e)
	if !st.SetUp || st.Remote != "https://example.com/mirror.git" || !st.LastSync.IsZero() {
		t.Errorf("status after init = %+v, want set up, the redacted remote, no sync", st)
	}
	content, err := fs.ReadFile(filepath.Join("/home", publishStateName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "token") {
		t.Errorf("the state file holds the credential: %q", content)
	}
}

func TestPublishStatus_PublishLockHeld_RunningWithItsHolder(t *testing.T) {
	fs, e, _, _ := publishFixture(t, true)
	unlock := holdPublishLock(t, fs, e)

	st := publishStatus(t, fs, e)
	if !st.Running || st.RunningPID != 4711 || !st.RunningSince.Equal(time.Date(2026, 11, 13, 14, 2, 0, 0, time.UTC)) {
		t.Errorf("status while held = %+v, want running, pid 4711, since 14:02Z", st)
	}

	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if st := publishStatus(t, fs, e); st.Running {
		t.Errorf("status after the release = %+v, want not running", st)
	}
}

// A holder killed before it emptied the lock file leaves its content under a
// free lock: no publish runs, and the try must not keep the lock.
func TestPublishStatus_StaleHolderUnderAFreeLock_NotRunningAndLockFree(t *testing.T) {
	fs, e, _, _ := publishFixture(t, true)
	path := filepath.Join("/home", publishLockName)
	if err := fs.WriteInPlace(path, publishHolder(4711, time.Date(2026, 11, 13, 14, 2, 0, 0, time.UTC)), 0o644); err != nil {
		t.Fatal(err)
	}

	if st := publishStatus(t, fs, e); st.Running {
		t.Errorf("status = %+v, want not running", st)
	}
	unlock, free, err := fs.TryLock(path)
	if err != nil || !free {
		t.Fatalf("the publish lock is held after the status (free=%v, err=%v)", free, err)
	}
	_ = unlock()
}

func TestPublishStatus_DamagedStateFile_ErrorNamingTheFile(t *testing.T) {
	fs, e, _, _ := publishFixture(t, true)
	path := filepath.Join("/home", publishStateName)
	if err := fs.WriteAtomic(path, []byte("last_sync: [not a time"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := publishStatusWith(fs, e); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("publishStatusWith = %v, want an error naming %s", err, path)
	}
}
