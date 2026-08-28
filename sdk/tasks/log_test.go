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
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// records parses the captured JSON log lines into maps.
func records(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(buf)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("log line not JSON: %s (%v)", line, err)
		}
		out = append(out, m)
	}
	return out
}

func find(recs []map[string]any, msg string) map[string]any {
	for _, r := range recs {
		if r["msg"] == msg {
			return r
		}
	}
	return nil
}

func TestLogHook_RecordsDecisionAndDuration(t *testing.T) {
	var buf bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	fake := &exec.Fake{Func: func(exec.Spec) exec.Result {
		return exec.Result{Category: exec.Completed, ExitCode: 0, Stdout: []byte("hi"), Duration: 7 * time.Millisecond}
	}}
	s, err := InitWithVFS("/", "x", vfs.NewMem(), WithLogger(lg))
	if err != nil {
		t.Fatal(err)
	}
	s.runner = fake
	storePackage(t, s, "p", []Hook{{ID: "g", Event: "pre-create", Run: []string{"g"}}})

	if _, err := s.Create(CreateInput{Title: "t"}); err != nil {
		t.Fatal(err)
	}

	recs := records(t, &buf)
	hook := find(recs, "hook")
	if hook == nil {
		t.Fatal("expected a hook log record")
	}
	if hook["event"] != "pre-create" || hook["hook"] != "pkg:p:g" || hook["decision"] != "allow" {
		t.Errorf("hook record = %v", hook)
	}
	if _, ok := hook["duration_ms"]; !ok {
		t.Error("hook record must carry duration_ms")
	}
	// A committed write is also logged.
	if find(recs, "write") == nil {
		t.Error("expected a write log record")
	}
}

func TestLogHook_DenyLogsAtInfo(t *testing.T) {
	var buf bytes.Buffer
	// Level info: an allow (debug) is filtered out, a deny (info) is kept.
	lg := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	fake := &exec.Fake{Func: func(exec.Spec) exec.Result { return exec.Deny(1, "nope") }}
	s, err := InitWithVFS("/", "x", vfs.NewMem(), WithLogger(lg))
	if err != nil {
		t.Fatal(err)
	}
	s.runner = fake
	storePackage(t, s, "p", []Hook{{ID: "gate", Event: "pre-create", Run: []string{"g"}}})

	if _, err := s.Create(CreateInput{Title: "t"}); err == nil {
		t.Fatal("expected denial")
	}
	hook := find(records(t, &buf), "hook")
	if hook == nil || hook["decision"] != "deny" {
		t.Fatalf("expected a deny hook record at info level, got %v", hook)
	}
}

// A post-hook cannot deny: it runs after the write committed (HOOK-SPEC §7), so
// a non-zero exit is logged as `warn`. Logging it as `deny` would make the
// deny-rate query in MONITORING.md count writes that were never blocked.
func TestLogHook_PostHookFailureLogsWarnNotDeny(t *testing.T) {
	var buf bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	fake := &exec.Fake{Func: func(exec.Spec) exec.Result { return exec.Deny(1, "notify failed") }}
	s, err := InitWithVFS("/", "x", vfs.NewMem(), WithLogger(lg))
	if err != nil {
		t.Fatal(err)
	}
	s.runner = fake
	storePackage(t, s, "p", []Hook{{ID: "notify", Event: "post-create", Run: []string{"n"}}})

	res, err := s.Create(CreateInput{Title: "t"})
	if err != nil {
		t.Fatalf("a failing post-hook must not fail the write: %v", err)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("warnings = %v, want one", res.Warnings)
	}

	hook := find(records(t, &buf), "hook")
	if hook == nil {
		t.Fatal("expected a hook record")
	}
	if hook["decision"] != "warn" {
		t.Errorf("decision = %v, want warn (a post-hook denies nothing)", hook["decision"])
	}
	if hook["event"] != "post-create" {
		t.Errorf("event = %v, want post-create", hook["event"])
	}
}

