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

//go:build linux && integration

// A KeepGroup process killed with its caller, by a real SIGKILL to the caller
// alone. Integration-tagged: it writes a PID file to a real temp dir.
package exec

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// alive reports whether pid runs: it exists and is no zombie waiting for a
// reaper.
func alive(pid int) bool {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	_, rest, _ := strings.Cut(string(stat), ") ")
	return !strings.HasPrefix(rest, "Z")
}

// A push must not outlive a taskmgr killed alone — by kill -9, by the OOM
// killer — where no Ctrl-C reaches the group: it would race the next publish.
func TestOSRunner_L3_KeepGroupProcessDiesWhenTheCallerIsKilled(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	spec := helper(helperKeptSleep, pidFile)
	caller := osexec.Command(spec.Argv[0], spec.Argv[1:]...)
	caller.Env = append(os.Environ(), spec.Env...)
	if err := caller.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = caller.Process.Kill(); _ = caller.Wait() }()

	var pid int
	for deadline := time.Now().Add(10 * time.Second); pid == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the kept process never wrote its PID")
		}
		if b, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()

	if err := caller.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = caller.Wait()
	for deadline := time.Now().Add(5 * time.Second); alive(pid); {
		if time.Now().After(deadline) {
			t.Fatalf("process %d still runs 5s after its caller was killed", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
