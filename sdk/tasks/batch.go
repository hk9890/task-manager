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

// batch.go — the pure half of CreateBatch: the entry type, the error that names
// a refused entry, the rule that turns a set's local refs into issue IDs, and
// the order a set is written in.
package tasks

import (
	"errors"
	"fmt"
	"strings"
)

// maxBatchEntries bounds a CreateBatch set. The store lock is held across the
// pre-create hooks of every entry, so the bound is what caps that hold; 256 is
// the bound the edge lists already use.
const maxBatchEntries = 256

// BatchEntry is one issue of a CreateBatch set.
type BatchEntry struct {
	// Ref names this entry inside the set, so another entry's Parent, BlockedBy
	// or Related can point at an issue that has no ID yet. It is never stored.
	// Empty when nothing in the set refers to the entry.
	Ref string
	CreateInput
}

// BatchEntryError is the refusal of one entry of a CreateBatch set. Nothing of
// the set was written. Err is the error a single Create of that entry returns,
// so errors.Is and errors.As see through to it. The one exception is an edge
// that names neither a ref of the set nor a valid issue ID, which only a set
// can hold.
type BatchEntryError struct {
	Index int // zero-based position in the set
	Ref   string
	Err   error
}

func (e *BatchEntryError) Error() string {
	name := fmt.Sprintf("entry %d", e.Index+1)
	if e.Ref != "" {
		name += fmt.Sprintf(" (ref %q)", e.Ref)
	}
	return name + ": " + e.Err.Error()
}

func (e *BatchEntryError) Unwrap() error { return e.Err }

// resolveBatchRefs returns the entries as plain create inputs, with every edge
// that names a ref of the set replaced by the ID allocated to that entry; ids[i]
// is the ID of entries[i]. An edge that names no ref is left for the reference
// check to resolve as the ID of an existing issue. On a refusal, failed is the
// index of the entry at fault.
//
// A ref may not carry the store prefix: it could then equal the ID of an existing
// issue, and an edge naming it would silently bind to the wrong one.
//
// Refs and edges are compared trimmed, as the edges of an issue are stored.
func resolveBatchRefs(prefix string, entries []BatchEntry, ids []string) (inputs []CreateInput, failed int, err error) {
	idOf := make(map[string]string, len(entries))
	for i, e := range entries {
		ref := strings.TrimSpace(e.Ref)
		if ref == "" {
			continue
		}
		if strings.HasPrefix(ref, prefix+"-") {
			return nil, i, invalid("ref", "%q carries the store prefix %q, which is reserved for issue IDs", ref, prefix)
		}
		if _, dup := idOf[ref]; dup {
			return nil, i, invalid("ref", "%q is already used by an earlier entry", ref)
		}
		idOf[ref] = ids[i]
	}
	resolve := func(edge string) string {
		edge = strings.TrimSpace(edge)
		if id, ok := idOf[edge]; ok {
			return id
		}
		return edge
	}
	inputs = make([]CreateInput, len(entries))
	for i, e := range entries {
		in := e.CreateInput
		in.ID = ids[i]
		in.Parent = resolve(in.Parent)
		in.BlockedBy = mapStrings(in.BlockedBy, resolve)
		in.Related = mapStrings(in.Related, resolve)
		inputs[i] = in
	}
	return inputs, 0, nil
}

// edgeNamingNothing refuses the first edge of the set that is neither a ref of
// the set nor a valid issue ID; failed is the index of its entry, and -1 when
// there is none. Left to the field check, such an edge is refused as an invalid
// ID, which does not tell a caller who misspelt a ref that no entry has that
// name. Create has no refs and keeps the field check's message.
func edgeNamingNothing(entries []BatchEntry) (failed int, err error) {
	isRef := make(map[string]bool, len(entries))
	for _, e := range entries {
		isRef[strings.TrimSpace(e.Ref)] = true
	}
	for i, e := range entries {
		edges := []struct {
			field  string
			values []string
		}{
			{"parent", []string{e.Parent}},
			{"blocked_by", e.BlockedBy},
			{"related", e.Related},
		}
		for _, edge := range edges {
			for _, v := range edge.values {
				v = strings.TrimSpace(v)
				if v == "" || isRef[v] || validIssueID(v) {
					continue
				}
				return i, invalid(edge.field, "%q is neither a ref of this set nor a valid issue ID", v)
			}
		}
	}
	return -1, nil
}

// namedByRef returns a validation error of a set with each allocated ID replaced
// by the ref its entry carries: the caller wrote the refs, and the IDs name
// issues that were never written.
func namedByRef(err error, entries []BatchEntry, ids []string) error {
	var ve *ValidationError
	if !errors.As(err, &ve) {
		return err
	}
	var refOf []string
	for i, e := range entries {
		if ref := strings.TrimSpace(e.Ref); ref != "" {
			refOf = append(refOf, ids[i], ref)
		}
	}
	return invalid(ve.Field, "%s", strings.NewReplacer(refOf...).Replace(ve.Message))
}

func mapStrings(in []string, f func(string) string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

// batchWriteOrder returns the indexes of issues in the order to write them: an
// issue after every issue of the set its edges name. Each file on disk then
// references only files already there, so a set cut short between two writes
// leaves issues the engine accepts. Two issues that are related to each other
// have no such order and are written in entry order.
func batchWriteOrder(issues []*Issue) []int {
	indexOf := make(map[string]int, len(issues))
	for i, iss := range issues {
		indexOf[iss.ID] = i
	}
	order := make([]int, 0, len(issues))
	visited := make([]bool, len(issues))
	var visit func(i int)
	visit = func(i int) {
		if visited[i] {
			return
		}
		visited[i] = true
		iss := issues[i]
		for _, id := range append(append([]string{iss.Parent}, iss.BlockedBy...), iss.Related...) {
			if j, inSet := indexOf[id]; inSet {
				visit(j)
			}
		}
		order = append(order, i)
	}
	for i := range issues {
		visit(i)
	}
	return order
}
