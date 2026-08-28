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

//go:build integration

// harness_cli_test.go — the shared L4 harness: the binary builder, the process
// driver, and the store fixtures every cmd/*_cli_test.go file uses.
//
// These used to live in comment_cli_test.go, a file about the comment commands,
// which is why four of them ended up written twice under different names: nobody
// looking for a fixture found the existing one. Add new shared helpers here.
package cmd_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// ── driving the binary ───────────────────────────────────────────────────────

// taskmgr runs the taskmgr binary (built from this module) with the given
// arguments against storeDir. It returns stdout, stderr, and the exit code.
func taskmgr(t *testing.T, storeDir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	bin := taskmgrBin(t)
	cmd := exec.Command(bin, append([]string{"--dir", storeDir}, args...)...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// taskmgrBin returns the path to the taskmgr binary, building it once per test
// run. The binary is placed in os.TempDir() so it survives individual test
// teardowns.
var (
	_taskmgrBinPath string
	_taskmgrBinErr  error
	_taskmgrBinOnce sync.Once
)

func taskmgrBin(t *testing.T) string {
	t.Helper()
	_taskmgrBinOnce.Do(func() {
		bin := filepath.Join(os.TempDir(), "taskmgr-test-bin")
		out, err := exec.Command("go", "build", "-o", bin,
			"github.com/hk9890/task-manager/cmd/taskmgr").CombinedOutput()
		if err != nil {
			_taskmgrBinErr = fmt.Errorf("go build failed: %v\n%s", err, out)
			return
		}
		_taskmgrBinPath = bin
	})
	if _taskmgrBinErr != nil {
		t.Fatalf("failed to build taskmgr: %v", _taskmgrBinErr)
	}
	return _taskmgrBinPath
}

// ── store fixtures ───────────────────────────────────────────────────────────

// initStore creates a temp directory with an empty store using prefix, and
// returns the project root.
func initStore(t *testing.T, prefix string) string {
	t.Helper()
	root := t.TempDir()
	if _, err := tasks.Init(root, prefix); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return root
}

// newTestStoreDir creates a temp store holding a single open issue, and returns
// (root, issueID).
func newTestStoreDir(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	s := initStoreAt(t, root, "tst")
	iss, err := unwrap(s.Create(tasks.CreateInput{Title: "cli test issue"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return root, iss.ID
}

// newStoreWithOpenAndClosed creates a temp store holding one open and one closed
// issue, and returns (root, openID, closedID). The two carry area:hot and
// area:cold labels so label-scoped queries have something to select, and share
// the word "task" in their titles so a text search can match both.
func newStoreWithOpenAndClosed(t *testing.T, prefix string) (root, openID, closedID string) {
	t.Helper()
	root = t.TempDir()
	s := initStoreAt(t, root, prefix)
	open, err := unwrap(s.Create(tasks.CreateInput{Title: "active task", Labels: []string{"area:hot"}}))
	if err != nil {
		t.Fatalf("Create open: %v", err)
	}
	closed, err := unwrap(s.Create(tasks.CreateInput{Title: "done task", Labels: []string{"area:cold"}}))
	if err != nil {
		t.Fatalf("Create to-close: %v", err)
	}
	if _, err := s.Close(closed.ID, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return root, open.ID, closed.ID
}

// initStoreAt initialises a store at root with a deterministic clock. It is the
// shared body of the fixtures above; tests that only need a path call initStore.
func initStoreAt(t *testing.T, root, prefix string) *tasks.Store {
	t.Helper()
	s, err := tasks.Init(root, prefix, tasks.WithClock(newTestClock(defaultFixtureStart).Now))
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return s
}

// defaultFixtureStart is the instant every CLI fixture store starts its clock
// at, unless a test needs its own.
var defaultFixtureStart = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// testClock is the deterministic clock a fixture store is built with. Now
// advances one second per call so ordering is visible; Set pins it to an
// instant, which is how a test reaches a timestamp it cannot get by advancing —
// closing an issue "before" the fixture started, for instance.
//
// It replaces the former Store.SetNow. A clock is injected once, at
// construction (tasks.WithClock), so anything a test wants to change later has
// to be state the test still owns — this value. That is the point: SetNow wrote
// an unsynchronised field on a store documented as safe for concurrent use,
// and the mutex here is on the test's own object instead.
type testClock struct {
	mu     sync.Mutex
	at     time.Time
	frozen bool
}

func newTestClock(start time.Time) *testClock {
	return &testClock{at: start}
}

// Now returns the next instant: one second on from the last, or the pinned
// instant while the clock is frozen.
func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.frozen {
		c.at = c.at.Add(time.Second)
	}
	return c.at
}

// Set pins the clock to at. Every later read returns it until the next Set.
func (c *testClock) Set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = at
	c.frozen = true
}

// ── helpers ──────────────────────────────────────────────────────────────────

// mkIssue creates an issue through the CLI and returns its ID.
func mkIssue(t *testing.T, root, title string) string {
	t.Helper()
	out, errs, code := taskmgr(t, root, "create", "--title", title, "--json")
	if code != 0 {
		t.Fatalf("create exit=%d stderr=%q", code, errs)
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.ID == "" {
		t.Fatalf("bad create json %q: %v", out, err)
	}
	return res.ID
}

// writeTempFile writes content to a file named name in a fresh temp directory
// and returns its path. Used for the --description-file / --body-file flags.
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}
