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
// a refused entry, and the rule that turns a set's local refs into issue IDs.
package tasks

import (
	"fmt"
	"strings"
)

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
// so errors.Is and errors.As see through to it.
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
// check, which accepts it only as the ID of an existing issue.
//
// A ref may not carry the store prefix: it could then equal the ID of an existing
// issue, and an edge naming it would silently bind to the wrong one.
func resolveBatchRefs(prefix string, entries []BatchEntry, ids []string) ([]CreateInput, error) {
	idOf := make(map[string]string, len(entries))
	for i, e := range entries {
		if e.Ref == "" {
			continue
		}
		if strings.HasPrefix(e.Ref, prefix+"-") {
			return nil, &BatchEntryError{i, e.Ref, invalid("ref", "%q carries the store prefix %q, which is reserved for issue IDs", e.Ref, prefix)}
		}
		if _, dup := idOf[e.Ref]; dup {
			return nil, &BatchEntryError{i, e.Ref, invalid("ref", "%q is already used by an earlier entry", e.Ref)}
		}
		idOf[e.Ref] = ids[i]
	}
	resolve := func(edge string) string {
		if id, ok := idOf[edge]; ok {
			return id
		}
		return edge
	}
	inputs := make([]CreateInput, len(entries))
	for i, e := range entries {
		in := e.CreateInput
		in.ID = ids[i]
		in.Parent = resolve(in.Parent)
		in.BlockedBy = mapStrings(in.BlockedBy, resolve)
		in.Related = mapStrings(in.Related, resolve)
		inputs[i] = in
	}
	return inputs, nil
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
