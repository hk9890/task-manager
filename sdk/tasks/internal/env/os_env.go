package env

import "os"

// osEnv is the real-environment implementation of Environment. It is, alongside
// vfs.osFS and exec's OS runner, one of the few types in sdk/tasks permitted to
// import os.
type osEnv struct{}

func (osEnv) Getenv(key string) string { return os.Getenv(key) }

func (osEnv) UserHomeDir() (string, error) { return os.UserHomeDir() }
