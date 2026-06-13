//go:build unix

package exec

import (
	"bytes"
	"context"
	"errors"
	"os"
	osexec "os/exec"
	"syscall"
	"time"
)

// osRunner spawns real processes. It is the production Runner (NewOS).
//
// Timeout handling uses the os/exec context machinery (Go 1.20+): the command
// runs under a deadline context, Cmd.Cancel sends SIGTERM, and Cmd.WaitDelay
// gives the process KillGrace to exit before os/exec sends SIGKILL. This is the
// SIGTERM-then-SIGKILL grace of HOOK-SPEC §7, implemented without a manual
// goroutine.
type osRunner struct{}

func (osRunner) Run(spec Spec) Result {
	if len(spec.Argv) == 0 {
		return Result{Category: SpawnError, Err: errors.New("exec: empty argv")}
	}

	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	if spec.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
	}
	defer cancel()

	cmd := osexec.CommandContext(ctx, spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Stdin = bytes.NewReader(spec.Stdin)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// On timeout, send SIGTERM (default would be SIGKILL); WaitDelay then upgrades
	// to SIGKILL after KillGrace if the process has not exited.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = KillGrace

	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start)

	res := Result{
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		Duration: dur,
	}

	if err == nil {
		res.Category = Completed
		res.ExitCode = 0
		return res
	}

	// A timeout kill must be classified as Timeout even though it surfaces as a
	// signal kill — check the context first.
	if ctx.Err() == context.DeadlineExceeded {
		res.Category = Timeout
		res.Err = err
		return res
	}

	var ee *osexec.ExitError
	if errors.As(err, &ee) {
		if ee.Exited() {
			res.Category = Completed
			res.ExitCode = ee.ExitCode()
			return res
		}
		// Ran but killed by a signal we did not send.
		res.Category = Signaled
		res.Err = err
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			res.Signal = int(ws.Signal())
		}
		return res
	}

	// Could not start (binary missing / not executable / other execve failure).
	res.Category = SpawnError
	res.Err = err
	return res
}
