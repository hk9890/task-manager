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
