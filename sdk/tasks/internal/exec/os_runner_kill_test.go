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

//go:build unix && integration

// Timeout escalation against a real process tree: SIGTERM to the group, then
// SIGKILL after KillGrace. These are the seam's slowest tests by two orders of
// magnitude — one waits out a grandchild's own sleep to prove it is gone rather
// than merely slow, the other waits out the full KillGrace, which HOOK-SPEC §8
// fixes at 2s and is not tunable for a test's convenience.
//
// They are integration-tagged for that reason, not because the coverage is
// optional: if the escalation regresses, the failure mode is detached processes
// left running on the developer's machine, which is exactly what the pre-commit
// suite must not do on every commit.
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

// A timed-out hook must take its children with it. HOOK-SPEC §3.2 points
// projects at ["sh", "-c", ...], so the process taskmgr spawns is usually a
// shell whose real work happens in children. Signalling only that shell leaves
// the children running and holding the captured pipes, which (a) stalls Wait for
// the full KillGrace on top of hook_timeout, doubling the lock hold, and (b)
// orphans them afterwards.
func TestOSRunner_TimeoutKillsChildren(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "survived")
	// A backgrounded grandchild that would touch the marker shortly after the
	// timeout, and a foreground sleep that holds the shell (and the pipes) open.
	// Job control is off in a non-interactive shell, so the background job stays
	// in the shell's process group and a group signal reaches it.
	script := "(sleep 1; touch " + marker + ") & sleep 30"

	r := NewOS()
	start := time.Now()
	res := r.Run(Spec{Argv: []string{"sh", "-c", script}, Timeout: 100 * time.Millisecond})
	elapsed := time.Since(start)

	if res.Category != Timeout {
		t.Fatalf("got category=%v, want Timeout", res.Category)
	}
	// The pipes must close as soon as the group dies. Waiting out KillGrace here
	// is the regression: it means a child outlived the signal.
	if elapsed >= KillGrace {
		t.Errorf("Run took %v (>= KillGrace %v): a child survived the timeout signal and held the pipes", elapsed, KillGrace)
	}

	// Give the grandchild more than its own sleep to prove it is gone, not just
	// slow. The margin is what costs wall-clock here, so keep it tight: 20% over
	// the grandchild's 1s is enough to distinguish "killed" from "still queued".
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("grandchild survived the timeout and kept running (orphaned)")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat marker: %v", err)
	}
}

// A child that IGNORES SIGTERM must still not outlive the run.
//
// os/exec's own escalation after WaitDelay is Process.Kill(), a SIGKILL to the
// spawned process alone. The group gets the SIGTERM but only the shell gets the
// SIGKILL, so a child that traps TERM — or re-execs under setsid — survives the
// command as an orphan holding whatever it holds. That is the exact leak the
// process group exists to prevent, so the escalation has to reach the group too.
func TestOSRunner_TimeoutSIGKILLsChildrenThatIgnoreSIGTERM(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	// Both the shell and its child ignore SIGTERM, so nothing exits until a
	// SIGKILL arrives. The parent records the child's pid via $! — inside the
	// subshell $$ would still be the parent's, which is the process os/exec
	// kills anyway, and the test would prove nothing.
	script := "(trap '' TERM; sleep 30) & echo $! > " + pidFile + "; trap '' TERM; sleep 30"

	// The subject is the escalation from SIGTERM to SIGKILL, not the deadline.
	// A 100ms timeout made that subject depend on the shell starting, forking and
	// writing the pid file inside 100ms: injecting a delay before the spawn broke
	// this test and nothing else in the repository, which is the signature of a
	// test that a loaded machine can fail for reasons unrelated to the code.
	// Half a second buys the margin and still fires the timeout immediately —
	// the script sleeps 30s either way.
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", script}, Timeout: 500 * time.Millisecond})
	if res.Category != Timeout {
		t.Fatalf("got category=%v, want Timeout", res.Category)
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parse child pid %q: %v", raw, err)
	}

	// Signal 0 probes for existence. Poll briefly: the SIGKILL is delivered as
	// Run returns, and reaping is not instantaneous.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return // gone
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL) // don't leak it out of the test
			t.Fatalf("child %d ignored SIGTERM and survived the timeout as an orphan", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
