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
	"strings"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// L1: buildHookSet is pure (no filesystem), so compilation and the timeout rule
// are unit-tested directly against a resolved chain. HOOK-SPEC §3.1/§3.4/§3.5.

// chain builds the flat, already-ordered input buildHookSet takes, giving each
// entry the effective id a package would have composed.
func chain(pkg string, hooks ...Hook) []packageHook {
	out := make([]packageHook, 0, len(hooks))
	for _, h := range hooks {
		out = append(out, packageHook{id: packageHookID(pkg, h.ID), hook: h})
	}
	return out
}

func TestBuildHookSet_Defaults(t *testing.T) {
	hs, err := buildHookSet("", "", nil)
	if err != nil {
		t.Fatalf("empty chain: unexpected error %v", err)
	}
	if hs.timeout != defaultHookTimeout {
		t.Fatalf("default timeout = %v, want %v", hs.timeout, defaultHookTimeout)
	}
	if len(hs.hooks) != 0 {
		t.Fatalf("no hooks configured, got %d", len(hs.hooks))
	}
}

func TestBuildHookSet_Timeout(t *testing.T) {
	cases := []struct {
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{"", defaultHookTimeout, false},
		{"5m", 5 * time.Minute, false},
		{"500ms", 500 * time.Millisecond, false},
		{"0", 0, false},  // disables
		{"0s", 0, false}, // disables
		{"abc", 0, true}, // unparseable
		{"-1s", 0, true}, // negative
		{"10", 0, true},  // missing unit
	}
	for _, c := range cases {
		hs, err := buildHookSet("", c.raw, nil)
		if c.wantErr {
			if err == nil {
				t.Errorf("hook_timeout %q: want error, got none", c.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("hook_timeout %q: unexpected error %v", c.raw, err)
			continue
		}
		if hs.timeout != c.want {
			t.Errorf("hook_timeout %q: got %v, want %v", c.raw, hs.timeout, c.want)
		}
	}
}

// The store's value wins over the per-user one, and the per-user one is the
// fallback for a store that sets none (HOOK-SPEC §3.1).
func TestBuildHookSet_TimeoutFallsBackToGlobal(t *testing.T) {
	hs, err := buildHookSet("5m", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if hs.timeout != 5*time.Minute {
		t.Errorf("store unset: timeout = %v, want the per-user 5m", hs.timeout)
	}
	hs, err = buildHookSet("5m", "1s", nil)
	if err != nil {
		t.Fatal(err)
	}
	if hs.timeout != time.Second {
		t.Errorf("store set: timeout = %v, want the store's 1s", hs.timeout)
	}
}

func TestBuildHookSet_ValidHooks(t *testing.T) {
	hs, err := buildHookSet("", "", chain("policy",
		Hook{ID: "tests", Event: "pre-close", When: `type == "feature"`, Run: []string{"make", "test"}},
		Hook{ID: "notify", Event: "post-close", Run: []string{"./notify.sh"}},
	))
	if err != nil {
		t.Fatalf("valid hooks: unexpected error %v", err)
	}
	if len(hs.hooks) != 2 {
		t.Fatalf("compiled %d hooks, want 2", len(hs.hooks))
	}
	if hs.hooks[0].id != "pkg:policy:tests" || hs.hooks[0].event != "pre-close" || hs.hooks[0].when == nil {
		t.Errorf("hook[0] = %+v, want id=pkg:policy:tests event=pre-close with a compiled when", hs.hooks[0])
	}
	if hs.hooks[1].id != "pkg:policy:notify" || hs.hooks[1].event != "post-close" {
		t.Errorf("hook[1] = %+v, want id=pkg:policy:notify event=post-close", hs.hooks[1])
	}
	if hs.hooks[1].when != nil {
		t.Error("hook[1] has no when clause; predicate must be nil (always)")
	}
}

func TestBuildHookSet_InvalidHooks(t *testing.T) {
	cases := []struct {
		name    string
		hook    Hook
		errWant string
	}{
		{"unknown event", Hook{ID: "a", Event: "pre-delete", Run: []string{"x"}}, "unknown event"},
		{"missing event", Hook{ID: "a", Run: []string{"x"}}, "missing required field event"},
		{"empty run", Hook{ID: "a", Event: "pre-close"}, "non-empty argv"},
		{"blank program", Hook{ID: "a", Event: "pre-close", Run: []string{"   "}}, "non-empty argv"},
		{"bad when", Hook{ID: "a", Event: "pre-close", When: "type ==", Run: []string{"x"}}, "invalid when"},
	}
	for _, c := range cases {
		_, err := buildHookSet("", "", chain("p", c.hook))
		if err == nil {
			t.Errorf("%s: want error, got none", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.errWant) {
			t.Errorf("%s: error %q does not contain %q", c.name, err.Error(), c.errWant)
		}
	}
}

// An error names the hook by its effective id, so the reader knows which package
// to open and which entry inside it (HOOK-SPEC §3.5).
func TestCompileHook_ErrorNamesTheEffectiveID(t *testing.T) {
	_, err := buildHookSet("", "", chain("doc-policy", Hook{ID: "my-gate", Event: "bogus", Run: []string{"x"}}))
	if err == nil || !strings.Contains(err.Error(), "pkg:doc-policy:my-gate") {
		t.Fatalf("error should name the hook by its effective id: %v", err)
	}
}

func TestHookSet_ForEventPreservesChainOrder(t *testing.T) {
	hs, err := buildHookSet("", "", chain("p",
		Hook{ID: "a", Event: "pre-close", Run: []string{"a"}},
		Hook{ID: "b", Event: "post-close", Run: []string{"b"}},
		Hook{ID: "c", Event: "pre-close", Run: []string{"c"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	pre := hs.forEvent("pre-close")
	if len(pre) != 2 || pre[0].id != "pkg:p:a" || pre[1].id != "pkg:p:c" {
		t.Fatalf("forEvent(pre-close) = %v, want [a c] in chain order", pre)
	}
	if got := hs.forEvent("pre-update"); len(got) != 0 {
		t.Fatalf("forEvent(pre-update) = %v, want none", got)
	}
}

// L2: the lazy accessor builds once and surfaces the config error. Reads never
// call hooks(), so an unusable package does not break queries.
func TestStoreHooks_LazyBuildAndCache(t *testing.T) {
	s, err := InitWithVFS("/", "x", vfs.NewMem())
	if err != nil {
		t.Fatal(err)
	}
	storePackage(t, s, "bad", []Hook{{ID: "g", Event: "nope", Run: []string{"x"}}})

	hs1, err1 := s.hooks()
	hs2, err2 := s.hooks()
	if err1 == nil {
		t.Fatal("a malformed package must surface a config error from hooks()")
	}
	if err1 != err2 || hs1 != hs2 {
		t.Fatal("hooks() must cache its result (build once)")
	}
	// A read still works despite the malformed package.
	if _, err := s.All(); err != nil {
		t.Fatalf("read All() must be unaffected by a malformed package: %v", err)
	}
}

// A `use:` entry naming a package that is not there fails the write and names
// what is missing, while leaving every read working (HOOK-SPEC §1 principle 4).
func TestStoreHooks_MissingPackageFailsWritesNotReads(t *testing.T) {
	s, err := InitWithVFS("/", "x", vfs.NewMem())
	if err != nil {
		t.Fatal(err)
	}
	useRef(s, PackageRef{Path: "packages/absent"})

	_, err = s.Create(CreateInput{Title: "blocked by a missing package"})
	if err == nil {
		t.Fatal("a use entry that does not resolve must fail the write")
	}
	if !strings.Contains(err.Error(), "absent") || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("error %q must name the package and say it is not installed", err)
	}
	if _, err := s.All(); err != nil {
		t.Fatalf("read All() must be unaffected: %v", err)
	}
}
