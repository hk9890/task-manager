package vfs

import (
	"fmt"
	"os"
	"path/filepath"
)

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
	return nil
}

// Append opens name with O_APPEND (creating it if absent) and writes data,
// then fsyncs before closing.
func (osFS) Append(name string, data []byte, perm os.FileMode) error {
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
	return f.Close()
}

func (osFS) Rename(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}

func (osFS) MkdirAll(dir string, perm os.FileMode) error {
	return os.MkdirAll(dir, perm)
}

func (osFS) Remove(name string) error {
	return os.Remove(name)
}

func (osFS) Getwd() (string, error) {
	return os.Getwd()
}
