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

package vfs_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

func TestOsFS_Watch_WriteAtomic_ReportsTheTargetAndNoTempFile(t *testing.T) {
	dir := t.TempDir()
	fs := vfs.NewOS()
	changes, err := fs.Watch(t.Context(), dir, nil)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	target, last := filepath.Join(dir, "a.md"), filepath.Join(dir, "last")

	for _, name := range []string{target, last} {
		if err := fs.WriteAtomic(name, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteAtomic: %v", err)
		}
	}

	if got, want := pathsUntil(t, changes, last), []string{target}; !slices.Equal(got, want) {
		t.Errorf("paths = %q, want %q", got, want)
	}
}

func TestOsFS_Watch_NamedSubdirCreatedLater_IsWatched(t *testing.T) {
	dir := t.TempDir()
	fs := vfs.NewOS()
	changes, err := fs.Watch(t.Context(), dir, []string{"closed"})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	closed, other := filepath.Join(dir, "closed"), filepath.Join(dir, "other")
	last := filepath.Join(dir, "last")

	for _, sub := range []string{closed, other} {
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
	}
	// The watch on closed starts before its path is sent, so receiving up to
	// here makes the writes below race-free.
	if err := fs.WriteAtomic(last, nil, 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if got, want := pathsUntil(t, changes, last), []string{closed, other}; !slices.Equal(got, want) {
		t.Fatalf("paths = %q, want %q", got, want)
	}

	inClosed := filepath.Join(closed, "a.md")
	for _, name := range []string{inClosed, filepath.Join(other, "b.md"), filepath.Join(closed, "last")} {
		if err := fs.WriteAtomic(name, nil, 0o644); err != nil {
			t.Fatalf("WriteAtomic: %v", err)
		}
	}

	if got, want := pathsUntil(t, changes, filepath.Join(closed, "last")), []string{inClosed}; !slices.Equal(got, want) {
		t.Errorf("paths = %q, want %q", got, want)
	}
}

func TestOsFS_Watch_MissingDir_IsNotExist(t *testing.T) {
	_, err := vfs.NewOS().Watch(t.Context(), filepath.Join(t.TempDir(), "absent"), nil)

	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Watch error = %v, want os.ErrNotExist", err)
	}
}
