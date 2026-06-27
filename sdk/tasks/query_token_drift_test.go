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

package tasks_test

import (
	"testing"

	tasks "github.com/hk9890/task-manager/sdk/tasks"
	"github.com/hk9890/task-manager/sdk/tasks/internal/query"
)

// TestQueryTokensMatchModel is the anti-drift guard for issue #22.
//
// Criteria.Build validates enum values against the model (Status.Valid /
// Type.Valid) and then emits a filter expression that the query parser must
// accept. The parser keeps its own mirror of the accepted tokens because it is
// a leaf package that cannot import tasks (import-cycle rule). The two layers
// must therefore agree by construction; this test fails CI the moment they
// drift — which is exactly what happened when the model gained "deferred" but
// the parser did not (issue #21).
//
// Iterating tasks.Statuses / tasks.Types means a newly added enum value is
// covered automatically, with no second list to remember to update here.
func TestQueryTokensMatchModel(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		for _, s := range tasks.Statuses {
			expr, err := tasks.Criteria{Statuses: []tasks.Status{s}}.Build()
			if err != nil {
				t.Errorf("Criteria.Build rejected status %q: %v", s, err)
				continue
			}
			if _, err := query.Parse(expr); err != nil {
				t.Errorf("status %q: Build emitted %q but the parser rejected it: %v", s, expr, err)
			}
		}
	})

	t.Run("type", func(t *testing.T) {
		for _, tp := range tasks.Types {
			expr, err := tasks.Criteria{Types: []tasks.Type{tp}}.Build()
			if err != nil {
				t.Errorf("Criteria.Build rejected type %q: %v", tp, err)
				continue
			}
			if _, err := query.Parse(expr); err != nil {
				t.Errorf("type %q: Build emitted %q but the parser rejected it: %v", tp, expr, err)
			}
		}
	})

	// The OR-group emit path (multiple values in one Criteria) must round-trip too.
	t.Run("all_statuses_or_group", func(t *testing.T) {
		expr, err := tasks.Criteria{Statuses: tasks.Statuses}.Build()
		if err != nil {
			t.Fatalf("Criteria.Build rejected the full status set: %v", err)
		}
		if _, err := query.Parse(expr); err != nil {
			t.Fatalf("full status set: Build emitted %q but the parser rejected it: %v", expr, err)
		}
	})

	t.Run("all_types_or_group", func(t *testing.T) {
		expr, err := tasks.Criteria{Types: tasks.Types}.Build()
		if err != nil {
			t.Fatalf("Criteria.Build rejected the full type set: %v", err)
		}
		if _, err := query.Parse(expr); err != nil {
			t.Fatalf("full type set: Build emitted %q but the parser rejected it: %v", expr, err)
		}
	})
}
