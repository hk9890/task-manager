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

package vfs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// fsyncDirFn is the seam every caller in this file goes through instead of
// calling fsyncDir directly.
//
// A directory fsync leaves no trace an assertion can reach: the write succeeds
// either way, the bytes read back either way, and the difference shows up only
// as a lost file after a crash — which a test cannot stage. So the tests that
// carried durability in their names asserted the only thing they could see,
// that the call returned nil, and the fsync could be deleted with all of them
// green. Routing through a variable makes "the parent directory was synced" an
// observable event, and makes a failing fsync injectable.
//
// It is only ever reassigned by tests in this package (fsyncdir_internal_test.go).
var fsyncDirFn = fsyncDir

// fsyncDir opens the directory at dir, calls Sync, and closes it.
// This ensures that a preceding rename or new-file creation is durable:
// the directory entry itself must be flushed to disk so the operation
// survives a crash between the rename and the next journal checkpoint.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("vfs fsyncDir open: %w", err)
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return fmt.Errorf("vfs fsyncDir sync: %w", err)
	}
	if err := d.Close(); err != nil {
		return fmt.Errorf("vfs fsyncDir close: %w", err)
	}
	return nil
}

// osFS is the real filesystem implementation of FS. It is the only type in
// the entire sdk that is allowed to call os.*, filepath.*, and syscall.*.
type osFS struct{}

// NewOS returns an FS backed by the real operating-system filesystem.
func NewOS() FS { return osFS{} }

func (osFS) ReadDir(dir string) ([]os.DirEntry, error) {
	return os.ReadDir(dir)
}

func (osFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (osFS) Stat(name string) (os.FileInfo, error) {
	return os.Stat(name)
}

// WriteAtomic writes data atomically using the temp-fsync-rename pattern.
// It creates a temp file in the same directory as name (so the rename is
// guaranteed to be on the same filesystem), syncs, and renames over name.
func (osFS) WriteAtomic(name string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(name)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("vfs.WriteAtomic create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Always clean up the temp file; the remove is a no-op if rename succeeded.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("vfs.WriteAtomic write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("vfs.WriteAtomic sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("vfs.WriteAtomic close: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("vfs.WriteAtomic chmod: %w", err)
	}
	if err := os.Rename(tmpName, name); err != nil {
		return fmt.Errorf("vfs.WriteAtomic rename: %w", err)
	}
	if err := fsyncDirFn(dir); err != nil {
		return fmt.Errorf("vfs.WriteAtomic fsyncDir: %w", err)
	}
	return nil
}

// Append opens name with O_APPEND (creating it if absent) and writes data,
// then fsyncs before closing. When the file did not exist before (a new dir
// entry is created), the parent directory is also fsynced so that the new
// entry is durable on crash.
func (osFS) Append(name string, data []byte, perm os.FileMode) error {
	// Detect whether the file already exists so we know if we are adding a new
	// directory entry. We stat before opening; if stat returns ErrNotExist the
	// file will be created by O_CREATE and the parent dir needs an fsync.
	_, statErr := os.Stat(name)
	isNew := os.IsNotExist(statErr)

	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, perm)
	if err != nil {
		return fmt.Errorf("vfs.Append open: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("vfs.Append write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("vfs.Append sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("vfs.Append close: %w", err)
	}
	if isNew {
		if err := fsyncDirFn(filepath.Dir(name)); err != nil {
			return fmt.Errorf("vfs.Append fsyncDir: %w", err)
		}
	}
	return nil
}

func (osFS) Rename(oldpath, newpath string) error {
	if err := os.Rename(oldpath, newpath); err != nil {
		return fmt.Errorf("vfs.Rename: %w", err)
	}
	// fsync the destination directory so the rename that publishes the new
	// name is crash-durable. When src and dst are in different directories,
	// both entries change; for the common (same-dir) case one fsync covers
	// both. Cross-dir renames are not used by the store today.
	if err := fsyncDirFn(filepath.Dir(newpath)); err != nil {
		return fmt.Errorf("vfs.Rename fsyncDir: %w", err)
	}
	return nil
}

// MoveTree moves the directory tree at src to dst (see FS.MoveTree). The
// same-filesystem path is a plain rename; the cross-filesystem path copies the
// tree and then removes the source, which is deliberately not atomic.
func (osFS) MoveTree(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("vfs.MoveTree: destination already exists: %s", dst)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("vfs.MoveTree stat destination: %w", err)
	}
	err := os.Rename(src, dst)
	if err == nil {
		if err := fsyncDirFn(filepath.Dir(dst)); err != nil {
			return fmt.Errorf("vfs.MoveTree fsyncDir: %w", err)
		}
		return nil
	}
	if !isCrossDevice(err) {
		return fmt.Errorf("vfs.MoveTree rename: %w", err)
	}
	if err := copyTree(src, dst); err != nil {
		return fmt.Errorf("vfs.MoveTree copy: %w", err)
	}
	// Publish the new directory entry durably *before* unlinking the source, so
	// a crash in between cannot leave neither copy on disk. copyTree fsyncs the
	// files and the directories it creates, but not the parent it links dst
	// into; the rename branch above gets this from its own fsyncDir.
	if err := fsyncDirFn(filepath.Dir(dst)); err != nil {
		return fmt.Errorf("vfs.MoveTree fsyncDir: %w", err)
	}
	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("vfs.MoveTree remove source (a complete copy is at %s; the source may be partly removed): %w", dst, err)
	}
	return nil
}

// copyTree recursively copies src to dst, which must not exist. Directories and
// regular files are copied with their permissions; any other file type (symlink,
// device, socket, fifo) is an error. A store holds neither, so hitting one means
// the source is not the tree the caller thinks it is — and failing loudly here is
// what keeps the caller from deleting a source it only partly copied.
func copyTree(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case fi.IsDir():
		// Create it traversable, populate, then Chmod to the source mode. Mkdir
		// and OpenFile subtract the process umask, so only an explicit Chmod
		// reproduces the permissions exactly — and doing it last means a
		// restrictive source mode cannot lock the copy out of its own children.
		if err := os.Mkdir(dst, 0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		if err := os.Chmod(dst, fi.Mode().Perm()); err != nil {
			return err
		}
		return fsyncDirFn(dst)
	case fi.Mode().IsRegular():
		return copyFile(src, dst, fi.Mode().Perm())
	default:
		return fmt.Errorf("unsupported file type %s: %s", fi.Mode().Type(), src)
	}
}

// copyFile copies the regular file src to dst (which must not exist) and fsyncs
// it before returning, so the content is durable once the tree copy completes.
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	// OpenFile's mode is masked by the umask; Chmod is not.
	if err := out.Chmod(perm); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func (osFS) MkdirAll(dir string, perm os.FileMode) error {
	return os.MkdirAll(dir, perm)
}

func (osFS) Remove(name string) error {
	return os.Remove(name)
}

func (osFS) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

func (osFS) Getwd() (string, error) {
	return os.Getwd()
}

func (osFS) EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
