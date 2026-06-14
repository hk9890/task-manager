//go:build integration

// L4 CLI test for the no-store hint: when no .tasks store exists, the error names
// the fix (`taskmgr init`) instead of dead-ending. CLI-SPEC §1 (Discovery).
package cmd_test

import (
	"strings"
	"testing"
)

// TestL4_NoStore_HintsInit verifies that a store-backed command run where no store
// exists fails with an actionable hint on stderr (and leaves stdout empty).
func TestL4_NoStore_HintsInit(t *testing.T) {
	root := t.TempDir() // a bare directory: no .tasks anywhere up the tree.

	for _, args := range [][]string{
		{"list"},
		{"show", "abc-123"},
		{"--json", "list"},
	} {
		stdout, stderr, code := taskmgr(t, root, args...)
		if code != 1 {
			t.Errorf("%v: expected exit 1, got %d", args, code)
		}
		if strings.TrimSpace(stdout) != "" {
			t.Errorf("%v: stdout must be empty on error; got %q", args, stdout)
		}
		if !strings.HasPrefix(stderr, "taskmgr: ") {
			t.Errorf("%v: error not prefixed 'taskmgr: '; stderr=%q", args, stderr)
		}
		if !strings.Contains(stderr, "no .tasks directory found") {
			t.Errorf("%v: missing the no-store message; stderr=%q", args, stderr)
		}
		if !strings.Contains(stderr, "taskmgr init") {
			t.Errorf("%v: no-store error should suggest 'taskmgr init'; stderr=%q", args, stderr)
		}
	}
}
