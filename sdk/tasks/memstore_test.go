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

// memstore_test.go — the store fixture for white-box (`package tasks`) tests.
//
// internal/storetest is the fixture builder for black-box (`package tasks_test`)
// files. It imports tasks, so a white-box file cannot import it back without a
// cycle; before this file existed every white-box test that needed a store
// hand-rolled its own builder, and six near-identical copies accumulated.
//
// The rule deciding which package a test file belongs in is in
// docs/implementation/TESTING-STRATEGY.md.

import (
	"sync"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// newMemStore returns a store rooted at "/" on a fresh vfs.Mem, together with
// that Mem so the caller can inject faults with FailOn. Ignore the second
// return value when you do not need it:
//
//	s, _ := newMemStore(t)
//
// The clock is deterministic and advances one second per call. It is guarded by
// a mutex so concurrency tests can share the store across goroutines without the
// clock itself being the race.
func newMemStore(t *testing.T) (*Store, *vfs.Mem) {
	t.Helper()
	m := vfs.NewMem()
	s, err := InitWithVFS("/", "agt", m)
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}
	s.now = monotonicClock()
	return s, m
}

// monotonicClock returns a deterministic clock that advances one second per
// call and is safe to call from multiple goroutines. Identical timestamps would
// otherwise mask ordering bugs in tests that compare Updated values.
func monotonicClock() func() time.Time {
	var mu sync.Mutex
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		tick = tick.Add(time.Second)
		return tick
	}
}
