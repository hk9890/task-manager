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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These tests exercise the real OS runner by spawning tiny, deterministic
// processes (sh, true, sleep) and prove the seam's exit-code, stdin, env,
// timeout, and spawn-failure mechanics that the Fake cannot. HOOK-SPEC §6.1/§7.
//
// Every process here exits within milliseconds. The two that wait out a real
// SIGTERM→SIGKILL escalation on a process tree are seconds each, and live in
// os_runner_kill_test.go behind the integration tag.

func TestOSRunner_AllowExitZero(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "exit 0"}})
	if res.Category != Completed || res.ExitCode != 0 {
		t.Fatalf("got category=%v exit=%d, want Completed/0", res.Category, res.ExitCode)
	}
}

func TestOSRunner_DenyExitCode(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "echo nope >&2; exit 3"}})
	if res.Category != Completed || res.ExitCode != 3 {
		t.Fatalf("got category=%v exit=%d, want Completed/3", res.Category, res.ExitCode)
	}
	if got := strings.TrimSpace(string(res.Stderr)); got != "nope" {
		t.Fatalf("stderr = %q, want %q", got, "nope")
	}
}

func TestOSRunner_CapturesStdout(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "echo hello"}})
	if got := strings.TrimSpace(string(res.Stdout)); got != "hello" {
		t.Fatalf("stdout = %q, want %q", got, "hello")
	}
}

func TestOSRunner_StdinDelivered(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "cat"}, Stdin: []byte("payload-123")})
	if got := string(res.Stdout); got != "payload-123" {
		t.Fatalf("stdout = %q, want stdin echoed back", got)
	}
}

func TestOSRunner_EnvExtrasLayered(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{
		Argv: []string{"sh", "-c", "printf %s \"$TASKMGR_HOOK_EVENT\""},
		Env:  []string{"TASKMGR_HOOK_EVENT=pre-close"},
	})
	if got := string(res.Stdout); got != "pre-close" {
		t.Fatalf("env var not delivered: stdout = %q", got)
	}
}

func TestOSRunner_InheritsParentEnv(t *testing.T) {
	t.Setenv("TASKMGR_OS_RUNNER_PROBE", "inherited")
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "printf %s \"$TASKMGR_OS_RUNNER_PROBE\""}})
	if got := string(res.Stdout); got != "inherited" {
		t.Fatalf("parent env not inherited: stdout = %q", got)
	}
}

func TestOSRunner_WorkingDir(t *testing.T) {
	dir := t.TempDir()
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "pwd"}, Dir: dir})
	// macOS /tmp is a symlink to /private/tmp; compare resolved suffix.
	got := strings.TrimSpace(string(res.Stdout))
	if !strings.HasSuffix(got, filepath.Base(dir)) {
		t.Fatalf("pwd = %q, want a dir ending in %q", got, filepath.Base(dir))
	}
}

func TestOSRunner_SpawnErrorMissingBinary(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"this-binary-does-not-exist-xyzzy"}})
	if res.Category != SpawnError {
		t.Fatalf("got category=%v, want SpawnError", res.Category)
	}
	if res.Err == nil {
		t.Fatal("SpawnError must carry a diagnostic Err")
	}
}

func TestOSRunner_EmptyArgvIsSpawnError(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: nil})
	if res.Category != SpawnError {
		t.Fatalf("got category=%v, want SpawnError", res.Category)
	}
}

func TestOSRunner_Timeout(t *testing.T) {
	r := NewOS()
	start := time.Now()
	res := r.Run(Spec{Argv: []string{"sleep", "30"}, Timeout: 100 * time.Millisecond})
	elapsed := time.Since(start)
	if res.Category != Timeout {
		t.Fatalf("got category=%v, want Timeout", res.Category)
	}
	// Must return promptly after the timeout (well under the 30s sleep, allowing
	// for the SIGTERM->SIGKILL grace).
	if elapsed > KillGrace+5*time.Second {
		t.Fatalf("timeout took %v, expected prompt kill", elapsed)
	}
}

func TestOSRunner_TimeoutZeroDisables(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "exit 0"}, Timeout: 0})
	if res.Category != Completed {
		t.Fatalf("got category=%v, want Completed (no timeout)", res.Category)
	}
}

// An unlimited hook must stay in the caller's process group; a bounded one must
// not.
//
// Detaching costs the hook its place in the terminal's foreground group, so
// Ctrl-C no longer reaches it. That trade is only sound while hook_timeout
// bounds it instead — and with hook_timeout: 0 the context is Background, whose
// Done() is nil, so os/exec starts no cancellation watcher and neither Cancel
// nor WaitDelay ever fires. Detaching there would leave a hook that nothing
// bounds and nothing can interrupt.
func TestOSRunner_ProcessGroupOnlyWhenBounded(t *testing.T) {
	r := NewOS()
	own := syscall.Getpgrp()

	pgidOf := func(t *testing.T, timeout time.Duration) int {
		t.Helper()
		res := r.Run(Spec{Argv: []string{"sh", "-c", "ps -o pgid= -p $$"}, Timeout: timeout})
		if res.Category != Completed || res.ExitCode != 0 {
			t.Fatalf("probe failed: category=%v exit=%d stderr=%q", res.Category, res.ExitCode, res.Stderr)
		}
		pgid, err := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
		if err != nil {
			t.Fatalf("parse pgid %q: %v", res.Stdout, err)
		}
		return pgid
	}

	if got := pgidOf(t, 0); got != own {
		t.Errorf("unbounded hook pgid = %d, want the caller's group %d — Ctrl-C cannot reach it and no timeout will either", got, own)
	}
	if got := pgidOf(t, 10*time.Second); got == own {
		t.Errorf("bounded hook pgid = %d: it must be detached so the timeout can signal its whole tree", got)
	}
}

func TestOSRunner_DurationRecorded(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "exit 0"}})
	if res.Duration <= 0 {
		t.Fatalf("duration = %v, want > 0", res.Duration)
	}
}

// Sanity: the seam is the only place that reads os.Environ; confirm it actually
// does by checking PATH is present (so a bare argv like "sh" resolves).
func TestOSRunner_PathResolvesBareCommand(t *testing.T) {
	if os.Getenv("PATH") == "" {
		t.Skip("no PATH in environment")
	}
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"true"}})
	if res.Category != Completed || res.ExitCode != 0 {
		t.Fatalf("bare 'true' did not resolve via PATH: category=%v exit=%d", res.Category, res.ExitCode)
	}
}
