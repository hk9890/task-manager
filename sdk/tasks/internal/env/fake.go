package env

import "errors"

// Fake is an Environment that returns scripted values, so store resolution can
// be unit-tested hermetically (the env analogue of vfs.Mem) without reading the
// real HOME or process environment.
type Fake struct {
	// Vars holds environment variables; Getenv returns Vars[key] (or "").
	Vars map[string]string
	// Home is the UserHomeDir result. When empty, UserHomeDir returns an error,
	// matching os.UserHomeDir on a machine with no $HOME.
	Home string
}

func (f Fake) Getenv(key string) string { return f.Vars[key] }

func (f Fake) UserHomeDir() (string, error) {
	if f.Home == "" {
		return "", errors.New("env: $HOME is not defined")
	}
	return f.Home, nil
}

// compile-time check that Fake satisfies Environment.
var _ Environment = Fake{}
