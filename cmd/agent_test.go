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

// agent_test.go — coding-agent detection (CLI-SPEC §1.1): the marker table as a
// pure unit, then what a command actually records, in-process through Run.
//
// Every CLI test here sets the markers it depends on, including the ones it
// expects to be absent: the suite itself is often run from inside a coding
// agent, so an unset expectation that reads the ambient environment would pass
// on CI and fail on a developer's machine.

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── the marker table ─────────────────────────────────────────────────────────

func TestDetectAgent_Markers(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantAgent   string
		wantSession string
	}{
		{"no marker", map[string]string{}, "", ""},
		{
			"claude code carries a session",
			map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "s-1"},
			"claude-code", "s-1",
		},
		{
			"claude code without a session id",
			map[string]string{"CLAUDECODE": "1"},
			"claude-code", "",
		},
		{
			"a harness that publishes no session",
			map[string]string{"GEMINI_CLI": "1", "CLAUDE_CODE_SESSION_ID": "s-1"},
			"gemini-cli", "",
		},
		{"cursor", map[string]string{"CURSOR_AGENT": "1"}, "cursor", ""},
		{"opencode", map[string]string{"OPENCODE": "true"}, "opencode", ""},
		{"kiro", map[string]string{"AGENT_CONTEXT_OUT": "/tmp/ctx"}, "kiro", ""},
		{
			"a marker set to a falsy value reads as unset",
			map[string]string{"CLAUDECODE": "0", "GEMINI_CLI": "false", "COPILOT_CLI": ""},
			"", "",
		},
		{
			"first match wins when harnesses nest",
			map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "s-1", "OPENCODE": "1"},
			"claude-code", "s-1",
		},
		{
			"TASKMGR_NO_AGENT suppresses detection",
			map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "s-1", "TASKMGR_NO_AGENT": "1"},
			"", "",
		},
		{
			"a falsy TASKMGR_NO_AGENT does not suppress",
			map[string]string{"CLAUDECODE": "1", "TASKMGR_NO_AGENT": "0"},
			"claude-code", "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agent, session := detectAgent(func(k string) string { return tc.env[k] })
			if agent != tc.wantAgent || session != tc.wantSession {
				t.Errorf("detectAgent = (%q, %q), want (%q, %q)", agent, session, tc.wantAgent, tc.wantSession)
			}
		})
	}
}

// Every marker in the table must be reachable: a typo in a slug is invisible
// until someone runs that harness, which is never in this repository.
func TestDetectAgent_EveryMarkerResolves(t *testing.T) {
	for _, a := range codingAgents {
		agent, _ := detectAgent(func(k string) string {
			if k == a.marker {
				return "1"
			}
			return ""
		})
		if agent != a.slug {
			t.Errorf("marker %s detected as %q, want %q", a.marker, agent, a.slug)
		}
	}
}

// ── what a command records ───────────────────────────────────────────────────

// showJSON runs `show --json` and returns the decoded detail object.
func showJSON(t *testing.T, root, id string) map[string]any {
	t.Helper()
	out, errOut, code := run(t, "--dir", root, "--json", "show", id)
	if code != 0 {
		t.Fatalf("show %s: exit %d, stderr %q", id, code, errOut)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse show output: %v\n%s", err, out)
	}
	return dto
}

// createIssue runs `create` and returns the new ID.
func createIssue(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, errOut, code := run(t, append([]string{"--dir", root, "--json", "create", "--title", "provenance"}, args...)...)
	if code != 0 {
		t.Fatalf("create: exit %d, stderr %q", code, errOut)
	}
	var dto struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("parse create output: %v\n%s", err, out)
	}
	return dto.ID
}

