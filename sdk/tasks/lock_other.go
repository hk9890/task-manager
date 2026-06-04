//go:build !unix

package tasks

// withLock is a no-op fallback on platforms without flock. The supported
// targets (Linux for atctl and beads-workbench) use the unix implementation.
func (s *Store) withLock(fn func() error) error {
	return fn()
}
