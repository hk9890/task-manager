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
	"bytes"
	"errors"
	"fmt"
	"log/slog"
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

// The publish has done its job when the record is written, so a failed write
// fails nothing: it is logged, and the status keeps the run before.
func TestPublish_RecordWriteFails_RunSucceedsWarnsAndKeepsTheLastRecord(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	first := time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC)
	if _, err := publishWith(fs, e, runner, clockAt(first)); err != nil {
		t.Fatal(err)
	}
	g.staged = true
	fs.FailOn("WriteAtomic", filepath.Join("/home", publishStateName), errors.New("disk full"))
	var buf bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	res, err := publishWith(fs, e, runner, clockAt(first.Add(time.Hour)), WithLogger(lg))
	if err != nil || !res.Pushed {
		t.Fatalf("publishWith = %+v, %v, want a pushed run", res, err)
	}
	if rec := find(records(t, &buf), "publish_record_error"); rec == nil || !strings.Contains(fmt.Sprint(rec["error"]), "disk full") {
		t.Errorf("publish_record_error record = %v, want one naming the cause", rec)
	}
	if st := publishStatus(t, fs, e); !st.LastSync.Equal(first) {
		t.Errorf("LastSync = %s, want the run before, %s", st.LastSync, first)
	}
}

func TestInitPublish_SameRemoteAgain_KeepsTheRecordUnwritten(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	if _, err := publishWith(fs, e, runner, clockAt(time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("/home", publishStateName)
	before, _ := fs.ReadFile(path)
	fs.FailOn("WriteAtomic", path, errors.New("an init with the same remote must not write"))

	if _, err := initPublishWith("/remote.git", fs, e, runner); err != nil {
		t.Fatalf("initPublishWith: %v", err)
	}
	if after, _ := fs.ReadFile(path); !bytes.Equal(before, after) {
		t.Errorf("the record changed:\n%s\nwant\n%s", after, before)
	}
}

// The documented way to change the remote is to remove origin and init again;
// a sync recorded for the old remote says nothing about the new one.
func TestInitPublish_AnotherRemote_DropsTheSyncOfTheOldOne(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	if _, err := publishWith(fs, e, runner, clockAt(time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	g.remote = ""
	if _, err := initPublishWith("/other.git", fs, e, runner); err != nil {
		t.Fatalf("initPublishWith: %v", err)
	}

	st := publishStatus(t, fs, e)
	if st.Remote != "/other.git" || !st.LastSync.IsZero() || st.Commit != "" {
		t.Errorf("status = %+v, want the new remote and no sync", st)
	}
}

func TestInitPublish_DamagedRecord_Replaced(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	if err := fs.WriteAtomic(filepath.Join("/home", publishStateName), []byte("last_sync: [not a time"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := initPublishWith("/remote.git", fs, e, runner); err != nil {
		t.Fatalf("initPublishWith over a damaged record: %v", err)
	}
	if st := publishStatus(t, fs, e); st.Remote != "/remote.git" {
		t.Errorf("Remote = %q, want /remote.git", st.Remote)
	}
}

func TestInitPublish_RecordUnreadable_RefusesAndErasesNothing(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	if _, err := publishWith(fs, e, runner, clockAt(time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("EIO")
	fs.FailOn("ReadFile", filepath.Join("/home", publishStateName), boom)

	if _, err := initPublishWith("/remote.git", fs, e, runner); !errors.Is(err, boom) {
		t.Fatalf("initPublishWith = %v, want the read error", err)
	}
	if st := publishStatus(t, fs, e); st.LastSync.IsZero() {
		t.Error("an unreadable record was erased")
	}
}

// After machine_id changes, the record is of the old branch: it says nothing
// about when the new one synced.
func TestPublishStatus_RecordOfAnotherBranch_NoSyncShown(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	if _, err := publishWith(fs, e, runner, clockAt(time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	if err := updateGlobalConfig(fs, e, func(g *GlobalConfig) error { g.MachineID = "desk-1"; return nil }); err != nil {
		t.Fatal(err)
	}

	st := publishStatus(t, fs, e)
	if st.Branch != "machine/desk-1" || !st.LastSync.IsZero() || st.Commit != "" {
		t.Errorf("status = %+v, want machine/desk-1 with no sync", st)
	}
}

// A .git with no recorded remote is an init that stopped, or a stores/ under
// git before any init: every sync would fail, so it is not set up.
func TestPublishStatus_RepositoryWithoutARecordedRemote_NotSetUp(t *testing.T) {
	fs, e, _, _ := publishFixture(t, true)
	if st := publishStatus(t, fs, e); st.SetUp {
		t.Errorf("status = %+v, want not set up", st)
	}
}

func TestPublishStatus_LastSync_WholeSecondsUTC(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	at := time.Date(2026, 11, 13, 15, 0, 7, 123456789, time.FixedZone("CET", 3600))
	if _, err := publishWith(fs, e, runner, clockAt(at)); err != nil {
		t.Fatal(err)
	}
	content, err := fs.ReadFile(filepath.Join("/home", publishStateName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "last_sync: 2026-11-13T14:00:07Z\n") {
		t.Errorf("state file = %q, want last_sync in whole seconds, UTC", content)
	}
}

// beforeTry is a Mem that runs onTry before each TryLock, so a test can change
// the publish lock between the status call's read of the holder and its try.
type beforeTry struct {
	*vfs.Mem
	onTry func()
}

func (b beforeTry) TryLock(path string) (func() error, bool, error) {
	b.onTry()
	return b.Mem.TryLock(path)
}

func writeHolder(t *testing.T, fs vfs.FS, pid int) {
	t.Helper()
	at := time.Date(2026, 11, 13, 14, 2, 0, 0, time.UTC)
	if err := fs.WriteInPlace(filepath.Join("/home", publishLockName), publishHolder(pid, at), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A dead holder costs one try: the status empties its content, so the next
// status takes no lock and the next refusal cannot name it.
func TestPublishStatus_StaleHolderUnderAFreeLock_ContentEmptied(t *testing.T) {
	fs, e, _, _ := publishFixture(t, true)
	writeHolder(t, fs, 4711)

	publishStatus(t, fs, e)
	content, err := fs.ReadFile(filepath.Join("/home", publishLockName))
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != 0 {
		t.Errorf("lock file after the status = %q, want it emptied", content)
	}
}

// A publish that takes the lock between the status call's read and its try
// is named, not the dead holder the first read saw.
func TestPublishStatus_HolderChangedBeforeTheTry_NamesTheNewHolder(t *testing.T) {
	m, e, _, _ := publishFixture(t, true)
	writeHolder(t, m, 4711)
	path := filepath.Join("/home", publishLockName)
	fs := beforeTry{Mem: m.Mem, onTry: func() {
		if _, err := m.Lock(path); err != nil {
			t.Fatal(err)
		}
		writeHolder(t, m, 9000)
	}}

	if st := publishStatus(t, fs, e); !st.Running || st.RunningPID != 9000 {
		t.Errorf("status = %+v, want running with pid 9000", st)
	}
}

// Another status that holds the lock while it empties a dead holder's content
// is not a running publish.
func TestPublishStatus_OtherStatusEmptiedTheHolder_NotRunning(t *testing.T) {
	m, e, _, _ := publishFixture(t, true)
	writeHolder(t, m, 4711)
	path := filepath.Join("/home", publishLockName)
	fs := beforeTry{Mem: m.Mem, onTry: func() {
		if _, err := m.Lock(path); err != nil {
			t.Fatal(err)
		}
		if err := m.WriteInPlace(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}}

	if st := publishStatus(t, fs, e); st.Running {
		t.Errorf("status = %+v, want not running", st)
	}
}
