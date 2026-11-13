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
	"bytes"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// newBoundedStore returns a Mem store opened with opts, and the path of its
// write lock.
func newBoundedStore(t *testing.T, opts ...Option) (*Store, *vfs.Mem, string) {
	t.Helper()
	m := vfs.NewMem()
	s, err := InitWithVFS("/", "agt", m, append([]Option{WithClock(monotonicClock())}, opts...)...)
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}
	return s, m, filepath.Join(s.dir, lockFileName)
}

// holdLock takes the store's write lock as another writer would, and releases
// it at the end of the test unless the test released it first.
func holdLock(t *testing.T, m *vfs.Mem, lockPath string) (release func()) {
	t.Helper()
	unlock, err := m.Lock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() { once.Do(func() { _ = unlock() }) }
	t.Cleanup(release)
	return release
}

func TestWithLockTimeout_LockHeldForTheWholeTimeout_StoreBusyLoggedAndNothingWritten(t *testing.T) {
	var buf bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	s, m, lockPath := newBoundedStore(t, WithLockTimeout(20*time.Millisecond), WithLogger(lg))
	release := holdLock(t, m, lockPath)

	_, err := s.Create(CreateInput{Title: "blocked"})
	if !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("Create = %v, want ErrStoreBusy", err)
	}
	busy := find(records(t, &buf), "store_busy")
	if busy == nil {
		t.Fatal("a busy write logged no store_busy record")
	}
	if busy["level"] != "WARN" || busy["store"] != s.dir || busy["waited_ms"] != float64(20) {
		t.Errorf("store_busy record = %v", busy)
	}
	release()
	all, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("a busy write wrote %d issues, want 0", len(all))
	}
}

// The goroutine a busy write leaves behind still waits for both locks; once it
// holds them it must release them, or every later write of the store hangs.
func TestWithLockTimeout_AfterABusyWrite_TheWaiterReleasesBothLocks(t *testing.T) {
	s, m, lockPath := newBoundedStore(t, WithLockTimeout(20*time.Millisecond))
	release := holdLock(t, m, lockPath)
	if _, err := s.Create(CreateInput{Title: "blocked"}); !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("Create = %v, want ErrStoreBusy", err)
	}

	release()
	// The waiter holds mu while it waits for the flock, so this returns only
	// once it has taken the flock and released both.
	s.mu.Lock()
	unlock, ok, err := m.TryLock(lockPath)
	s.mu.Unlock()
	if err != nil || !ok {
		t.Fatalf("the write lock is still held (ok=%v, err=%v)", ok, err)
	}
	_ = unlock()
	if _, err := s.Create(CreateInput{Title: "free"}); err != nil {
		t.Fatalf("Create after the release = %v, want success", err)
	}
}

func TestWithLockTimeout_LockReleasedDuringTheWait_WriteGoesThrough(t *testing.T) {
	s, m, lockPath := newBoundedStore(t, WithLockTimeout(time.Minute))
	release := holdLock(t, m, lockPath)
	time.AfterFunc(20*time.Millisecond, release)

	res, err := s.Create(CreateInput{Title: "waited"})
	if err != nil {
		t.Fatalf("Create = %v, want success after the holder released", err)
	}
	if res.Issue.Title != "waited" {
		t.Errorf("Title = %q, want %q", res.Issue.Title, "waited")
	}
}

func TestWithLockTimeout_MutexHeld_StoreBusy(t *testing.T) {
	s, _, _ := newBoundedStore(t, WithLockTimeout(20*time.Millisecond))
	s.mu.Lock()
	_, err := s.Create(CreateInput{Title: "blocked"})
	s.mu.Unlock()
	if !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("Create = %v, want ErrStoreBusy", err)
	}
}

func TestWithLockTimeout_BusyWrite_RunsNoPreHook(t *testing.T) {
	s, m, lockPath := newBoundedStore(t, WithLockTimeout(20*time.Millisecond))
	var runs int
	s.runner = &exec.Fake{Func: func(exec.Spec) exec.Result { runs++; return exec.Allow("") }}
	storePackage(t, s, "p", []Hook{{ID: "g", Event: "pre-create", Run: []string{"g"}}})
	holdLock(t, m, lockPath)

	if _, err := s.Create(CreateInput{Title: "blocked"}); !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("Create = %v, want ErrStoreBusy", err)
	}
	if runs != 0 {
		t.Errorf("a busy write ran %d hooks, want 0", runs)
	}
}

func TestWithLockTimeout_LockFault_ReturnedAndMutexReleased(t *testing.T) {
	s, m, lockPath := newBoundedStore(t, WithLockTimeout(time.Minute))
	fault := errors.New("ENOLCK")
	m.FailOn("Lock", lockPath, fault)

	if _, err := s.Create(CreateInput{Title: "fault"}); !errors.Is(err, fault) {
		t.Fatalf("Create = %v, want the lock fault", err)
	}
	if _, err := s.Create(CreateInput{Title: "after"}); err != nil {
		t.Fatalf("Create after the fault = %v, want success: the fault must release mu", err)
	}
}

func TestWithLockTimeout_ConcurrentWritesOfOneStore_AllWritten(t *testing.T) {
	s, _, _ := newBoundedStore(t, WithLockTimeout(time.Minute))
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Create(CreateInput{Title: "parallel"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Create = %v", err)
		}
	}
	if all, _ := s.All(); len(all) != n {
		t.Errorf("wrote %d issues, want %d", len(all), n)
	}
}

func TestWithLockTimeout_ZeroAfterABound_WaitsWithoutALimit(t *testing.T) {
	s, m, lockPath := newBoundedStore(t, WithLockTimeout(20*time.Millisecond), WithLockTimeout(0))
	release := holdLock(t, m, lockPath)
	done := make(chan error, 1)
	go func() {
		_, err := s.Create(CreateInput{Title: "waits"})
		done <- err
	}()

	// Five times the cleared bound: a write still bounded has returned by now.
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("Create returned %v while the lock was held, want it to wait", err)
	default:
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("Create = %v, want success after the release", err)
	}
}
