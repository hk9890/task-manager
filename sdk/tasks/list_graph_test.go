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
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/query"
)

func TestGraph_MarksEveryHotIssueInWorkOrder(t *testing.T) {
	s, _ := batchStore(t)
	prio := func(p int) *int { return &p }
	blocker := mustCreate(t, s, CreateInput{Title: "blocker", Priority: prio(3)})
	blocked := mustCreate(t, s, CreateInput{Title: "blocked", Priority: prio(0), BlockedBy: []string{blocker.ID}})
	doc := mustCreate(t, s, CreateInput{Title: "notes", Type: TypeDoc, Priority: prio(1)})

	nodes, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	type mark struct {
		id             string
		ready, blocked bool
		blockedBy      []string
	}
	var got []mark
	for _, n := range nodes {
		m := mark{id: n.Issue.ID, ready: n.Ready, blocked: n.Blocked}
		for _, r := range n.BlockedBy {
			m.blockedBy = append(m.blockedBy, r.ID)
		}
		got = append(got, m)
	}
	want := []mark{
		{id: blocked.ID, blocked: true, blockedBy: []string{blocker.ID}},
		{id: doc.ID},
		{id: blocker.ID, ready: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Graph = %+v\nwant    %+v", got, want)
	}
}

func TestGraph_AgreesWithReadyAndBlocked(t *testing.T) {
	s, _ := batchStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	mustCreate(t, s, CreateInput{Title: "b", BlockedBy: []string{a.ID}})
	mustCreate(t, s, CreateInput{Title: "c"})

	nodes, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	var wantReady, wantBlocked []string
	for _, n := range nodes {
		if n.Ready {
			wantReady = append(wantReady, n.Issue.ID)
		}
		if n.Blocked {
			wantBlocked = append(wantBlocked, n.Issue.ID)
		}
	}
	ready, err := s.Ready()
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := s.Blocked()
	if err != nil {
		t.Fatal(err)
	}
	var gotReady, gotBlocked []string
	for _, iss := range ready {
		gotReady = append(gotReady, iss.ID)
	}
	for _, b := range blocked {
		gotBlocked = append(gotBlocked, b.Issue.ID)
	}
	if !slices.Equal(gotReady, wantReady) || !slices.Equal(gotBlocked, wantBlocked) {
		t.Errorf("Ready = %v (Graph %v), Blocked = %v (Graph %v)", gotReady, wantReady, gotBlocked, wantBlocked)
	}
}

func TestDetails_ReturnsEachDetailInTheOrderAsked(t *testing.T) {
	s, _ := batchStore(t)
	epic := mustCreate(t, s, CreateInput{Title: "epic", Type: TypeEpic})
	child := mustCreate(t, s, CreateInput{Title: "child", Parent: epic.ID})
	if _, err := s.Close(child.ID, "done"); err != nil {
		t.Fatal(err)
	}

	got, err := s.Details(child.ID, epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{child.ID, epic.ID} {
		want, err := s.Detail(id)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got[i], want) {
			t.Errorf("Details[%d] = %+v\nwant Detail(%s) = %+v", i, got[i], id, want)
		}
	}
}

func TestDetails_OneMissingID_FailsTheCall(t *testing.T) {
	s, _ := batchStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	if _, err := s.Details(a.ID, "x-nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestQueryFields_EveryFieldCompiles(t *testing.T) {
	fields := QueryFields()
	if !slices.IsSorted(fields) || !slices.Contains(fields, "status") {
		t.Fatalf("QueryFields = %v", fields)
	}
	for _, f := range fields {
		// An unknown field is refused at the field name; a known one fails later
		// or not at all, depending on the value its kind takes.
		_, err := query.Compile(f + ` == ` + QuoteQueryValue(`a "b" \c`))
		var pe *ParseError
		if errors.As(err, &pe) && pe.Pos == 0 {
			t.Errorf("field %q is not known to the engine: %v", f, err)
		}
	}
}
