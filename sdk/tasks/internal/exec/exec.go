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

// Package exec is the process seam: the only package in sdk/tasks besides vfs
// that is permitted to import os, os/exec, and syscall. Hook execution spawns
// external programs, which needs the parent environment, signals, and the
// process API; concentrating that here keeps the pure core (and the rest of
// sdk/tasks) free of os/syscall, exactly as vfs does for the filesystem.
//
// Callers build a Spec (argv, env extras, stdin, working dir, timeout) and get
// back a Result describing how the process ended (Category) plus its exit code,
// captured stdout/stderr, and wall-clock duration. The seam reports mechanics
// only — interpreting an exit code as allow/deny is the hook orchestration's
// job (HOOK-SPEC §6.1), not this package's.
package exec

import "time"

// Runner spawns a single external process and reports its outcome. The OS
// implementation (NewOS) runs a real process; Fake scripts results for tests so
// hook logic can be exercised without spawning anything.
type Runner interface {
	// Run executes spec to completion (or until Timeout) and returns the
	// outcome. It never returns an error separately from Result: a failure to
	// spawn, a timeout, or a signal kill are all reported via Result.Category
	// (with the underlying cause in Result.Err for diagnostics).
	Run(spec Spec) Result
}

// Spec describes one process invocation.
type Spec struct {
	// Argv is the command and its arguments, executed directly via execve with
	// no shell. Argv[0] is the binary. Must be non-empty.
	Argv []string
	// Dir is the working directory for the process (the repo root for hooks).
	Dir string
	// Env holds EXTRA environment variables ("KEY=value") layered on top of the
	// parent process environment, which the OS runner inherits. The seam owns
	// the os.Environ() read so callers stay free of the os import.
	Env []string
	// Stdin is written to the process's standard input, which is then closed.
	Stdin []byte
	// Timeout bounds the process wall-clock time. Zero disables the limit. On
	// expiry the process is sent SIGTERM, then SIGKILL after KillGrace.
	Timeout time.Duration
}

// Category describes how a process ended. The exit code is authoritative only
// for Completed; the other categories are failures to run cleanly.
type Category int

const (
	// Completed means the process ran and exited on its own; ExitCode holds its
	// status (0..255). Interpreting that code is the caller's job (HOOK-SPEC §6.1).
	Completed Category = iota
	// SpawnError means the process could not be started at all — binary missing,
	// not executable, or another execve failure. ExitCode is meaningless.
	SpawnError
	// Timeout means Spec.Timeout elapsed and the process was killed
	// (SIGTERM, then SIGKILL after KillGrace).
	Timeout
	// Signaled means the process was killed by a signal the seam did not send
	// (i.e. not the timeout path); Signal holds the signal number.
	Signaled
)

// Result is the outcome of a Run.
type Result struct {
	Category Category
	ExitCode int           // meaningful when Category == Completed
	Signal   int           // meaningful when Category == Signaled
	Stdout   []byte        // captured standard output
	Stderr   []byte        // captured standard error
	Duration time.Duration // wall-clock time the process ran
	Err      error         // underlying cause for non-Completed categories (for logging)
}

// KillGrace is how long the seam waits after SIGTERM before sending SIGKILL to
// a timed-out process. It is a fixed, small grace: hooks are expected to exit
// promptly on SIGTERM, and a longer wait only extends the lock hold (HOOK-SPEC
// §7/§8). HOOK-SPEC has a single global hook_timeout and no per-hook policy, so
// this grace is not configurable.
const KillGrace = 2 * time.Second

// NewOS returns a Runner that spawns real processes.
func NewOS() Runner { return osRunner{} }
