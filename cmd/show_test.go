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

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestShow_SeveralIDs_PrintsBlocksInArgumentOrder(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root)
	b := createIssue(t, root)

	out, errOut, code := run(t, "--dir", root, "show", b, a)
	if code != 0 {
		t.Fatalf("show: exit %d, stderr %q", code, errOut)
	}
	first, second := strings.Index(out, b+"  "), strings.Index(out, "\n\n"+a+"  ")
	if first != 0 || second < 0 {
		t.Errorf("want the block of %s, a blank line, then the block of %s; got:\n%s", b, a, out)
	}
}

func TestShow_OneID_JSONIsAnObject(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root)

	out, errOut, code := run(t, "--dir", root, "--json", "show", a)
	if code != 0 {
		t.Fatalf("show: exit %d, stderr %q", code, errOut)
	}
	var dto struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("one ID must stay a JSON object: %v\n%s", err, out)
	}
	if dto.ID != a {
		t.Errorf("id = %q, want %q", dto.ID, a)
	}
}

func TestShow_SeveralIDs_JSONIsAnArrayInArgumentOrder(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root)
	b := createIssue(t, root)

	out, errOut, code := run(t, "--dir", root, "--json", "show", b, a)
	if code != 0 {
		t.Fatalf("show: exit %d, stderr %q", code, errOut)
	}
	var dtos []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &dtos); err != nil {
		t.Fatalf("several IDs must print a JSON array: %v\n%s", err, out)
	}
	if len(dtos) != 2 || dtos[0].ID != b || dtos[1].ID != a {
		t.Errorf("got %+v, want [%s %s]", dtos, b, a)
	}
}

func TestShow_SeveralIDs_OneMissing_PrintsNothing(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root)

	for _, jsonFlag := range []bool{false, true} {
		args := []string{"--dir", root, "show", a, "tst-9999"}
		if jsonFlag {
			args = append([]string{"--json"}, args...)
		}
		out, errOut, code := run(t, args...)
		if code != 1 {
			t.Errorf("json=%v: exit %d, want 1", jsonFlag, code)
		}
		if out != "" {
			t.Errorf("json=%v: a missing ID must leave stdout empty; got %q", jsonFlag, out)
		}
		if !strings.Contains(errOut, "issue not found: tst-9999") {
			t.Errorf("json=%v: stderr %q does not name the missing ID", jsonFlag, errOut)
		}
	}
}
