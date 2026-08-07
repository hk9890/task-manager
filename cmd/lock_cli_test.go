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

// L4 test for the single-writer invariant across processes.
//
// ARCHITECTURE-SPEC §7 states that concurrent writers serialize on a store-wide
// advisory flock, and that this is "the precondition for validation and
// atomicity". Every other test of that invariant runs goroutines inside one
// process — but one process per invocation is the only shape the CLI actually
// produces, and flock is per-process, so the in-process tests cannot prove it.
// This file is the one that contends the lock from separate OS processes.
package cmd_test

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// TestL4_ConcurrentProcesses_NoLostUpdate has several taskmgr processes add
// labels to one issue at the same time. Every label must survive.
//
// `update --add-label` is a read-modify-write of the whole issue file: the
// process reads the issue, appends to its label set, and writes the file back.
// Two processes that overlap without the flock both read the same starting
// state and the second write erases the first one's label — the classic lost
// update. Serializing on the store lock is what makes the operation safe, and
// this is the only test that puts two real processes on either side of it.
func TestL4_ConcurrentProcesses_NoLostUpdate(t *testing.T) {
	root := initStore(t, "cxp")
	bin := taskmgrBin(t)
	issueID := mkIssue(t, root, "contended issue")

	const (
		writers         = 4
		labelsPerWriter = 12
		wantLabels      = writers * labelsPerWriter
	)

	// Each writer runs its updates back to back, so the processes keep
	// overlapping for the whole test instead of colliding once at startup: a
	// single round of concurrent spawns is dominated by process startup and
	// would pass even with no lock at all.
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, writers)

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < labelsPerWriter; j++ {
				label := fmt.Sprintf("w%d:l%d", i, j)
				cmd := exec.Command(bin, "--dir", root, "update", issueID, "--add-label", label)
				var out strings.Builder
				cmd.Stdout = &out
				cmd.Stderr = &out
				if err := cmd.Run(); err != nil {
					results <- fmt.Errorf("writer %d label %s: %w\noutput: %s", i, label, err, out.String())
					return
				}
			}
		}(i)
	}

	close(start)
	wg.Wait()
	close(results)

	for err := range results {
		if err != nil {
			t.Errorf("concurrent update: %v", err)
		}
	}

	out, stderr, code := taskmgr(t, root, "--json", "show", issueID)
	if code != 0 {
		t.Fatalf("show failed (exit %d): %s", code, stderr)
	}
	var got struct {
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse show JSON: %v\noutput: %s", err, out)
	}

	seen := make(map[string]bool, len(got.Labels))
	for _, l := range got.Labels {
		seen[l] = true
	}
	if len(seen) != wantLabels {
		t.Errorf("issue carries %d labels, want %d — a concurrent write was lost", len(seen), wantLabels)
	}
}
