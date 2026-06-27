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

// search_test.go — L1 (pure) tests for SearchExpr.
//
// SDK-SPEC §3 (Criteria/SearchExpr). QUERY-SPEC §4 (the `text` field).
package tasks_test

import (
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

func TestSearchExpr(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"single word", "drill", `text ~ "drill"`},
		{"two words AND-ed", "drill nav", `text ~ "drill" && text ~ "nav"`},
		{"order independent", "nav drill", `text ~ "nav" && text ~ "drill"`},
		{"collapses whitespace", "  drill\t nav  ", `text ~ "drill" && text ~ "nav"`},
		{"empty", "", ""},
		{"whitespace only", "   \t", ""},
		{"quotes are escaped", `a"b c`, `text ~ "a\"b" && text ~ "c"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tasks.SearchExpr(tc.query)
			if got != tc.want {
				t.Errorf("SearchExpr(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}

// TestSearchExpr_MatchesCriteriaAllWords pins SearchExpr as exact sugar over
// Criteria{TextMatch: TextAllWords}.Build — the single shared definition.
func TestSearchExpr_MatchesCriteriaAllWords(t *testing.T) {
	const q = "drill nav issue"
	want, err := tasks.Criteria{Text: q, TextMatch: tasks.TextAllWords}.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if got := tasks.SearchExpr(q); got != want {
		t.Errorf("SearchExpr(%q) = %q, want Criteria build %q", q, got, want)
	}
}
