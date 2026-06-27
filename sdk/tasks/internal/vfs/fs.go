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

// Package vfs is the disk seam: the only package in sdk/tasks that is
// permitted to import os or syscall. All file operations go through the FS
// interface so that tests can substitute an in-memory implementation without
// touching the real filesystem.
package vfs

import (
	"errors"
	"os"
)

// FS is the single gateway to all filesystem operations used by the store.
// Every method mirrors a standard os/syscall operation; callers use the seam
// instead of calling os directly so the implementation can be swapped for
// tests or fault injection.
type FS interface {
	// ReadDir reads the directory named by dir and returns a list of entries.
	ReadDir(dir string) ([]os.DirEntry, error)

	// ReadFile reads the named file and returns its contents.
	ReadFile(name string) ([]byte, error)

	// Stat returns the FileInfo for the named file.
	Stat(name string) (os.FileInfo, error)

	// WriteAtomic writes data to name atomically: write to a temp file in the
	// same directory, fsync, then rename over name. On a crash the target is
	// either fully written or untouched — never torn.
	WriteAtomic(name string, data []byte, perm os.FileMode) error

	// Append opens name (creating it if necessary) with O_APPEND and writes
	// data, then fsyncs. Used for the immutable comment sidecar.
	Append(name string, data []byte, perm os.FileMode) error

	// Rename atomically renames (moves) oldpath to newpath.
	Rename(oldpath, newpath string) error

	// MkdirAll creates dir and any necessary parents with the given perm.
	MkdirAll(dir string, perm os.FileMode) error

	// Remove removes the named file or empty directory.
	Remove(name string) error

	// Lock acquires an exclusive advisory lock on the file at path (creating it
	// if necessary). It returns an unlock function that must be called to
	// release the lock. The lock is process-wide advisory (flock on unix).
	//
	// Platform contract: Lock is implemented only for unix targets (Linux, macOS,
	// etc.). On non-unix targets it returns an error immediately (fails closed)
	// rather than silently providing no mutual exclusion. Production builds must
	// target a unix OS.
	Lock(path string) (unlock func() error, err error)

	// Getwd returns the current working directory.
	Getwd() (string, error)

	// EvalSymlinks returns path with any symbolic links resolved. It requires
	// the path to exist; a non-existent path returns an error so the caller can
	// fall back to the lexical (cleaned) path. Used by store resolution to
	// compare canonical project paths (CONFIG-SPEC §4).
	EvalSymlinks(path string) (string, error)
}

// IsNotExist reports whether err represents a "file not found" condition,
// matching os.ErrNotExist. Callers use this instead of os.IsNotExist so that
// the os import stays inside this package.
func IsNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
