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

package vfs_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// pathsUntil receives from changes until it gets last, and returns the
// distinct paths that came before it. A test writes last after every other
// write, so the result holds all that the watch reported for them.
func pathsUntil(t *testing.T, changes <-chan string, last string) []string {
	t.Helper()
	var paths []string
	deadline := time.After(5 * time.Second)
	for {
		select {
		case path, ok := <-changes:
			if !ok {
				t.Fatalf("channel closed before %q, after %q", last, paths)
			}
			if path == last {
				return paths
			}
			if !slices.Contains(paths, path) {
				paths = append(paths, path)
			}
		case <-deadline:
			t.Fatalf("no %q within the deadline, got %q", last, paths)
		}
	}
}

func TestMem_Watch_ReportsDirAndNamedSubdirsOnly(t *testing.T) {
	m := vfs.NewMem()
	for _, dir := range []string{"/store/closed", "/store/packages", "/elsewhere"} {
		if err := m.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	changes, err := m.Watch(t.Context(), "/store", []string{"closed", "comments"})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	writes := []func() error{
		func() error { return m.WriteAtomic("/store/a.md", nil, 0o644) },
		func() error { return m.Rename("/store/a.md", "/store/closed/a.md") },
		func() error { return m.MkdirAll("/store/comments", 0o755) },
		func() error { return m.Append("/store/comments/a.yml", nil, 0o644) },
		func() error { return m.WriteAtomic("/store/packages/p.yaml", nil, 0o644) },
		func() error { return m.WriteAtomic("/elsewhere/b.md", nil, 0o644) },
		func() error { return m.Remove("/store/closed/a.md") },
		func() error { return m.WriteAtomic("/store/last", nil, 0o644) },
	}
	for i, write := range writes {
		if err := write(); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	got := pathsUntil(t, changes, "/store/last")
	want := []string{"/store/a.md", "/store/closed/a.md", "/store/comments", "/store/comments/a.yml"}
	if !slices.Equal(got, want) {
		t.Errorf("paths = %q, want %q", got, want)
	}
}

func TestMem_Watch_FailedWrite_ReportsNothing(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/store", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	changes, err := m.Watch(t.Context(), "/store", nil)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	m.FailOn("WriteAtomic", "/store/a.md", errors.New("disk full"))

	if err := m.WriteAtomic("/store/a.md", nil, 0o644); err == nil {
		t.Fatal("WriteAtomic succeeded, want the injected error")
	}
	if err := m.WriteAtomic("/store/last", nil, 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	if got := pathsUntil(t, changes, "/store/last"); len(got) != 0 {
		t.Errorf("paths = %q, want none", got)
	}
}

func TestMem_Watch_MissingDir_IsNotExist(t *testing.T) {
	_, err := vfs.NewMem().Watch(t.Context(), "/store", nil)

	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Watch error = %v, want os.ErrNotExist", err)
	}
}

func TestMem_Watch_ContextDone_ClosesChannel(t *testing.T) {
	m := vfs.NewMem()
	if err := m.MkdirAll("/store", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	changes, err := m.Watch(ctx, "/store", nil)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	cancel()

	select {
	case _, ok := <-changes:
		if ok {
			t.Fatal("got a path, want the channel closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channel still open")
	}
}
