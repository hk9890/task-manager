//go:build integration

// L4 CLI tests for misuse-help — the compact help block shown when a command is
// invoked wrong, the unknown-subcommand error, and the "did you mean?" suggestion.
//
// The defining property is the split: a *misuse* (bad args, bad/missing flag,
// unknown subcommand) gets the helpful block; a *runtime* error (not found, etc.)
// stays terse. Both go to stderr with the "taskmgr: " prefix and leave stdout empty.
package cmd_test

import (
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// TestL4_Misuse_MissingArg_ShowsBlock: `show` with no id renders purpose, usage,
// example, and a --help pointer — not the bare cobra one-liner.
func TestL4_Misuse_MissingArg_ShowsBlock(t *testing.T) {
	root := t.TempDir()
	stdout, stderr, code := taskmgr(t, root, "show")
	if code != 1 {
		t.Fatalf("show (no id): expected exit 1, got %d", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("misuse must leave stdout empty; got %q", stdout)
	}
	if !strings.HasPrefix(stderr, "taskmgr: ") {
		t.Errorf("misuse error not prefixed 'taskmgr: '; stderr=%q", stderr)
	}
	for _, want := range []string{"needs", "Show full detail", "usage:", "example:", "--help"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("misuse block missing %q\n---\n%s", want, stderr)
		}
	}
}

// TestL4_Misuse_MissingRequiredFlag_ListsFlags: `create` with no --title lists the
// command's flags (so the agent sees --title right there), via the required-flag path.
func TestL4_Misuse_MissingRequiredFlag_ListsFlags(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	stdout, stderr, code := taskmgr(t, root, "create")
	if code != 1 {
		t.Fatalf("create (no --title): expected exit 1, got %d", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("misuse must leave stdout empty; got %q", stdout)
	}
	for _, want := range []string{"required flag", "usage:", "flags:", "--title"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("create misuse block missing %q\n---\n%s", want, stderr)
		}
	}
}

// TestL4_Misuse_UnknownSubcommand_Errors: an unknown subcommand exits 1 with a
// suggestion — not the silent exit-0 help that cobra prints by default.
func TestL4_Misuse_UnknownSubcommand_Errors(t *testing.T) {
	root := t.TempDir()
	stdout, stderr, code := taskmgr(t, root, "dep", "addd", "a", "b")
	if code != 1 {
		t.Fatalf("dep addd: expected exit 1, got %d (stdout=%q)", code, stdout)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("unknown subcommand must leave stdout empty; got %q", stdout)
	}
	for _, want := range []string{"unknown subcommand", "Did you mean", "add"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("unknown-subcommand output missing %q\n---\n%s", want, stderr)
		}
	}
}

// TestL4_Misuse_UnknownCommand_Suggests: a top-level typo gets cobra's built-in
// "did you mean?" (enabled, surfaced through our error path).
func TestL4_Misuse_UnknownCommand_Suggests(t *testing.T) {
	root := t.TempDir()
	_, stderr, code := taskmgr(t, root, "shw")
	if code != 1 {
		t.Fatalf("shw: expected exit 1, got %d", code)
	}
	for _, want := range []string{"unknown command", "Did you mean", "show"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("unknown-command output missing %q\n---\n%s", want, stderr)
		}
	}
}

// TestL4_Misuse_SubcommandName_InMessage: a leaf subcommand names its full path in
// the "needs" message ("dep add needs …"), not just the leaf word.
func TestL4_Misuse_SubcommandName_InMessage(t *testing.T) {
	root := t.TempDir()
	_, stderr, code := taskmgr(t, root, "dep", "add")
	if code != 1 {
		t.Fatalf("dep add (no args): expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "dep add needs") {
		t.Errorf("expected 'dep add needs …' in message; stderr=%q", stderr)
	}
}

// TestL4_RuntimeError_StaysTerse is the guard for the central design property: a
// genuine runtime failure (unknown id) must NOT be dressed up with the usage block.
func TestL4_RuntimeError_StaysTerse(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	_, stderr, code := taskmgr(t, root, "show", "tst-9999")
	if code != 1 {
		t.Fatalf("show unknown id: expected exit 1, got %d", code)
	}
	if !strings.HasPrefix(stderr, "taskmgr: ") {
		t.Errorf("runtime error not prefixed 'taskmgr: '; stderr=%q", stderr)
	}
	// The teaching message must stand alone — no usage/example/flags scaffolding.
	for _, unwanted := range []string{"usage:", "example:", "Run 'taskmgr"} {
		if strings.Contains(stderr, unwanted) {
			t.Errorf("runtime error should stay terse but contained %q\n---\n%s", unwanted, stderr)
		}
	}
}
