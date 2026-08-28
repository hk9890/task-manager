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

// run_test.go — in-process CLI tests through Run.
//
// These are the tests that used to require `go build` plus a fork, which is why
// they either sat behind the integration tag or did not exist. They run in the
// default (fast) suite. Never call t.Parallel here: Run drives package-level
// state, so invocations must stay sequential.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// run invokes the CLI in-process and returns stdout, stderr and the exit code.
func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	code = Run(args, &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), code
}

// newStore initialises a store in a temp dir and returns its root.
func newStore(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, err := tasks.Init(root, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return root
}

// ── the entry point itself ───────────────────────────────────────────────────

func TestRun_SucceedsAndWritesToTheGivenWriter(t *testing.T) {
	out, errOut, code := run(t, "version")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	if !strings.HasPrefix(out, "taskmgr ") {
		t.Errorf("stdout = %q, want it to start with %q", out, "taskmgr ")
	}
}

func TestRun_UnknownCommandExitsOneWithoutTouchingStdout(t *testing.T) {
	out, errOut, code := run(t, "nosuchcommand")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty — errors belong on stderr", out)
	}
	if errOut == "" {
		t.Error("stderr is empty; the failure must say something")
	}
}

// TestRun_FlagsDoNotLeakBetweenInvocations is the property that makes the
// package-level command tree safe to reuse: --json in one call must not survive
// into the next, and a repeatable flag must not accumulate across calls.
func TestRun_FlagsDoNotLeakBetweenInvocations(t *testing.T) {
	root := newStore(t)

	out, _, code := run(t, "--dir", root, "--json", "create", "--title", "first", "--label", "a")
	if code != 0 {
		t.Fatalf("create --json: exit %d", code)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("first call did not emit JSON: %q", out)
	}

	// No --json this time: the flag must have been reset.
	out, _, code = run(t, "--dir", root, "create", "--title", "second")
	if code != 0 {
		t.Fatalf("create: exit %d", code)
	}
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("--json leaked into the next invocation: %q", out)
	}

	// The repeatable --label from the first call must not still be set.
	out, _, code = run(t, "--dir", root, "--json", "create", "--title", "third")
	if code != 0 {
		t.Fatalf("create: exit %d", code)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse create JSON: %v (%q)", err, out)
	}
	show, _, code := run(t, "--dir", root, "--json", "show", created.ID)
	if code != 0 {
		t.Fatalf("show: exit %d", code)
	}
	var got struct {
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(show), &got); err != nil {
		t.Fatalf("parse show JSON: %v (%q)", err, show)
	}
	if len(got.Labels) != 0 {
		t.Errorf("labels = %v, want none — a repeatable flag accumulated across invocations", got.Labels)
	}
}

// ── output shapes (the DTO layer) ────────────────────────────────────────────

func TestRun_ShowJSON_HasTheDocumentedDetailShape(t *testing.T) {
	root := newStore(t)

	out, _, code := run(t, "--dir", root, "--json", "create", "--title", "parent task")
	if code != 0 {
		t.Fatalf("create: exit %d", code)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse create JSON: %v", err)
	}

	if _, _, code := run(t, "--dir", root, "comment", "add", created.ID, "a note"); code != 0 {
		t.Fatalf("comment add: exit %d", code)
	}

	out, _, code = run(t, "--dir", root, "--json", "show", created.ID)
	if code != 0 {
		t.Fatalf("show: exit %d", code)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse show JSON: %v (%q)", err, out)
	}
	for _, key := range []string{"id", "title", "status", "type", "priority", "created", "updated", "comments"} {
		if _, ok := dto[key]; !ok {
			t.Errorf("detail DTO is missing %q; got keys %v", key, keysOf(dto))
		}
	}
	comments, _ := dto["comments"].([]any)
	if len(comments) != 1 {
		t.Fatalf("comments = %v, want exactly one", dto["comments"])
	}
	c, _ := comments[0].(map[string]any)
	for _, key := range []string{"id", "created", "body"} {
		if _, ok := c[key]; !ok {
			t.Errorf("comment DTO is missing %q; got keys %v", key, keysOf(c))
		}
	}
}

func TestRun_ListJSON_IsAnArray(t *testing.T) {
	root := newStore(t)
	if _, _, code := run(t, "--dir", root, "create", "--title", "one"); code != 0 {
		t.Fatal("create failed")
	}

	out, _, code := run(t, "--dir", root, "--json", "list")
	if code != 0 {
		t.Fatalf("list: exit %d", code)
	}
	var issues []map[string]any
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		t.Fatalf("list --json must be an array: %v (%q)", err, out)
	}
	if len(issues) != 1 {
		t.Fatalf("list returned %d issues, want 1", len(issues))
	}
}

func TestRun_ListHuman_SaysNoneWhenEmpty(t *testing.T) {
	root := newStore(t)
	out, _, code := run(t, "--dir", root, "list")
	if code != 0 {
		t.Fatalf("list: exit %d", code)
	}
	if strings.TrimSpace(out) != "(none)" {
		t.Errorf("stdout = %q, want %q", out, "(none)")
	}
}

