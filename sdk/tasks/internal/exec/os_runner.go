// Copyright 2026 Hans Kohlreiter
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

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
//
// The hook runs in its own process group so the timeout signal reaches its whole
// tree, not just the process taskmgr spawned — see killGroup.
type osRunner struct{}

// killGroup signals the entire process group led by pid. Hooks are started with
// Setpgid, so a hook's process-group id equals its pid and the negated pid
// addresses the group.
//
// Signalling the group rather than the single child matters because HOOK-SPEC
// §3.2 points projects at ["sh", "-c", ...] for shell features: the shell exits
// on SIGTERM but its children do not receive it, keep running, and keep the
// captured stdout/stderr pipes open. os/exec then cannot finish Wait until
// WaitDelay expires, so the store lock is held for the full KillGrace on top of
// hook_timeout (HOOK-SPEC §8) and the children are orphaned afterwards.
//
// pid must be positive: kill(-0) would signal taskmgr's own process group and
// kill(-1) every process the user can reach.
func killGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(-pid, sig)
}

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

	// Run the hook in its own process group so a timeout can signal its whole
	// tree (killGroup) — but only when there IS a timeout to enforce.
	//
	// The detachment costs the hook its place in the terminal's foreground
	// group, so a Ctrl-C aimed at taskmgr no longer reaches it. That is only
	// acceptable while hook_timeout bounds it instead. With hook_timeout: 0
	// ("disables the limit", buildHookSet) the context is Background, whose
	// Done() is nil, so os/exec starts no cancellation watcher at all: neither
	// Cancel nor WaitDelay can ever fire. A hook detached under that config
	// would be bounded by nothing and interruptible by nothing — a hang the user
	// has to find with ps. An unlimited hook therefore stays in taskmgr's group,
	// where Ctrl-C still reaches both.
	if spec.Timeout > 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		// On timeout, send SIGTERM to the whole group (the default would SIGKILL
		// the spawned process alone); WaitDelay then upgrades to SIGKILL after
		// KillGrace if it has not exited. A hook that ignores SIGTERM therefore
		// still costs hook_timeout + KillGrace, the documented ceiling
		// (HOOK-SPEC §8).
		cmd.Cancel = func() error { return killGroup(cmd.Process.Pid, syscall.SIGTERM) }
		cmd.WaitDelay = KillGrace
	}

	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start)

	// Captured after Run: Process is set by Start and survives Wait, and the
	// group id equals the leader's pid under Setpgid. A pid that is still a
	// process-group id is not recycled while the group has members, so this stays
	// addressed to the hook's own tree.
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

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
		// Sweep the group. os/exec's own escalation after WaitDelay is
		// Process.Kill(), a SIGKILL to the leader alone, so a child that ignored
		// the group SIGTERM — the ["sh", "-c", ...] tree killGroup exists for —
		// outlives the run as an orphan. SIGTERM went to the group; SIGKILL has
		// to as well, or the escalation only half happens. ESRCH (nothing left)
		// is the expected case and is discarded.
		_ = killGroup(pid, syscall.SIGKILL)
		res.Category = Timeout
		res.Err = err
		return res
	}

	// Classify from the authoritative process state rather than the error type.
	// ProcessState is nil only when the process never ran (execve failure); once
	// it ran, st.Exited() distinguishes a normal exit (any code) from a signal
	// kill. Driving off ProcessState — not *ExitError — correctly handles the
	// WaitDelay case (the process exited but a child held the pipes open, so Run
	// returns ErrWaitDelay, not an *ExitError): the real exit code still wins.
	st := cmd.ProcessState
	if st == nil {
		res.Category = SpawnError
		res.Err = err
		return res
	}
	if st.Exited() {
		res.Category = Completed
		res.ExitCode = st.ExitCode()
		return res
	}
	// Ran but did not exit normally → killed by a signal we did not send.
	res.Category = Signaled
	res.Err = err
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		res.Signal = int(ws.Signal())
	}
	return res
}
