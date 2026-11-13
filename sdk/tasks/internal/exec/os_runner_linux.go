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
	osexec "os/exec"
	"runtime"
	"syscall"
)

// runKeptInGroup runs a KeepGroup process so that it dies with taskmgr. The
// group keeps it reachable by a Ctrl-C at the terminal; Pdeathsig makes the
// kernel send it SIGKILL when taskmgr dies by any other signal too — a kill of
// taskmgr alone, an OOM kill — which no group signal reaches.
//
// The kernel sends Pdeathsig when the thread that started the process exits,
// not the process. The Go runtime ends a thread only when a goroutine locked
// to it exits, so the goroutine is locked to its thread from the start until
// the process is reaped: the thread cannot end, and fire the signal, while
// the process runs.
func runKeptInGroup(cmd *osexec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return cmd.Run()
}
