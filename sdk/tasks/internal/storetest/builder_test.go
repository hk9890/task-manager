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

package storetest_test

import (
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
	"github.com/hk9890/task-manager/sdk/tasks/internal/storetest"
)

// TestBuilder_Comment verifies that Comment() adds a comment to an issue.
// Comments are now stored in the sidecar, not on the Issue struct.
func TestBuilder_Comment(t *testing.T) {
	b := storetest.New(t).
		Issue("tst-0001").
		Comment("tst-0001", "hans", "first note")

	store := b.Mem()
	// Comments live in the sidecar; use store.Comments() to retrieve them.
	comments, err := store.Comments("tst-0001")
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if comments[0].Body != "first note" {
		t.Errorf("comment body = %q, want %q", comments[0].Body, "first note")
	}
	if comments[0].Author != "hans" {
		t.Errorf("comment author = %q, want %q", comments[0].Author, "hans")
	}
}

// TestBuilder_Parent verifies that Parent() sets the parent relationship.
func TestBuilder_Parent(t *testing.T) {
	b := storetest.New(t).
		Issue("tst-0001").
		Issue("tst-0002", storetest.Parent("tst-0001"))

	store := b.Mem()
	child, err := store.Get("tst-0002")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if child.Parent != "tst-0001" {
		t.Errorf("parent = %q, want tst-0001", child.Parent)
	}
}

// TestBuilder_Labels verifies that Label() opts are applied.
func TestBuilder_Labels(t *testing.T) {
	b := storetest.New(t).
		Issue("tst-0001", storetest.Label("urgent", "backend"))

	store := b.Mem()
	iss, err := store.Get("tst-0001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(iss.Labels) != 2 {
		t.Fatalf("expected 2 labels, got %v", iss.Labels)
	}
}

// TestBuilder_TypeOpt verifies that IssueType() sets the issue type.
func TestBuilder_TypeOpt(t *testing.T) {
	b := storetest.New(t).
		Issue("tst-0001", storetest.IssueType(tasks.TypeBug))

	store := b.Mem()
	iss, err := store.Get("tst-0001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if iss.Type != tasks.TypeBug {
		t.Errorf("type = %q, want bug", iss.Type)
	}
}

// TestBuilder_Closed verifies that Closed() creates a closed issue.
func TestBuilder_Closed(t *testing.T) {
	b := storetest.New(t).
		Closed("tst-0001")

	store := b.Mem()
	iss, err := store.Get("tst-0001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if iss.Status != tasks.StatusClosed {
		t.Errorf("status = %q, want closed", iss.Status)
	}
}
