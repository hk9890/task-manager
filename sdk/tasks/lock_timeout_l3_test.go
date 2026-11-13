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

//go:build integration

package tasks_test

// lock_timeout_l3_test.go — L3 for WithLockTimeout on a real temp dir: a real
// flock on .tasks/.lock, taken through a second open file as another writer
// takes it, bounds the wait of a write.

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

func TestWithLockTimeout_RealFlockHeld_StoreBusyWithinTheTimeout(t *testing.T) {
	t.Setenv("TASKMGR_HOME", t.TempDir())
	root := t.TempDir()
	const timeout = 100 * time.Millisecond
	s, err := tasks.Init(root, "demo", tasks.WithLockTimeout(timeout))
	if err != nil {
		t.Fatal(err)
	}
	release, err := vfs.NewOS().Lock(filepath.Join(root, tasks.DataDirName, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			_ = release()
		}
	})

	start := time.Now()
	_, err = s.Create(tasks.CreateInput{Title: "blocked"})
	waited := time.Since(start)
	if !errors.Is(err, tasks.ErrStoreBusy) {
		t.Fatalf("Create = %v, want ErrStoreBusy", err)
	}
	if waited < timeout || waited > 10*timeout {
		t.Errorf("Create gave up after %s, want about %s", waited, timeout)
	}

	released = true
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(tasks.CreateInput{Title: "free"}); err != nil {
		t.Fatalf("Create after the release = %v, want success", err)
	}
}
