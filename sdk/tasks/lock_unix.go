//go:build unix

package tasks

import (
	"os"
	"path/filepath"
	"syscall"
)

// withLock runs fn while holding an exclusive advisory lock on the store, so
// concurrent atctl/bwb processes cannot interleave writes.
func (s *Store) withLock(fn func() error) error {
	f, err := os.OpenFile(filepath.Join(s.dir, lockFileName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	return fn()
}