// A pre-hook error and a post-hook error both stay `error`: the hook itself
// misbehaved, which is the same fact in either phase (HOOK-SPEC §7).
func TestLogHook_ErrorDecisionIsPhaseIndependent(t *testing.T) {
	for _, event := range []string{"pre-create", "post-create"} {
		t.Run(event, func(t *testing.T) {
			var buf bytes.Buffer
			lg := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

			fake := &exec.Fake{Func: func(exec.Spec) exec.Result {
				return exec.Result{Category: exec.Timeout}
			}}
			s, err := InitWithVFS("/", "x", vfs.NewMem(), WithLogger(lg))
			if err != nil {
				t.Fatal(err)
			}
			s.runner = fake
			storePackage(t, s, "p", []Hook{{ID: "h", Event: event, Run: []string{"h"}}})

			_, _ = s.Create(CreateInput{Title: "t"})

			hook := find(records(t, &buf), "hook")
			if hook == nil || hook["decision"] != "error" {
				t.Fatalf("decision = %v, want error", hook)
			}
		})
	}
}

// The writes that are not lifecycle transitions emit no `write` record, but a
// failure must still surface as io_error — otherwise a failed comment or dep
// write is invisible at every log level (MONITORING.md).
func TestLogIOError_NonTransitionWrites(t *testing.T) {
	// The faulted vfs call differs by write shape: comments append to the
	// sidecar, dep/rel edits rewrite the issue file.
	sidecar := func(s *Store, target *Issue) string { return s.commentsPath(target.ID) }
	issueFile := func(s *Store, target *Issue) string {
		p, err := s.issueFilePath(target.ID)
		if err != nil {
			panic(err)
		}
		return p
	}

	cases := []struct {
		name   string
		op     string
		vfsOp  string
		path   func(s *Store, target *Issue) string
		mutate func(s *Store, target, other *Issue) error
	}{
		{"comment add", "comment_add", "Append", sidecar, func(s *Store, target, _ *Issue) error {
			_, err := s.AddComment(target.ID, "a", "body")
			return err
		}},
		{"comment edit", "comment_edit", "Append", sidecar, nil}, // set below; needs an existing comment
		{"dep add", "dep_add", "WriteAtomic", issueFile, func(s *Store, target, other *Issue) error {
			return s.AddDep(target.ID, other.ID)
		}},
		{"dep remove", "dep_remove", "WriteAtomic", issueFile, func(s *Store, target, other *Issue) error {
			return s.RemoveDep(target.ID, other.ID)
		}},
		{"rel add", "rel_add", "WriteAtomic", issueFile, func(s *Store, target, other *Issue) error {
			return s.AddRelated(target.ID, other.ID)
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			lg := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			m := vfs.NewMem()
			s, err := InitWithVFS("/", "x", m)
			if err != nil {
				t.Fatal(err)
			}
			s.logger = lg

			target, err := unwrap(s.Create(CreateInput{Title: "target"}))
			if err != nil {
				t.Fatal(err)
			}
			other, err := unwrap(s.Create(CreateInput{Title: "other"}))
			if err != nil {
				t.Fatal(err)
			}
			// EditComment needs an existing comment to replace.
			existing, err := s.AddComment(target.ID, "a", "first")
			if err != nil {
				t.Fatal(err)
			}
			if c.op == "comment_edit" {
				c.mutate = func(s *Store, target, _ *Issue) error {
					_, err := s.EditComment(target.ID, existing.ID, "a", "revised")
					return err
				}
			}
			buf.Reset()

			m.FailOn(c.vfsOp, c.path(s, target), errors.New("simulated disk full"))
			if err := c.mutate(s, target, other); err == nil {
				t.Fatal("expected the injected fault to fail the mutation")
			}

			recs := records(t, &buf)
			rec := find(recs, "io_error")
			if rec == nil {
				t.Fatal("a failed write must emit an io_error record")
			}
			if rec["op"] != c.op {
				t.Errorf("op = %v, want %v", rec["op"], c.op)
			}
			if rec["issue"] != target.ID {
				t.Errorf("issue = %v, want %v", rec["issue"], target.ID)
			}
			if find(recs, "write") != nil {
				t.Error("a non-transition write must not emit a write record")
			}
		})
	}
}

func TestLogger_DefaultIsSilent(t *testing.T) {
	// No WithLogger: the discard logger must not panic and a successful run is
	// silent. (We can't capture discard output; this asserts the path is safe.)
	s, err := InitWithVFS("/", "x", vfs.NewMem())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(CreateInput{Title: "t"}); err != nil {
		t.Fatal(err)
	}
}
