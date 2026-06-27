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

package exec

import "testing"

func TestFake_DefaultAllows(t *testing.T) {
	f := &Fake{}
	res := f.Run(Spec{Argv: []string{"x"}})
	if res.Category != Completed || res.ExitCode != 0 {
		t.Fatalf("default Fake.Run = %v/%d, want Completed/0", res.Category, res.ExitCode)
	}
}

func TestFake_RecordsCalls(t *testing.T) {
	f := &Fake{}
	f.Run(Spec{Argv: []string{"a"}, Env: []string{"K=1"}})
	f.Run(Spec{Argv: []string{"b"}})
	calls := f.Calls()
	if len(calls) != 2 {
		t.Fatalf("recorded %d calls, want 2", len(calls))
	}
	if calls[0].Argv[0] != "a" || calls[1].Argv[0] != "b" {
		t.Fatalf("calls out of order: %v", calls)
	}
	// Calls returns a slice-level copy: appending to or reordering the returned
	// slice must not affect the Fake's own record.
	calls[0], calls[1] = calls[1], calls[0]
	calls = append(calls, Spec{Argv: []string{"c"}})
	_ = calls
	again := f.Calls()
	if len(again) != 2 || again[0].Argv[0] != "a" || again[1].Argv[0] != "b" {
		t.Fatalf("Calls() must return an independent copy; got %v", again)
	}
}

func TestFake_FuncDrivesResult(t *testing.T) {
	f := &Fake{Func: func(s Spec) Result {
		for _, e := range s.Env {
			if e == "TASKMGR_HOOK_ID=denier" {
				return Deny(1, "blocked")
			}
		}
		return Allow("hinted")
	}}

	deny := f.Run(Spec{Env: []string{"TASKMGR_HOOK_ID=denier"}})
	if deny.Category != Completed || deny.ExitCode != 1 {
		t.Fatalf("deny = %v/%d, want Completed/1", deny.Category, deny.ExitCode)
	}
	if string(deny.Stderr) != "blocked" {
		t.Fatalf("deny reason on stderr = %q, want %q", deny.Stderr, "blocked")
	}

	allow := f.Run(Spec{Env: []string{"TASKMGR_HOOK_ID=other"}})
	if allow.ExitCode != 0 || string(allow.Stdout) != "hinted" {
		t.Fatalf("allow = exit %d stdout %q, want 0/hinted", allow.ExitCode, allow.Stdout)
	}
}

func TestAllowDenyHelpers(t *testing.T) {
	if a := Allow(""); a.ExitCode != 0 || a.Stdout != nil {
		t.Fatalf("Allow(\"\") = %+v, want exit 0 no stdout", a)
	}
	if a := Allow("hi"); string(a.Stdout) != "hi" {
		t.Fatalf("Allow hint on stdout = %q", a.Stdout)
	}
	if d := Deny(7, "why"); d.ExitCode != 7 || string(d.Stderr) != "why" {
		t.Fatalf("Deny = %+v, want exit 7 stderr why", d)
	}
}