// ── misuse help ──────────────────────────────────────────────────────────────
//
// The bulk of this is in usage_render_test.go; these are the two cases that are
// about Run's own error handling rather than the rendered block.

func TestRun_BadFlagValue_IsMisuseNotACrash(t *testing.T) {
	root := newStore(t)
	_, errOut, code := run(t, "--dir", root, "list", "--sort", "nonsense")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, "nonsense") {
		t.Errorf("stderr does not name the rejected value:\n%s", errOut)
	}
}

func TestRun_NoStore_PointsAtInit(t *testing.T) {
	_, errOut, code := run(t, "--dir", filepath.Join(t.TempDir(), "empty"), "list")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, "taskmgr init") {
		t.Errorf("stderr should tell the user how to create a store:\n%s", errOut)
	}
}

// ── the generated command catalog ────────────────────────────────────────────
//
// The catalog output itself is asserted in commands_catalog_test.go; these pin
// the derivation helpers directly.

// TestExampleFor_GroupCommandsShowASubcommand pins the structural rule: a group
// is invoked through a subcommand, and detecting that via Runnable() would be
// wrong because the misuse layer attaches a RunE to groups at startup.
func TestExampleFor_GroupCommandsShowASubcommand(t *testing.T) {
	installUsageErrorsOnce()
	for _, name := range []string{"comment", "dep", "rel", "store"} {
		c := findCommand(t, name)
		got := exampleFor(c)
		if want := "taskmgr " + name + " <subcommand>"; got != want {
			t.Errorf("exampleFor(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestExampleFor_IncludesPositionalsAndRequiredFlags(t *testing.T) {
	if got, want := exampleFor(findCommand(t, "show")), "taskmgr show <id>"; got != want {
		t.Errorf("exampleFor(show) = %q, want %q", got, want)
	}
	if got := exampleFor(findCommand(t, "create")); !strings.Contains(got, "--title") {
		t.Errorf("exampleFor(create) = %q, want it to name the required --title", got)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func findCommand(t *testing.T, name string) *cobra.Command {
	t.Helper()
	for _, c := range rootCmd.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("command %q not found on the root", name)
	return nil
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestRun_NilArgsIsAnEmptyArgumentList: cobra reads SetArgs(nil) as "never set"
// and falls back to os.Args[1:], so a caller spelling "no arguments" the
// idiomatic way used to get the host process's own command line parsed as
// taskmgr's — here, the test binary's -test.* flags.
func TestRun_NilArgsIsAnEmptyArgumentList(t *testing.T) {
	var outBuf, errBuf bytes.Buffer
	code := Run(nil, &outBuf, &errBuf)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errBuf.String())
	}
	if !strings.Contains(outBuf.String(), "taskmgr is a lean") {
		t.Errorf("stdout does not carry the root help:\n%s", outBuf.String())
	}
	if strings.Contains(errBuf.String(), "test.") {
		t.Errorf("the test binary's own flags reached the parser: %q", errBuf.String())
	}
}

// TestRun_LogRecordsGoToTheGivenStderrWriter: Run promises to write everything
// the command emits to the writers it was given. The SDK logs a hook error at
// warn, which is above the default threshold, so it fires with TASKMGR_LOG
// unset — and a handler bound to os.Stderr put it on the host's real stderr,
// where no in-process test could see it.
func TestRun_LogRecordsGoToTheGivenStderrWriter(t *testing.T) {
	root := newStore(t)
	if _, errOut, code := run(t, "--dir", root, "config", "hook", "add",
		"--event", "pre-close", "--run", "/nonexistent/taskmgr-hook-probe"); code != 0 {
		t.Fatalf("hook add: exit %d, stderr %q", code, errOut)
	}
	out, _, code := run(t, "--dir", root, "--json", "create", "--title", "gated")
	if code != 0 {
		t.Fatalf("create: exit %d", code)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse create JSON: %v (%q)", err, out)
	}

	_, errOut, code := run(t, "--dir", root, "close", created.ID, "--reason", "done")
	if code == 0 {
		t.Fatal("a hook that cannot be executed must fail the close")
	}
	if !strings.Contains(errOut, "level=WARN") || !strings.Contains(errOut, "msg=hook") {
		t.Errorf("the hook-error record did not reach Run's stderr writer:\n%s", errOut)
	}
}

// TestInit_RelativeDirIsResolvedBeforeThePrefixIsDerived: the prefix is baked
// into every issue ID and is immutable once written, so deriving it from "."
// (which yields the "task" fallback) can only be undone by recreating the store.
func TestInit_RelativeDirIsResolvedBeforeThePrefixIsDerived(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "payments-api")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Chdir(dir)

	out, errOut, code := run(t, "-C", ".", "init")
	if code != 0 {
		t.Fatalf("init: exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, `prefix "payments"`) {
		t.Errorf("init derived the wrong prefix from a relative --dir:\n%s", out)
	}
}
