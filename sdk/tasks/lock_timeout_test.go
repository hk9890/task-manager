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

// lock_timeout_test.go — L2 for WithLockTimeout: a write of a store opened with
// it gives up with ErrStoreBusy when another writer holds the write lock for
// the whole timeout (SDK-SPEC §7).

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// newBoundedStore returns a Mem store opened WithLockTimeout(timeout) whose
// pauses between tries call pause instead of sleeping, and the path of its
// write lock.
func newBoundedStore(t *testing.T, timeout time.Duration, pause func()) (*Store, *vfs.Mem, string) {
	t.Helper()
	m := vfs.NewMem()
	s, err := InitWithVFS("/", "agt", m, WithLockTimeout(timeout), WithClock(monotonicClock()))
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}
	s.lockPause = pause
	return s, m, filepath.Join(s.dir, lockFileName)
}

// assertLocksFree fails when the store's mutex or its flock is still held.
func assertLocksFree(t *testing.T, s *Store, m *vfs.Mem, lockPath string) {
	t.Helper()
	if !s.mu.TryLock() {
		t.Fatal("the store mutex is still held")
	}
	s.mu.Unlock()
	unlock, ok, err := m.TryLock(lockPath)
	if err != nil || !ok {
		t.Fatalf("the write lock is still held (ok=%v, err=%v)", ok, err)
	}
	_ = unlock()
}

func TestWithLockTimeout_FlockHeldForTheWholeTimeout_StoreBusyAndNothingWritten(t *testing.T) {
	pauses := 0
	s, m, lockPath := newBoundedStore(t, 50*time.Millisecond, func() { pauses++ })
	release, err := m.Lock(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.Create(CreateInput{Title: "blocked"})
	if !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("Create = %v, want ErrStoreBusy", err)
	}
	if want := int(50 * time.Millisecond / lockRetry); pauses != want {
		t.Errorf("pauses = %d, want %d: the pauses measure the wait", pauses, want)
	}
	_ = release()
	all, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("a busy write wrote %d issues, want 0", len(all))
	}
	assertLocksFree(t, s, m, lockPath)
}

func TestWithLockTimeout_FlockReleasedDuringTheWait_WriteGoesThrough(t *testing.T) {
	var release func() error
	pauses := 0
	s, m, lockPath := newBoundedStore(t, time.Second, func() {
		pauses++
		if pauses == 3 {
			_ = release()
		}
	})
	var err error
	if release, err = m.Lock(lockPath); err != nil {
		t.Fatal(err)
	}

	res, err := s.Create(CreateInput{Title: "waited"})
	if err != nil {
		t.Fatalf("Create = %v, want success after the holder released", err)
	}
	if res.Issue.Title != "waited" {
		t.Errorf("Title = %q, want %q", res.Issue.Title, "waited")
	}
	if pauses != 3 {
		t.Errorf("pauses = %d, want 3", pauses)
	}
	assertLocksFree(t, s, m, lockPath)
}

func TestWithLockTimeout_MutexHeldByAnotherGoroutine_StoreBusyAndFlockNotTaken(t *testing.T) {
	s, m, lockPath := newBoundedStore(t, 30*time.Millisecond, func() {})
	s.mu.Lock()

	_, err := s.Create(CreateInput{Title: "blocked"})
	if !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("Create = %v, want ErrStoreBusy", err)
	}
	unlock, ok, err := m.TryLock(lockPath)
	if err != nil || !ok {
		t.Fatalf("the write lock is held after a busy mutex (ok=%v, err=%v)", ok, err)
	}
	_ = unlock()
	s.mu.Unlock()
}

func TestWithLockTimeout_LockFault_ReturnedAndMutexReleased(t *testing.T) {
	s, m, lockPath := newBoundedStore(t, time.Second, func() { t.Fatal("a lock fault must not be retried") })
	fault := errors.New("ENOLCK")
	m.FailOn("TryLock", lockPath, fault)

	_, err := s.Create(CreateInput{Title: "fault"})
	if !errors.Is(err, fault) {
		t.Fatalf("Create = %v, want the lock fault", err)
	}
	assertLocksFree(t, s, m, lockPath)
}

func TestWithLockTimeout_ZeroOrNegative_WaitStaysUnbounded(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		var s Store
		WithLockTimeout(d)(&s)
		if s.lockTimeout != 0 {
			t.Errorf("WithLockTimeout(%s) set lockTimeout %s, want 0", d, s.lockTimeout)
		}
	}
}
