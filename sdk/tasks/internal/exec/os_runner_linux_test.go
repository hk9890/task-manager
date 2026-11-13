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

//go:build linux

package exec

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// helperEnv selects what the test binary does when the seam runs it as a
// child: helperPdeathsig prints the signal it is to get when its parent dies;
// helperKeptSleep runs a KeepGroup sh that writes its PID to the file the
// argument names and sleeps.
const (
	helperEnv       = "TASKMGR_EXEC_TEST_HELPER"
	helperPdeathsig = "pdeathsig"
	helperKeptSleep = "kept-sleep"
)

// TestOSRunner_HelperProcess is no test: it is the child the tests in this
// package start from the test binary, and returns at once when not one.
func TestOSRunner_HelperProcess(t *testing.T) {
	switch os.Getenv(helperEnv) {
	case helperPdeathsig:
		var sig int32
		if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_GET_PDEATHSIG, uintptr(unsafe.Pointer(&sig)), 0); errno != 0 {
			fmt.Fprintln(os.Stderr, errno)
			os.Exit(2)
		}
		fmt.Println(sig)
		os.Exit(0)
	case helperKeptSleep:
		pidFile := os.Args[len(os.Args)-1]
		NewOS().Run(Spec{Argv: []string{"sh", "-c", `echo $$ > "$0"; exec sleep 30`, pidFile}, KeepGroup: true})
		os.Exit(0)
	}
}

// helper is the Spec that runs the test binary as the helper mode names.
func helper(mode string, args ...string) Spec {
	return Spec{
		Argv: append([]string{os.Args[0], "-test.run=^TestOSRunner_HelperProcess$", "--"}, args...),
		Env:  []string{helperEnv + "=" + mode},
	}
}

// A KeepGroup process gets SIGKILL from the kernel when taskmgr dies, by any
// signal; any other process does not.
func TestOSRunner_KeepGroupDiesWithTheCaller(t *testing.T) {
	for _, keep := range []bool{true, false} {
		spec := helper(helperPdeathsig)
		spec.KeepGroup = keep
		res := NewOS().Run(spec)
		if res.Category != Completed || res.ExitCode != 0 {
			t.Fatalf("helper: category=%v exit=%d stderr=%q", res.Category, res.ExitCode, res.Stderr)
		}
		got, err := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
		if err != nil {
			t.Fatalf("helper printed %q: %v", res.Stdout, err)
		}
		want := 0
		if keep {
			want = int(syscall.SIGKILL)
		}
		if got != want {
			t.Errorf("KeepGroup %v: parent-death signal = %d, want %d", keep, got, want)
		}
	}
}
