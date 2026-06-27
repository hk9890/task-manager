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

import "sync"

// Fake is a Runner that scripts results instead of spawning processes, so hook
// orchestration can be unit-tested without real binaries (the exec analogue of
// vfs.Mem). It records every invocation in Calls for assertions.
type Fake struct {
	// Func computes the Result for a given Spec. When nil, Run returns a plain
	// allow (Completed, exit 0). Tests typically branch on spec.Env (e.g. the
	// TASKMGR_HOOK_ID variable) to decide per-hook outcomes.
	Func func(Spec) Result

	mu    sync.Mutex
	calls []Spec
}

// Run records the spec and returns the scripted result.
func (f *Fake) Run(spec Spec) Result {
	f.mu.Lock()
	f.calls = append(f.calls, spec)
	f.mu.Unlock()
	if f.Func != nil {
		return f.Func(spec)
	}
	return Allow("")
}

// Calls returns a copy of the specs Run was invoked with, in order.
func (f *Fake) Calls() []Spec {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Spec, len(f.calls))
	copy(out, f.calls)
	return out
}

// Allow builds an allow/OK result (exit 0). A non-empty hint is placed on
// stdout, where HOOK-SPEC §6.1 reads an allowing hook's message from.
func Allow(hint string) Result {
	r := Result{Category: Completed, ExitCode: 0}
	if hint != "" {
		r.Stdout = []byte(hint)
	}
	return r
}

// Deny builds a deny/warning result with the given non-zero exit code and a
// reason on stderr (HOOK-SPEC §6.1 reads a denying hook's reason from stderr
// when stdout is empty).
func Deny(code int, reason string) Result {
	r := Result{Category: Completed, ExitCode: code}
	if reason != "" {
		r.Stderr = []byte(reason)
	}
	return r
}
