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

// White-box unit test (L1, no FS): the guide is hand-written prose, so unlike the
// derived `commands` catalog it can drift from the model. This is its drift guard.
package cmd

import (
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// TestGuideText_CoversModel fails if a status or type is added to the SDK without
// also being reflected in the guide's "## The model" section. It is a presence
// check: the point is to catch a *new* value (a fresh status/type would be absent
// here), not to validate phrasing.
func TestGuideText_CoversModel(t *testing.T) {
	for _, s := range tasks.Statuses {
		if !strings.Contains(guideText, string(s)) {
			t.Errorf("guideText omits status %q — update the guide's model section", s)
		}
	}
	for _, ty := range tasks.Types {
		if !strings.Contains(guideText, string(ty)) {
			t.Errorf("guideText omits type %q — update the guide's model section", ty)
		}
	}
}
