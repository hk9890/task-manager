//go:build unix

package vfs

import (
	"fmt"
	"os"
	"syscall"
)

// Lock acquires an exclusive advisory flock on path, creating the file if
// necessary. It blocks until the lock is available. The returned unlock
// function releases the lock and closes the file descriptor.
func (osFS) Lock(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("vfs.Lock open: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("vfs.Lock flock: %w", err)
	}
	unlock := func() error {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
			_ = f.Close()
			return fmt.Errorf("vfs.Lock unlock: %w", err)
		}
		return f.Close()
	}
	return unlock, nil
}
