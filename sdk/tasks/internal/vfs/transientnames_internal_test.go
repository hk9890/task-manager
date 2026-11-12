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

// transientnames_internal_test.go — pins the name of the atomic-write temp
// file, which `taskmgr sync` ignores.
//
// The .gitignore `taskmgr sync init` writes (CLI-SPEC §2.5) lists this name.
// A rename that fails here must update that .gitignore and the spec too, or
// sync starts to publish the file.
package vfs

import "testing"

func TestTempPrefix_IsPinnedForTheSyncGitignore(t *testing.T) {
	if tempPrefix != ".tmp-" {
		t.Errorf("tempPrefix = %q; update syncGitignore in cmd/sync.go and CLI-SPEC §2.5 with it", tempPrefix)
	}
}
