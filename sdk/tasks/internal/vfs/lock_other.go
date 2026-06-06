//go:build !unix

package vfs

import (
	"fmt"
	"runtime"
)

// Lock fails closed on platforms without flock. FS.Lock is unix-only today
// (see fs.go). A caller on a non-unix target receives an error immediately
// rather than silently proceeding without mutual exclusion.
func (osFS) Lock(path string) (func() error, error) {
	return nil, fmt.Errorf("vfs.Lock: file locking unsupported on %s; build for a unix target", runtime.GOOS)
}
