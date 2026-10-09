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
	"strings"
	"testing"
)

// An edge flag takes an issue ID. "../<open-id>" used to resolve to the open
// issue's own file, so the value was stored and the new issue was listed as
// ready while its blocker was open.
func TestCreate_EdgeOutsideTheIDGrammar_IsRefused(t *testing.T) {
	cases := map[string]string{
		"--parent":     "parent",
		"--blocked-by": "blocked_by",
		"--related":    "related",
	}
	for flag, field := range cases {
		t.Run(flag, func(t *testing.T) {
			root := newStore(t)
			open := createIssue(t, root)

			out, errOut, code := run(t, "--dir", root, "create", "--title", "x", flag, "../"+open)
			if code != 1 || out != "" {
				t.Errorf("exit %d, stdout %q; want 1 and empty", code, out)
			}
			if !strings.Contains(errOut, field) || !strings.Contains(errOut, "not a valid issue ID") {
				t.Errorf("stderr %q does not name %s as an invalid issue ID", errOut, field)
			}
			if n := openIssueCount(t, root); n != 1 {
				t.Errorf("the store holds %d issues, want the one it had", n)
			}
		})
	}
}

func TestCreate_BlockedByAClosedIssue_IsAccepted(t *testing.T) {
	root := newStore(t)
	closed := createIssue(t, root)
	if _, errOut, code := run(t, "--dir", root, "close", closed); code != 0 {
		t.Fatalf("close: exit %d, stderr %q", code, errOut)
	}

	created := createIssue(t, root, "--blocked-by", closed)
	if got := showIssue(t, root, created).BlockedBy; len(got) != 1 || got[0] != closed {
		t.Errorf("blocked_by = %v, want [%s]", got, closed)
	}
}
