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
	"errors"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
)

// ---- L1: pure interpretation of an exec.Result (HOOK-SPEC §6.1/§7) ----

func TestClassifyResult(t *testing.T) {
	cases := []struct {
		name     string
		res      exec.Result
		wantDec  hookDecision
		wantMsg  string
		wantExit int
	}{
		{"allow with hint", exec.Result{Category: exec.Completed, ExitCode: 0, Stdout: []byte("hint")}, decAllow, "hint", 0},
		{"allow no message", exec.Result{Category: exec.Completed, ExitCode: 0}, decAllow, "", 0},
		{"deny with reason on stderr", exec.Result{Category: exec.Completed, ExitCode: 1, Stderr: []byte("nope")}, decDeny, "nope", 1},
		{"deny generic when silent", exec.Result{Category: exec.Completed, ExitCode: 5}, decDeny, "denied (exit 5)", 5},
		{"deny boundary 125", exec.Result{Category: exec.Completed, ExitCode: 125}, decDeny, "denied (exit 125)", 125},
		{"not executable 126", exec.Result{Category: exec.Completed, ExitCode: 126}, decError, "not executable (exit 126)", 126},
		{"not found 127", exec.Result{Category: exec.Completed, ExitCode: 127}, decError, "not executable (exit 127)", 127},
		{"exit 128+N is a signal death", exec.Result{Category: exec.Completed, ExitCode: 130}, decError, "killed by signal 2", 130},
		{"exit 255 not mislabeled", exec.Result{Category: exec.Completed, ExitCode: 255}, decError, "killed by signal 127", 255},
		{"timeout", exec.Result{Category: exec.Timeout}, decError, "timed out", -1},
		{"spawn error", exec.Result{Category: exec.SpawnError, Err: errors.New("boom")}, decError, "could not execute: boom", -1},
		{"signaled", exec.Result{Category: exec.Signaled, Signal: 9}, decError, "killed by signal 9", 128 + 9},
	}
	for _, c := range cases {
		dec, msg, exit := classifyResult(c.res)
		if dec != c.wantDec || msg != c.wantMsg || exit != c.wantExit {
			t.Errorf("%s: got (%v,%q,%d), want (%v,%q,%d)", c.name, dec, msg, exit, c.wantDec, c.wantMsg, c.wantExit)
		}
	}
}

func TestHookMessage_StdoutThenStderr(t *testing.T) {
	if got := hookMessage(exec.Result{Stdout: []byte(" out \n"), Stderr: []byte("err")}); got != "out" {
		t.Errorf("stdout present: got %q, want trimmed stdout", got)
	}
	if got := hookMessage(exec.Result{Stderr: []byte(" err \n")}); got != "err" {
		t.Errorf("stdout empty: got %q, want trimmed stderr", got)
	}
	if got := hookMessage(exec.Result{}); got != "" {
		t.Errorf("both empty: got %q, want empty", got)
	}
}

// ---- L2: orchestration with a fake runner ----

