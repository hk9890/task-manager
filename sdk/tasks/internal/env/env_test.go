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

// L1 tests for the environment seam's own boundaries.
//
// The package had no test file at all: every use of it went through store
// resolution, so a defect here surfaced as a confusing failure in a resolution
// test rather than at its source. Fake is what every one of those tests runs on,
// which makes its behaviour a dependency of theirs.
package env_test

import (
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
)

// A machine with no $HOME is a real configuration — a daemon, a container, a
// cron job — and os.UserHomeDir errors there. Fake must do the same, or a
// resolution test written against it proves nothing about the case it names.
func TestFake_UserHomeDir_EmptyHomeIsAnError(t *testing.T) {
	_, err := env.Fake{}.UserHomeDir()
	if err == nil {
		t.Error("UserHomeDir with no Home returned nil error; os.UserHomeDir fails on a machine with no $HOME")
	}
}

func TestFake_UserHomeDir_ReturnsTheScriptedHome(t *testing.T) {
	got, err := env.Fake{Home: "/home/u"}.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if got != "/home/u" {
		t.Errorf("UserHomeDir = %q, want /home/u", got)
	}
}

// Getenv mirrors os.Getenv: an unset variable is the empty string, not an error
// and not a missing-key panic.
func TestFake_Getenv(t *testing.T) {
	e := env.Fake{Vars: map[string]string{"TASKMGR_HOME": "/hm"}}

	if got := e.Getenv("TASKMGR_HOME"); got != "/hm" {
		t.Errorf("Getenv(TASKMGR_HOME) = %q, want /hm", got)
	}
	if got := e.Getenv("TASKMGR_ABSENT"); got != "" {
		t.Errorf("Getenv of an unset variable = %q, want the empty string", got)
	}
}

// A Fake with no Vars map at all must still answer Getenv. Store resolution
// constructs one that way whenever only Home matters.
func TestFake_Getenv_NilVarsMap(t *testing.T) {
	if got := (env.Fake{}).Getenv("TASKMGR_HOME"); got != "" {
		t.Errorf("Getenv on a Fake with no Vars = %q, want the empty string", got)
	}
}
