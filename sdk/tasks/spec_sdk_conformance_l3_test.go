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

// spec_sdk_conformance_l3_test.go — the SDK-SPEC conformance cases that need a
// real filesystem. Open's local walk-up is defined over real directories, so
// "no store anywhere above here" cannot be posed on the in-memory seam. The
// rest of the suite is in spec_sdk_conformance_test.go, at L2.
package tasks_test

import (
	"errors"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// TestSpec_SDK_ErrNoStore_FromOpen verifies that Open returns ErrNoStore when
// there is no .tasks directory (SDK-SPEC §1, §6).
func TestSpec_SDK_ErrNoStore_FromOpen(t *testing.T) {
	// t.TempDir() is a real empty directory with no .tasks child.
	empty := t.TempDir()
	_, err := tasks.Open(empty)
	if err == nil {
		t.Fatal("Open on empty dir: expected ErrNoStore, got nil")
	}
	if !errors.Is(err, tasks.ErrNoStore) {
		t.Errorf("Open on empty dir: got %v, want errors.Is(ErrNoStore)", err)
	}
}
