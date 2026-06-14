//go:build !unix

package exec

import (
	"fmt"
	"runtime"
)

// osRunner fails closed on platforms without unix signals. Like vfs.Lock, hook
// execution is unix-only today; a non-unix target reports a spawn error rather
// than silently running hooks without the SIGTERM/SIGKILL timeout contract.
// Production builds must target a unix OS.
type osRunner struct{}

func (osRunner) Run(spec Spec) Result {
	return Result{
		Category: SpawnError,
		Err:      fmt.Errorf("exec: hook execution unsupported on %s; build for a unix target", runtime.GOOS),
	}
}
