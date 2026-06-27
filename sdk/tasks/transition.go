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

// transition.go — pure transition classification and the no-op predicate for
// the write path (HOOK-SPEC §2.1). No filesystem here: these functions take
// in-memory issues and return values, so they unit-test at L1.
package tasks

import "slices"

// transition is the single lifecycle transition a mutation performs. Each maps
// to a pre-event and a post-event (HOOK-SPEC §2).
type transition string

const (
	transCreate transition = "create"
	transUpdate transition = "update"
	transClose  transition = "close"
	transReopen transition = "reopen"
)

func (t transition) preEvent() string  { return "pre-" + string(t) }
func (t transition) postEvent() string { return "post-" + string(t) }

// classify picks the single transition for a mutation by comparing the proposed
// new issue to the prior old issue (HOOK-SPEC §2.1). old is nil for a create.
// The priority is fixed: create (no old) → close (becomes closed) → reopen
// (leaves closed) → otherwise update.
func classify(old, newIss *Issue) transition {
	if old == nil {
		return transCreate
	}
	switch {
	case !old.Status.IsClosed() && newIss.Status.IsClosed():
		return transClose
	case old.Status.IsClosed() && !newIss.Status.IsClosed():
		return transReopen
	default:
		return transUpdate
	}
}

// cloneIssue returns a deep copy of iss (slice fields duplicated) so a caller
// can keep an immutable snapshot of the pre-mutation state while the original
// is mutated in place.
func cloneIssue(iss *Issue) *Issue {
	if iss == nil {
		return nil
	}
	c := *iss
	c.Labels = slices.Clone(iss.Labels)
	c.BlockedBy = slices.Clone(iss.BlockedBy)
	c.Related = slices.Clone(iss.Related)
	return &c
}

// issuesEqualIgnoringUpdated reports whether two issues are identical in every
// persisted field except Updated. It is the no-op predicate: a mutation whose
// materialized new equals old by this measure changes nothing on disk, so the
// engine writes nothing and fires no hooks (HOOK-SPEC §2.1).
//
// Updated is excluded because the engine stamps it to now before this check;
// including it would defeat no-op detection. Slice fields compare order-
// sensitively — a reordering is a real on-disk change.
func issuesEqualIgnoringUpdated(a, b *Issue) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID &&
		a.Title == b.Title &&
		a.Status == b.Status &&
		a.Type == b.Type &&
		a.Priority == b.Priority &&
		a.Assignee == b.Assignee &&
		a.Creator == b.Creator &&
		a.Parent == b.Parent &&
		a.Created.Equal(b.Created) &&
		a.Closed.Equal(b.Closed) &&
		a.CloseReason == b.CloseReason &&
		a.Description == b.Description &&
		slices.Equal(a.Labels, b.Labels) &&
		slices.Equal(a.BlockedBy, b.BlockedBy) &&
		slices.Equal(a.Related, b.Related)
}
