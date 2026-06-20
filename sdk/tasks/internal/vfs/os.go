package vfs

import (
	"fmt"
	"os"
	"path/filepath"
)

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
	if err := fsyncDir(dir); err != nil {
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
		if err := fsyncDir(filepath.Dir(name)); err != nil {
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
	if err := fsyncDir(filepath.Dir(newpath)); err != nil {
		return fmt.Errorf("vfs.Rename fsyncDir: %w", err)
	}
	return nil
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

func (osFS) EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
