// Package env is the environment seam: the only package in sdk/tasks besides
// vfs (disk) and exec (hook processes) permitted to import os. Store resolution
// (CONFIG-SPEC) must read the user's home directory and the TASKMGR_* variables;
// concentrating that here keeps the rest of sdk/tasks free of os, exactly as vfs
// does for the filesystem and exec for processes, so resolution stays
// hermetically testable with no real HOME or environment touched.
package env

// Environment is the seam for reading the user environment during store
// resolution. The OS implementation (NewOS) reads the real process environment;
// Fake scripts values for tests.
type Environment interface {
	// Getenv returns the value of the named environment variable, or "" if it is
	// unset (matching os.Getenv).
	Getenv(key string) string

	// UserHomeDir returns the current user's home directory (matching
	// os.UserHomeDir): $HOME on unix.
	UserHomeDir() (string, error)
}

// NewOS returns an Environment backed by the real process environment.
func NewOS() Environment { return osEnv{} }
