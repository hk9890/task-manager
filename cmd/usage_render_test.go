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

// In-process CLI tests for misuse-help — the compact help block shown when a command is
// invoked wrong, the unknown-subcommand error, and the "did you mean?" suggestion.
//
// They run in-process through Run, in the default suite: the whole subject is
// what lands on stdout and stderr, which needs no forked binary.
//
// The defining property is the split: a *misuse* (bad args, bad/missing flag,
// unknown subcommand) gets the helpful block; a *runtime* error (not found, etc.)
// stays terse. Both go to stderr with the "taskmgr: " prefix and leave stdout empty.
package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestMisuse_MissingArg_ShowsBlock: `show` with no id renders purpose, usage,
// example, and a --help pointer — not the bare cobra one-liner.
func TestMisuse_MissingArg_ShowsBlock(t *testing.T) {
	root := t.TempDir()
	stdout, stderr, code := run(t, "--dir", root, "show")
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

// TestMisuse_MissingRequiredFlag_ListsFlags: `create` with no --title lists the
// command's flags (so the agent sees --title right there), via the required-flag path.
func TestMisuse_MissingRequiredFlag_ListsFlags(t *testing.T) {
	root := newStore(t)
	stdout, stderr, code := run(t, "--dir", root, "create")
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

	// What the block must NOT carry. Asserting only the contents let the two
	// exclusions in localFlagLines be dropped with this test green: the flag
	// list would then gain the auto-added help flag and every flag the author
	// deliberately marked Hidden, in the agent-facing output CLI-SPEC keeps
	// terse. The block ends with a "Run 'taskmgr create --help'" pointer, so
	// the check is scoped to the flags section rather than the whole message.
	flagBlock := flagsSection(t, stderr)
	for _, unwanted := range []string{"--help", "-h,"} {
		if strings.Contains(flagBlock, unwanted) {
			t.Errorf("the flags block lists %q; the auto-added help flag is skipped on purpose\n---\n%s",
				unwanted, flagBlock)
		}
	}
	for _, hidden := range hiddenFlagNames(t, "create") {
		if strings.Contains(flagBlock, "--"+hidden) {
			t.Errorf("the flags block lists the hidden flag --%s\n---\n%s", hidden, flagBlock)
		}
	}
}

// flagsSection returns the lines between the "flags:" header and the blank line
// that ends it, which is the block localFlagLines produced.
func flagsSection(t *testing.T, stderr string) string {
	t.Helper()
	_, after, found := strings.Cut(stderr, "\nflags:\n")
	if !found {
		t.Fatalf("no flags block in:\n%s", stderr)
	}
	block, _, _ := strings.Cut(after, "\n\n")
	return block
}

// hiddenFlagNames returns the names of the flags the named command marks Hidden,
// so the assertion above names real flags rather than a guess that would pass
// once someone unhid one.
func hiddenFlagNames(t *testing.T, cmdName string) []string {
	t.Helper()
	var target *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == cmdName {
			target = c
			break
		}
	}
	if target == nil {
		t.Fatalf("command %q not found in the tree", cmdName)
	}
	var names []string
	target.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			names = append(names, f.Name)
		}
	})
	return names
}

// TestMisuse_FlagBlock_SkipsHiddenFlags proves the exclusion on a flag that is
// hidden for the length of the test, so the assertion holds whether or not the
// command tree happens to carry a hidden flag today.
func TestMisuse_FlagBlock_SkipsHiddenFlags(t *testing.T) {
	var create *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == "create" {
			create = c
			break
		}
	}
	if create == nil {
		t.Fatal("create command not found in the tree")
	}
	f := create.Flags().Lookup("assignee")
	if f == nil {
		t.Fatal("create has no --assignee flag to hide")
	}
	f.Hidden = true
	t.Cleanup(func() { f.Hidden = false })

	root := newStore(t)
	_, stderr, code := run(t, "--dir", root, "create")
	if code != 1 {
		t.Fatalf("create (no --title): expected exit 1, got %d", code)
	}

	block := flagsSection(t, stderr)
	if strings.Contains(block, "--assignee") {
		t.Errorf("the flags block lists --assignee while it is hidden\n---\n%s", block)
	}
	if !strings.Contains(block, "--title") {
		t.Errorf("hiding one flag removed the rest of the block\n---\n%s", block)
	}
}

// TestMisuse_UnknownSubcommand_Errors: an unknown subcommand exits 1 with a
// suggestion — not the silent exit-0 help that cobra prints by default.
func TestMisuse_UnknownSubcommand_Errors(t *testing.T) {
	root := t.TempDir()
	stdout, stderr, code := run(t, "--dir", root, "dep", "addd", "a", "b")
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

// TestMisuse_UnknownCommand_Suggests: a top-level typo gets cobra's built-in
// "did you mean?" (enabled, surfaced through our error path).
func TestMisuse_UnknownCommand_Suggests(t *testing.T) {
	root := t.TempDir()
	_, stderr, code := run(t, "--dir", root, "shw")
	if code != 1 {
		t.Fatalf("shw: expected exit 1, got %d", code)
	}
	for _, want := range []string{"unknown command", "Did you mean", "show"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("unknown-command output missing %q\n---\n%s", want, stderr)
		}
	}
}

// TestMisuse_SubcommandName_InMessage: a leaf subcommand names its full path in
// the "needs" message ("dep add needs …"), not just the leaf word.
func TestMisuse_SubcommandName_InMessage(t *testing.T) {
	root := t.TempDir()
	_, stderr, code := run(t, "--dir", root, "dep", "add")
	if code != 1 {
		t.Fatalf("dep add (no args): expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "dep add needs") {
		t.Errorf("expected 'dep add needs …' in message; stderr=%q", stderr)
	}
}

// TestRuntimeError_StaysTerse is the guard for the central design property: a
// genuine runtime failure (unknown id) must NOT be dressed up with the usage block.
func TestRuntimeError_StaysTerse(t *testing.T) {
	root := newStore(t)
	_, stderr, code := run(t, "--dir", root, "show", "tst-9999")
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
