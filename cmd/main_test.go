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

package cmd

import (
	"os"
	"testing"
)

// TestMain pins TASKMGR_HOME at an empty temp directory for the whole test
// binary — the in-process Run tests and, because a forked binary inherits the
// environment, the L4 subprocess tests too.
//
// A store reads the per-user config on its first write, to inherit global hooks
// (HOOK-SPEC §3.5). Without this the suite would read whatever the developer has
// in ~/.taskmgr/config.yaml, so one machine-wide gate on a contributor's laptop
// would fail tests that pass in CI. The central-store tests set their own
// TASKMGR_HOME after this one, which still wins.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "taskmgr-home-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("TASKMGR_HOME", home); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
