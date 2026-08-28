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

//go:build integration

// creator_l3_test.go — the creator field against a real temp-dir store.
//
// This case lived in creator_test.go, which carries no build tag, so it built a
// real store on disk in the default `mise run test` run. The layer a test sits
// in is decided by what it touches, not by which file it was written next to
// (TESTING.md): anything on a real disk is L3 and belongs behind the
// `integration` tag, where the L3 suite runs it.
package tasks_test

import (
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/storetest"
)

// TestCreator_BuilderCreatorPersistsTempDir verifies the builder creator opt
// works on a real temp-dir store (L3 durability).
func TestCreator_BuilderCreatorPersistsTempDir(t *testing.T) {
	s := storetest.New(t).
		Issue("tst-0001", storetest.Creator("carol")).
		TempDir(t)

	got, err := s.Get("tst-0001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Creator != "carol" {
		t.Errorf("Creator = %q, want %q (L3 round-trip)", got.Creator, "carol")
	}
}