// hookTestStore builds a real-temp store with the given hooks and a fake runner.
func hookTestStore(t *testing.T, fake *exec.Fake, hooks []Hook) (*Store, *hookSet) {
	t.Helper()
	s, err := Init(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	s.runner = fake
	s.cfg.Hooks = hooks
	hs, err := s.hooks()
	if err != nil {
		t.Fatalf("build hookSet: %v", err)
	}
	return s, hs
}

func envVal(spec exec.Spec, key string) string {
	for _, e := range spec.Env {
		if strings.HasPrefix(e, key+"=") {
			return strings.TrimPrefix(e, key+"=")
		}
	}
	return ""
}

// byHookID returns a Fake.Func that dispatches on the TASKMGR_HOOK_ID env var.
func byHookID(m map[string]exec.Result) func(exec.Spec) exec.Result {
	return func(spec exec.Spec) exec.Result {
		return m[envVal(spec, "TASKMGR_HOOK_ID")]
	}
}

func feature(id string) *Issue {
	return &Issue{ID: id, Title: "t", Status: StatusOpen, Type: TypeFeature, Priority: 2}
}

func TestRunPre_AllowAggregatesHints(t *testing.T) {
	fake := &exec.Fake{Func: byHookID(map[string]exec.Result{
		"a": exec.Allow("hintA"),
		"b": exec.Allow("hintB"),
	})}
	s, hs := hookTestStore(t, fake, []Hook{
		{ID: "a", Event: "pre-close", Run: []string{"a"}},
		{ID: "b", Event: "pre-close", Run: []string{"b"}},
	})

	hints, denial, err := s.runPre(hs, "pre-close", feature("x-1"), feature("x-1"), nil)
	if err != nil || denial != nil {
		t.Fatalf("expected allow, got denial=%v err=%v", denial, err)
	}
	if len(hints) != 2 || hints[0] != "hintA" || hints[1] != "hintB" {
		t.Errorf("hints = %v, want [hintA hintB] in order", hints)
	}
	if len(fake.Calls()) != 2 {
		t.Errorf("ran %d hooks, want 2", len(fake.Calls()))
	}
}

func TestRunPre_DenyShortCircuits(t *testing.T) {
	fake := &exec.Fake{Func: byHookID(map[string]exec.Result{
		"a": exec.Allow("hintA"),
		"b": exec.Deny(1, "reasonB"),
		"c": exec.Allow("hintC"),
	})}
	s, hs := hookTestStore(t, fake, []Hook{
		{ID: "a", Event: "pre-close", Run: []string{"a"}},
		{ID: "b", Event: "pre-close", Run: []string{"b"}},
		{ID: "c", Event: "pre-close", Run: []string{"c"}},
	})

	hints, denial, err := s.runPre(hs, "pre-close", feature("x-1"), feature("x-1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if denial == nil {
		t.Fatal("expected a denial")
	}
	if denial.Hook != "b" || denial.Reason != "reasonB" || denial.Exit != 1 {
		t.Errorf("denial = %+v, want hook=b reason=reasonB exit=1", denial)
	}
	if len(denial.Hints) != 1 || denial.Hints[0] != "hintA" {
		t.Errorf("denial.Hints = %v, want [hintA] (gathered before the deny)", denial.Hints)
	}
	if len(hints) != 1 || hints[0] != "hintA" {
		t.Errorf("returned hints = %v, want [hintA]", hints)
	}
	// c must never run (deny short-circuits).
	if n := len(fake.Calls()); n != 2 {
		t.Errorf("ran %d hooks, want 2 (a, b); c must not run", n)
	}
}

func TestRunPre_WhenFiltersAgainstNew(t *testing.T) {
	fake := &exec.Fake{Func: byHookID(map[string]exec.Result{"feat-gate": exec.Deny(1, "blocked")})}
	s, hs := hookTestStore(t, fake, []Hook{
		{ID: "feat-gate", Event: "pre-close", When: `type == "feature"`, Run: []string{"g"}},
	})

	// new is a bug -> when does not match -> hook not selected -> no denial.
	bug := &Issue{ID: "x-9", Status: StatusOpen, Type: TypeBug, Priority: 2}
	_, denial, err := s.runPre(hs, "pre-close", bug, bug, nil)
	if err != nil {
		t.Fatal(err)
	}
	if denial != nil {
		t.Fatalf("bug must not match `type==feature`, got denial %v", denial)
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("hook ran %d times on a non-matching issue, want 0", len(fake.Calls()))
	}

	// new is a feature -> matches -> denial fires.
	_, denial, err = s.runPre(hs, "pre-close", feature("x-1"), feature("x-1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if denial == nil {
		t.Fatal("feature must match the when clause and be denied")
	}
}

func TestRunPre_TimeoutAndNotExecutableAreDenials(t *testing.T) {
	cases := map[string]struct {
		res       exec.Result
		wantExit  int
		reasonHas string
	}{
		"timeout":        {exec.Result{Category: exec.Timeout}, -1, "timed out"},
		"not executable": {exec.Result{Category: exec.Completed, ExitCode: 127}, 127, "not executable"},
		"spawn error":    {exec.Result{Category: exec.SpawnError, Err: errors.New("no binary")}, -1, "could not execute"},
	}
	for name, c := range cases {
		fake := &exec.Fake{Func: func(exec.Spec) exec.Result { return c.res }}
		s, hs := hookTestStore(t, fake, []Hook{{ID: "g", Event: "pre-close", Run: []string{"g"}}})
		_, denial, err := s.runPre(hs, "pre-close", feature("x-1"), feature("x-1"), nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if denial == nil {
			t.Fatalf("%s: expected a denial (fail-closed)", name)
		}
		if denial.Exit != c.wantExit || !strings.Contains(denial.Reason, c.reasonHas) {
			t.Errorf("%s: denial = exit %d reason %q, want exit %d reason containing %q",
				name, denial.Exit, denial.Reason, c.wantExit, c.reasonHas)
		}
	}
}

func TestRunPost_FailuresBecomeWarningsAndDoNotStop(t *testing.T) {
	fake := &exec.Fake{Func: byHookID(map[string]exec.Result{
		"p1": exec.Deny(1, "p1 failed"),
		"p2": exec.Allow("p2 hint"),
		"p3": {Category: exec.SpawnError, Err: errors.New("gone")},
	})}
	s, hs := hookTestStore(t, fake, []Hook{
		{ID: "p1", Event: "post-close", Run: []string{"p1"}},
		{ID: "p2", Event: "post-close", Run: []string{"p2"}},
		{ID: "p3", Event: "post-close", Run: []string{"p3"}},
	})

	hints, warnings := s.runPost(hs, "post-close", feature("x-1"), feature("x-1"))
	// All three run (no short-circuit for post-hooks).
	if len(fake.Calls()) != 3 {
		t.Errorf("ran %d post-hooks, want 3 (no short-circuit)", len(fake.Calls()))
	}
	if len(hints) != 1 || hints[0] != "p2 hint" {
		t.Errorf("hints = %v, want [p2 hint]", hints)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want 2 (p1 deny, p3 spawn error)", warnings)
	}
	if !strings.Contains(warnings[0], "p1") || !strings.Contains(warnings[1], "p3") {
		t.Errorf("warnings = %v, want to name p1 and p3", warnings)
	}
}

func TestRunOne_DeliversPayloadAndEnv(t *testing.T) {
	fake := &exec.Fake{} // default allow
	s, hs := hookTestStore(t, fake, []Hook{{ID: "g", Event: "pre-close", Run: []string{"prog", "arg"}}})

	newIss := feature("x-7")
	if _, _, err := s.runPre(hs, "pre-close", feature("x-7"), newIss, nil); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("ran %d hooks, want 1", len(calls))
	}
	spec := calls[0]
	if spec.Argv[0] != "prog" || spec.Dir != s.root {
		t.Errorf("argv/dir = %v / %q, want [prog ...] and repo root %q", spec.Argv, spec.Dir, s.root)
	}
	if got := envVal(spec, "TASKMGR_HOOK_EVENT"); got != "pre-close" {
		t.Errorf("TASKMGR_HOOK_EVENT = %q", got)
	}
	if got := envVal(spec, "TASKMGR_HOOK_ID"); got != "g" {
		t.Errorf("TASKMGR_HOOK_ID = %q", got)
	}
	if got := envVal(spec, "TASKMGR_ISSUE_ID"); got != "x-7" {
		t.Errorf("TASKMGR_ISSUE_ID = %q, want x-7", got)
	}
	if got := envVal(spec, "TASKMGR_STORE"); got != s.dir {
		t.Errorf("TASKMGR_STORE = %q, want %q", got, s.dir)
	}
	if got := envVal(spec, "TASKMGR_PAYLOAD_SCHEMA"); got != "1" {
		t.Errorf("TASKMGR_PAYLOAD_SCHEMA = %q, want 1", got)
	}
	// Stdin carries the payload envelope for this event/issue.
	if !strings.Contains(string(spec.Stdin), `"event":"pre-close"`) ||
		!strings.Contains(string(spec.Stdin), `"issue_id":"x-7"`) {
		t.Errorf("stdin payload missing event/issue_id: %s", spec.Stdin)
	}
}

func TestRunPre_NoHooksForEventIsNoop(t *testing.T) {
	fake := &exec.Fake{}
	s, hs := hookTestStore(t, fake, []Hook{{ID: "g", Event: "post-close", Run: []string{"g"}}})
	hints, denial, err := s.runPre(hs, "pre-close", feature("x-1"), feature("x-1"), nil)
	if err != nil || denial != nil || hints != nil {
		t.Errorf("no pre-close hooks: got hints=%v denial=%v err=%v, want all nil", hints, denial, err)
	}
	if len(fake.Calls()) != 0 {
		t.Error("no hooks should run")
	}
}
