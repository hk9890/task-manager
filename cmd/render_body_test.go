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

// In-process tests for the `show` body-truncation boundary.
//
// The L4 tests in overflow_cli_test.go use a clearly-small body and a clearly-huge
// one, which leaves the comparison at showBodyLimit free to drift either way: one
// step tighter and a body of exactly the limit gains a notice telling the reader
// that complete output is incomplete; one step looser and an over-limit body
// prints in full. Neither is visible from a test that never lands on the value.
package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// showBody creates an issue with the given body and returns what `show` printed.
func showBody(t *testing.T, root, body string) string {
	t.Helper()
	out, stderr, code := run(t, "--dir", root, "--json", "create", "--title", "boundary", "--description", body)
	if code != 0 {
		t.Fatalf("create: exit %d, stderr %s", code, stderr)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse create JSON: %v", err)
	}

	human, stderr, code := run(t, "--dir", root, "show", created.ID)
	if code != 0 {
		t.Fatalf("show: exit %d, stderr %s", code, stderr)
	}
	return human
}

// TestShow_BodyExactlyAtTheLimitIsNotTruncated pins the boundary from below.
func TestShow_BodyExactlyAtTheLimitIsNotTruncated(t *testing.T) {
	root := newStore(t)
	// No leading or trailing space, so nothing trims the length out from under
	// the assertion.
	body := strings.Repeat("a", showBodyLimit)

	human := showBody(t, root, body)
	if strings.Contains(human, "body is") {
		t.Errorf("a body of exactly %d bytes got a truncation notice:\n%s", showBodyLimit, human)
	}
	if !strings.Contains(human, body) {
		t.Errorf("a body of exactly %d bytes was not printed in full", showBodyLimit)
	}
}

// TestShow_BodyOneByteOverTheLimitIsTruncated pins it from above.
func TestShow_BodyOneByteOverTheLimitIsTruncated(t *testing.T) {
	root := newStore(t)
	body := strings.Repeat("a", showBodyLimit+1)

	human := showBody(t, root, body)
	if !strings.Contains(human, "body is") {
		t.Errorf("a body of %d bytes got no truncation notice:\n%s", showBodyLimit+1, human)
	}
	if strings.Contains(human, body) {
		t.Errorf("a body of %d bytes was printed in full despite the notice", showBodyLimit+1)
	}
}