// setMarkers clears every marker in the table, then applies the given ones, so
// a test's environment is exactly what it declares.
func setMarkers(t *testing.T, env map[string]string) {
	t.Helper()
	t.Setenv("TASKMGR_NO_AGENT", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	for _, a := range codingAgents {
		t.Setenv(a.marker, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func TestCreate_RecordsTheDetectedAgent(t *testing.T) {
	root := newStore(t)
	setMarkers(t, map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "s-1"})

	dto := showJSON(t, root, createIssue(t, root))
	if dto["agent"] != "claude-code" {
		t.Errorf("agent = %v, want claude-code", dto["agent"])
	}
	if dto["session"] != "s-1" {
		t.Errorf("session = %v, want s-1", dto["session"])
	}
}

func TestCreate_NoAgent_OmitsBothFields(t *testing.T) {
	root := newStore(t)
	setMarkers(t, nil)

	dto := showJSON(t, root, createIssue(t, root))
	if _, ok := dto["agent"]; ok {
		t.Errorf("agent must be omitted when no harness is detected, got %v", dto["agent"])
	}
	if _, ok := dto["session"]; ok {
		t.Errorf("session must be omitted when no harness is detected, got %v", dto["session"])
	}
}

func TestCreate_NoAgentEnvSuppressesDetection(t *testing.T) {
	root := newStore(t)
	setMarkers(t, map[string]string{
		"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "s-1", "TASKMGR_NO_AGENT": "1",
	})

	dto := showJSON(t, root, createIssue(t, root))
	if _, ok := dto["agent"]; ok {
		t.Errorf("TASKMGR_NO_AGENT must suppress detection, got agent = %v", dto["agent"])
	}
}

// An explicit --agent takes the detected session with it: that id belongs to the
// harness that published it, not to whatever name the flag names.
func TestCreate_AgentFlagOverridesAndClearsDetectedSession(t *testing.T) {
	root := newStore(t)
	setMarkers(t, map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "s-1"})

	dto := showJSON(t, root, createIssue(t, root, "--agent", "custom-harness"))
	if dto["agent"] != "custom-harness" {
		t.Errorf("agent = %v, want custom-harness", dto["agent"])
	}
	if _, ok := dto["session"]; ok {
		t.Errorf("an explicit --agent must clear the detected session, got %v", dto["session"])
	}
}

func TestCreate_SessionFlagOverridesDetection(t *testing.T) {
	root := newStore(t)
	setMarkers(t, map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "s-1"})

	dto := showJSON(t, root, createIssue(t, root, "--agent", "custom-harness", "--session", "s-2"))
	if dto["agent"] != "custom-harness" || dto["session"] != "s-2" {
		t.Errorf("provenance = (%v, %v), want (custom-harness, s-2)", dto["agent"], dto["session"])
	}
}

// A session with no agent beside it is half a fact, and the half no reader can
// act on. It is refused rather than stored.
func TestCreate_SessionWithoutAgentIsRefused(t *testing.T) {
	root := newStore(t)
	setMarkers(t, nil)

	out, errOut, code := run(t, "--dir", root, "create", "--title", "orphan session", "--session", "s-9")
	if code == 0 {
		t.Fatalf("expected a non-zero exit, got 0; stdout %q", out)
	}
	if !strings.Contains(errOut, "--session") {
		t.Errorf("stderr does not name the offending flag: %q", errOut)
	}
	if out != "" {
		t.Errorf("stdout must stay empty on an error, got %q", out)
	}
}

func TestCommentAdd_SessionWithoutAgentIsRefused(t *testing.T) {
	root := newStore(t)
	setMarkers(t, map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "s-1"})
	id := createIssue(t, root)
	setMarkers(t, nil)

	if _, errOut, code := run(t, "--dir", root, "comment", "add", id, "note", "--session", "s-9"); code == 0 {
		t.Fatalf("expected a non-zero exit, got 0; stderr %q", errOut)
	}
}

// The store trims an issue's provenance; the comment path writes what it is
// handed. Trimming in the CLI is what keeps the two commands agreeing.
func TestProvenance_BlankFlagsAreTrimmedAway(t *testing.T) {
	root := newStore(t)
	setMarkers(t, map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "s-1"})
	id := createIssue(t, root, "--agent", "   ")

	dto := showJSON(t, root, id)
	if _, ok := dto["agent"]; ok {
		t.Errorf("a whitespace-only --agent must store nothing, got %v", dto["agent"])
	}

	if _, errOut, code := run(t, "--dir", root, "comment", "add", id, "note", "--agent", "   "); code != 0 {
		t.Fatalf("comment add: exit %d, stderr %q", code, errOut)
	}
	comments := showJSON(t, root, id)["comments"].([]any)
	c := comments[0].(map[string]any)
	if _, ok := c["agent"]; ok {
		t.Errorf("a whitespace-only --agent must store nothing on a comment, got %v", c["agent"])
	}
}

func TestCommentAdd_RecordsTheDetectedAgent(t *testing.T) {
	root := newStore(t)
	setMarkers(t, map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "s-1"})
	id := createIssue(t, root)

	if _, errOut, code := run(t, "--dir", root, "comment", "add", id, "a note"); code != 0 {
		t.Fatalf("comment add: exit %d, stderr %q", code, errOut)
	}

	dto := showJSON(t, root, id)
	comments, ok := dto["comments"].([]any)
	if !ok || len(comments) != 1 {
		t.Fatalf("comments = %v, want exactly one", dto["comments"])
	}
	c := comments[0].(map[string]any)
	if c["agent"] != "claude-code" || c["session"] != "s-1" {
		t.Errorf("comment provenance = (%v, %v), want (claude-code, s-1)", c["agent"], c["session"])
	}
}

// The human view says which agent wrote what; the session id stays in --json,
// where a caller that wants it is already parsing.
func TestShow_HumanOutputNamesTheAgent(t *testing.T) {
	root := newStore(t)
	setMarkers(t, map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "s-1"})
	id := createIssue(t, root)
	if _, errOut, code := run(t, "--dir", root, "comment", "add", id, "a note"); code != 0 {
		t.Fatalf("comment add: exit %d, stderr %q", code, errOut)
	}

	out, errOut, code := run(t, "--dir", root, "show", id)
	if code != 0 {
		t.Fatalf("show: exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "agent:") || !strings.Contains(out, "claude-code") {
		t.Errorf("show output does not name the agent:\n%s", out)
	}
	if !strings.Contains(out, "via claude-code") {
		t.Errorf("comment line does not name the agent that wrote it:\n%s", out)
	}
	if !strings.Contains(out, "s-1") {
		t.Errorf("the issue's session is not shown:\n%s", out)
	}
}

func TestList_FiltersOnTheDetectedAgent(t *testing.T) {
	root := newStore(t)
	setMarkers(t, map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "s-1"})
	agentFiled := createIssue(t, root)
	setMarkers(t, nil)
	createIssue(t, root)

	out, errOut, code := run(t, "--dir", root, "--json", "list", "-q", `agent == "claude-code"`)
	if code != 0 {
		t.Fatalf("list: exit %d, stderr %q", code, errOut)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parse list: %v\n%s", err, out)
	}
	if len(rows) != 1 || rows[0]["id"] != agentFiled {
		t.Errorf("list returned %d rows, want just %s", len(rows), agentFiled)
	}
}
