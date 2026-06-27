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
	"strings"
	"testing"
	"time"
)

// TestMarshalCommentDoc_TombstoneCreatedUnquoted guards the cosmetic fix for
// at-azw: every comment document — tombstones included — must emit the created
// timestamp as a bare scalar (not yaml.v3's quoted !!timestamp form), matching
// TASK-STORAGE-SPEC §6 and every non-tombstone doc.
func TestMarshalCommentDoc_TombstoneCreatedUnquoted(t *testing.T) {
	ts := time.Date(2026, 6, 5, 10, 34, 15, 0, time.UTC)
	out := string(marshalCommentDoc(Comment{
		ID:       "abcd1234",
		Author:   "hans",
		Created:  ts,
		Replaces: "ffff0000",
		Deleted:  true,
	}))

	if !strings.Contains(out, "created: 2026-06-05T10:34:15Z") {
		t.Errorf("tombstone created must be a bare scalar; got:\n%s", out)
	}
	if strings.Contains(out, `created: "`) {
		t.Errorf("tombstone created must not be quoted; got:\n%s", out)
	}
	if !strings.Contains(out, "deleted: true") {
		t.Errorf("tombstone must carry deleted: true; got:\n%s", out)
	}

	// Still round-trips back to a tombstone.
	docs, err := parseCommentStream([]byte(out))
	if err != nil {
		t.Fatalf("parseCommentStream: %v", err)
	}
	if len(docs) != 1 || !docs[0].Deleted || docs[0].Replaces != "ffff0000" {
		t.Errorf("tombstone did not round-trip: %+v", docs)
	}
}
