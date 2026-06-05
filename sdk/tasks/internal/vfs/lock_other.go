//go:build !unix

package vfs

// Lock is a no-op fallback on platforms without flock. The supported
// production targets (Linux) use the unix implementation.
func (osFS) Lock(path string) (func() error, error) {
	return func() error { return nil }, nil
}
