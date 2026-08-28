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

package tasks

import (
	"os"
	"testing"
)

// TestMain pins TASKMGR_HOME at an empty temp directory for the whole binary.
//
// A Store reads the per-user config on its first write, to inherit global hooks
// (HOOK-SPEC §3.5). Without this the suite would read whatever the developer has
// in ~/.taskmgr/config.yaml, so one machine-wide gate on a contributor's laptop
// would fail tests that pass in CI. Tests that want a home of their own set
// TASKMGR_HOME themselves, or inject env.Fake.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "taskmgr-home-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv(envTaskmgrHome, home); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
