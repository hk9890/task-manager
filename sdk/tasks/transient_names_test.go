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

// transient_names_test.go — pins the names of the engine's transient files
// that `taskmgr sync` ignores.
//
// The .gitignore `taskmgr sync init` writes (CLI-SPEC §2.5) lists these names.
// A rename that fails here must update that .gitignore and the spec too, or
// sync starts to publish the files.
package tasks

import (
	"path/filepath"
	"testing"
)

func TestTransientNames_ArePinnedForTheSyncGitignore(t *testing.T) {
	if stagingPrefix != ".incoming-" {
		t.Errorf("stagingPrefix = %q; update syncGitignore in cmd/sync.go and CLI-SPEC §2.5 with it", stagingPrefix)
	}
	s, _ := newMemStore(t)
	if got, want := s.stagedContentPath("abc-0001"), filepath.Join(s.contentDir(), ".abc-0001.incoming"); got != want {
		t.Errorf("stagedContentPath = %q, want %q; update syncGitignore in cmd/sync.go and CLI-SPEC §2.5 with it", got, want)
	}
}
