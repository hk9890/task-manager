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

// config_test.go — in-process tests for the `taskmgr config` tree. Never call
// t.Parallel here: Run drives package-level state (see run_test.go).

import (
	"encoding/json"
	"strings"
	"testing"
)

// isolatedHome points TASKMGR_HOME at an empty temp dir for one test, so a
// --global write lands there instead of in the binary-wide home TestMain set.
func isolatedHome(t *testing.T) {
	t.Helper()
	t.Setenv("TASKMGR_HOME", t.TempDir())
}

// ── the key catalog ──────────────────────────────────────────────────────────

func TestConfigKeys_ListsBothScopesAndNeedsNoStore(t *testing.T) {
	out, errOut, code := run(t, "config", "keys")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	for _, want := range []string{"prefix", "hook_timeout", "central_root", "version", "store", "global"} {
		if !strings.Contains(out, want) {
			t.Errorf("`config keys` output does not mention %q:\n%s", want, out)
		}
	}
}

func TestConfigKeys_JSONCarriesScopeAndWritability(t *testing.T) {
	out, errOut, code := run(t, "--json", "config", "keys")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	var got []struct {
		Key      string `json:"key"`
		Scope    string `json:"scope"`
		Writable bool   `json:"writable"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	for _, k := range got {
		if k.Key == "prefix" && k.Writable {
			t.Error("prefix is reported writable; it is baked into every issue ID")
		}
		if k.Key == "central_root" && (!k.Writable || k.Scope != "global") {
			t.Errorf("central_root = %+v, want a writable global key", k)
		}
	}
}

// ── scalar keys on a store ───────────────────────────────────────────────────

func TestConfigSetGetUnset_RoundTripsHookTimeout(t *testing.T) {
	root := newStore(t)

	if _, errOut, code := run(t, "--dir", root, "config", "set", "hook_timeout", "5m"); code != 0 {
		t.Fatalf("set: exit %d, stderr %q", code, errOut)
	}
	out, _, code := run(t, "--dir", root, "config", "get", "hook_timeout")
	if code != 0 || strings.TrimSpace(out) != "5m" {
		t.Fatalf("get = %q (exit %d), want 5m", out, code)
	}
	if _, errOut, code := run(t, "--dir", root, "config", "unset", "hook_timeout"); code != 0 {
		t.Fatalf("unset: exit %d, stderr %q", code, errOut)
	}
	out, _, code = run(t, "--dir", root, "config", "get", "hook_timeout")
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("get after unset = %q (exit %d), want empty", out, code)
	}
}

func TestConfigSet_RejectsAnUnparseableHookTimeoutAndKeepsTheOldValue(t *testing.T) {
	root := newStore(t)
	if _, _, code := run(t, "--dir", root, "config", "set", "hook_timeout", "5m"); code != 0 {
		t.Fatal("setup: set hook_timeout")
	}

	_, errOut, code := run(t, "--dir", root, "config", "set", "hook_timeout", "nonsense")
	if code == 0 {
		t.Fatal("an unparseable duration must not be accepted")
	}
	if !strings.Contains(errOut, "nonsense") {
		t.Errorf("stderr %q does not name the offending value", errOut)
	}
	out, _, _ := run(t, "--dir", root, "config", "get", "hook_timeout")
	if strings.TrimSpace(out) != "5m" {
		t.Errorf("hook_timeout = %q after a refused write, want the previous 5m", strings.TrimSpace(out))
	}
}

func TestConfigSet_RefusesAReadOnlyKey(t *testing.T) {
	root := newStore(t)

	_, errOut, code := run(t, "--dir", root, "config", "set", "prefix", "other")
	if code == 0 {
		t.Fatal("prefix must not be settable")
	}
	if !strings.Contains(errOut, "read-only") {
		t.Errorf("stderr = %q, want it to say the key is read-only", errOut)
	}
	out, _, _ := run(t, "--dir", root, "config", "get", "prefix")
	if strings.TrimSpace(out) != "tst" {
		t.Errorf("prefix = %q, want it untouched", strings.TrimSpace(out))
	}
}

func TestConfigGet_UnknownKeyNamesTheKnownOnes(t *testing.T) {
	root := newStore(t)

	_, errOut, code := run(t, "--dir", root, "config", "get", "nosuchkey")
	if code == 0 {
		t.Fatal("an unknown key must fail")
	}
	if !strings.Contains(errOut, "hook_timeout") || !strings.Contains(errOut, "config keys") {
		t.Errorf("stderr = %q, want the known keys and a pointer to 'config keys'", errOut)
	}
}

func TestConfigList_JSONCarriesScopeAndPath(t *testing.T) {
	root := newStore(t)

	out, errOut, code := run(t, "--dir", root, "--json", "config", "list")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	var got struct {
		Scope string `json:"scope"`
		Path  string `json:"path"`
		Keys  []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"keys"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if got.Scope != "store" {
		t.Errorf("scope = %q, want store", got.Scope)
	}
	if !strings.HasSuffix(got.Path, "config.yaml") {
		t.Errorf("path = %q, want the store's config.yaml", got.Path)
	}
	if len(got.Keys) == 0 {
		t.Error("no keys reported")
	}
}

// ── hooks ────────────────────────────────────────────────────────────────────

func TestConfigHook_AddListRemoveRoundTrip(t *testing.T) {
	root := newStore(t)

	_, errOut, code := run(t, "--dir", root, "config", "hook", "add",
		"--id", "gate", "--event", "pre-create", "--when", `type == "doc"`,
		"--run", "sh", "--run", "-c", "--run", "exit 1")
	if code != 0 {
		t.Fatalf("hook add: exit %d, stderr %q", code, errOut)
	}

	out, _, code := run(t, "--dir", root, "--json", "config", "hook", "list")
	if code != 0 {
		t.Fatalf("hook list: exit %d", code)
	}
	var hooks []struct {
		ID    string   `json:"id"`
		Scope string   `json:"scope"`
		Event string   `json:"event"`
		When  string   `json:"when"`
		Run   []string `json:"run"`
	}
	if err := json.Unmarshal([]byte(out), &hooks); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(hooks) != 1 {
		t.Fatalf("listed %d hooks, want 1", len(hooks))
	}
	if hooks[0].ID != "gate" || hooks[0].Scope != "store" || hooks[0].Event != "pre-create" {
		t.Errorf("hook = %+v, want id=gate scope=store event=pre-create", hooks[0])
	}
	if strings.Join(hooks[0].Run, "|") != "sh|-c|exit 1" {
		t.Errorf("run = %v, want each --run to be one argv element", hooks[0].Run)
	}

	if _, errOut, code := run(t, "--dir", root, "config", "hook", "rm", "gate"); code != 0 {
		t.Fatalf("hook rm: exit %d, stderr %q", code, errOut)
	}
	out, _, _ = run(t, "--dir", root, "--json", "config", "hook", "list")
	if strings.TrimSpace(out) != "[]" && strings.TrimSpace(out) != "null" {
		t.Errorf("hook list after rm = %q, want empty", out)
	}
}

// TestConfigHook_ListShowsTheDefaultedID pins the id a denial reports for a hook
// that declared none — the id `config hook rm` then has to accept.
func TestConfigHook_ListShowsTheDefaultedID(t *testing.T) {
	root := newStore(t)
	if _, _, code := run(t, "--dir", root, "config", "hook", "add",
		"--event", "post-close", "--run", "/bin/true"); code != 0 {
		t.Fatal("setup: hook add")
	}

	out, _, _ := run(t, "--dir", root, "config", "hook", "list")
	if !strings.Contains(out, "post-close#0") {
		t.Fatalf("hook list = %q, want the defaulted id post-close#0", out)
	}
	if _, errOut, code := run(t, "--dir", root, "config", "hook", "rm", "post-close#0"); code != 0 {
		t.Fatalf("rm by defaulted id: exit %d, stderr %q", code, errOut)
	}
}

func TestConfigHook_AddRejectsAnUnknownEventAndWritesNothing(t *testing.T) {
	root := newStore(t)

	_, errOut, code := run(t, "--dir", root, "config", "hook", "add",
		"--event", "pre-delete", "--run", "/bin/true")
	if code == 0 {
		t.Fatal("an unknown event must be refused")
	}
	if !strings.Contains(errOut, "unknown event") {
		t.Errorf("stderr = %q, want it to name the unknown event", errOut)
	}
	out, _, _ := run(t, "--dir", root, "config", "hook", "list")
	if !strings.Contains(out, "no hooks") {
		t.Errorf("hook list = %q, want the refused hook not to have been written", out)
	}
}

func TestConfigHook_AddRequiresEventAndRun(t *testing.T) {
	root := newStore(t)

	if _, errOut, code := run(t, "--dir", root, "config", "hook", "add", "--run", "/bin/true"); code == 0 {
		t.Error("a hook with no event must be refused")
	} else if !strings.Contains(errOut, "--event") {
		t.Errorf("stderr = %q, want it to name --event", errOut)
	}
	if _, errOut, code := run(t, "--dir", root, "config", "hook", "add", "--event", "pre-create"); code == 0 {
		t.Error("a hook with no run must be refused")
	} else if !strings.Contains(errOut, "--run") {
		t.Errorf("stderr = %q, want it to name --run", errOut)
	}
}

func TestConfigHook_AddRejectsADuplicateID(t *testing.T) {
	root := newStore(t)
	if _, _, code := run(t, "--dir", root, "config", "hook", "add",
		"--id", "gate", "--event", "pre-create", "--run", "/bin/true"); code != 0 {
		t.Fatal("setup: hook add")
	}

	_, errOut, code := run(t, "--dir", root, "config", "hook", "add",
		"--id", "gate", "--event", "pre-close", "--run", "/bin/true")
	if code == 0 {
		t.Fatal("a duplicate id must be refused: 'hook rm' could not then name one of the two")
	}
	if !strings.Contains(errOut, "gate") {
		t.Errorf("stderr = %q, want it to name the clashing id", errOut)
	}
}

func TestConfigHook_RmUnknownIDListsTheConfiguredOnes(t *testing.T) {
	root := newStore(t)
	if _, _, code := run(t, "--dir", root, "config", "hook", "add",
		"--id", "gate", "--event", "pre-create", "--run", "/bin/true"); code != 0 {
		t.Fatal("setup: hook add")
	}

	_, errOut, code := run(t, "--dir", root, "config", "hook", "rm", "nosuchhook")
	if code == 0 {
		t.Fatal("removing an unknown id must fail")
	}
	if !strings.Contains(errOut, "gate") {
		t.Errorf("stderr = %q, want it to list the configured ids", errOut)
	}
}

// ── --global ─────────────────────────────────────────────────────────────────

func TestConfigGlobal_WorksWithoutAStore(t *testing.T) {
	isolatedHome(t)

	if _, errOut, code := run(t, "config", "set", "--global", "central_root", "/tmp/somewhere"); code != 0 {
		t.Fatalf("set --global: exit %d, stderr %q", code, errOut)
	}
	out, _, code := run(t, "config", "get", "--global", "central_root")
	if code != 0 || strings.TrimSpace(out) != "/tmp/somewhere" {
		t.Fatalf("get --global = %q (exit %d), want /tmp/somewhere", out, code)
	}
	if _, _, code := run(t, "config", "list", "--global"); code != 0 {
		t.Errorf("list --global: exit %d", code)
	}
}

// TestConfigGlobal_HookIDCarriesTheScopePrefix is what makes a denial reason say
// which of the two files to edit.
func TestConfigGlobal_HookIDCarriesTheScopePrefix(t *testing.T) {
	isolatedHome(t)

	out, errOut, code := run(t, "config", "hook", "add", "--global",
		"--id", "doc-needs-path", "--event", "pre-create",
		"--when", `type == "doc"`, "--run", "/bin/false")
	if code != 0 {
		t.Fatalf("hook add --global: exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "global:doc-needs-path") {
		t.Errorf("stdout = %q, want the effective id global:doc-needs-path", out)
	}

	out, _, _ = run(t, "config", "hook", "list", "--global")
	if !strings.Contains(out, "global:doc-needs-path") {
		t.Errorf("hook list --global = %q, want the prefixed id", out)
	}
	if _, errOut, code := run(t, "config", "hook", "rm", "--global", "global:doc-needs-path"); code != 0 {
		t.Fatalf("hook rm --global: exit %d, stderr %q", code, errOut)
	}
}

// TestConfigGlobal_HooksApplyToAStoreThatHasNone is the end-to-end shape of the
// feature: one gate configured once, enforced in a store whose own config is
// empty.
func TestConfigGlobal_HooksApplyToAStoreThatHasNone(t *testing.T) {
	isolatedHome(t)
	root := newStore(t)

	if _, errOut, code := run(t, "config", "hook", "add", "--global",
		"--id", "doc-needs-path", "--event", "pre-create",
		"--when", `type == "doc" && !(label ~ "path:")`,
		"--run", "sh", "--run", "-c", "--run", "echo 'a doc needs a path: label' >&2; exit 1"); code != 0 {
		t.Fatalf("hook add --global: exit %d, stderr %q", code, errOut)
	}

	_, errOut, code := run(t, "--dir", root, "create", "--title", "Auth design", "--type", "doc")
	if code == 0 {
		t.Fatal("the global gate did not deny a doc with no path: label")
	}
	if !strings.Contains(errOut, "global:doc-needs-path") {
		t.Errorf("stderr = %q, want the denial to name the global hook", errOut)
	}

	if _, errOut, code := run(t, "--dir", root, "create", "--title", "Auth design",
		"--type", "doc", "--label", "path:design/auth"); code != 0 {
		t.Fatalf("a labelled doc must pass the gate: exit %d, stderr %q", code, errOut)
	}
	if _, errOut, code := run(t, "--dir", root, "create", "--title", "ordinary"); code != 0 {
		t.Fatalf("the when clause must leave non-docs alone: exit %d, stderr %q", code, errOut)
	}
}
