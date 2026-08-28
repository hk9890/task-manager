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

// TestConfigGlobal_PackageAppliesToAStoreThatHasNone is the end-to-end shape of
// the feature: one gate configured once for the machine, enforced in a store
// whose own config names no package.
func TestConfigGlobal_PackageAppliesToAStoreThatHasNone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TASKMGR_HOME", home)
	writeCmdPackage(t, home, "doc-policy", `hooks:
  - id: doc-needs-path
    event: pre-create
    when: 'type == "doc" && !(label ~ "path:")'
    run: ["sh", "-c", "echo 'a doc needs a path: label' >&2; exit 1"]
`)
	root := newStore(t)

	if _, errOut, code := run(t, "package", "add", "--global", "doc-policy"); code != 0 {
		t.Fatalf("package add --global: exit %d, stderr %q", code, errOut)
	}

	_, errOut, code := run(t, "--dir", root, "create", "--title", "Auth design", "--type", "doc")
	if code == 0 {
		t.Fatal("the machine-wide gate did not deny a doc with no path: label")
	}
	// The denial names the hook by its effective id, which says which package to
	// open (HOOK-SPEC §3.5).
	if !strings.Contains(errOut, "pkg:doc-policy:doc-needs-path") {
		t.Errorf("stderr = %q, want the denial to name the hook by its effective id", errOut)
	}

	if _, errOut, code := run(t, "--dir", root, "create", "--title", "Auth design",
		"--type", "doc", "--label", "path:design/auth"); code != 0 {
		t.Fatalf("a labelled doc must pass the gate: exit %d, stderr %q", code, errOut)
	}
	if _, errOut, code := run(t, "--dir", root, "create", "--title", "ordinary"); code != 0 {
		t.Fatalf("the when clause must leave non-docs alone: exit %d, stderr %q", code, errOut)
	}
}

// ── the --global selector ────────────────────────────────────────────────────

// TestConfigGlobal_IsAcceptedInBothPositions: --global is a persistent flag on
// the group, so it works where a reader naturally puts it. Registered on the
// leaves only, `config --global list` failed as an unknown flag.
func TestConfigGlobal_IsAcceptedInBothPositions(t *testing.T) {
	isolatedHome(t)
	for _, args := range [][]string{
		{"config", "list", "--global"},
		{"config", "--global", "list"},
	} {
		out, errOut, code := run(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
		}
		if !strings.Contains(out, "scope: global") {
			t.Errorf("%v did not act on the per-user config:\n%s", args, out)
		}
	}
}

// ── set is not unset ─────────────────────────────────────────────────────────

// TestConfigSet_RefusesAnEmptyValue: a wrapper passing an unset shell variable
// used to delete the key and exit 0, reporting "Unset".
func TestConfigSet_RefusesAnEmptyValue(t *testing.T) {
	root := newStore(t)
	if _, errOut, code := run(t, "--dir", root, "config", "set", "hook_timeout", "5m"); code != 0 {
		t.Fatalf("setup: exit %d, stderr %q", code, errOut)
	}

	_, errOut, code := run(t, "--dir", root, "config", "set", "hook_timeout", "")
	if code == 0 {
		t.Fatal("an empty value must not be accepted by set")
	}
	if !strings.Contains(errOut, "config unset") {
		t.Errorf("stderr %q does not point at the command that clears a key", errOut)
	}
	out, _, _ := run(t, "--dir", root, "config", "get", "hook_timeout")
	if strings.TrimSpace(out) != "5m" {
		t.Errorf("hook_timeout = %q after a refused set, want the previous 5m", strings.TrimSpace(out))
	}
}
