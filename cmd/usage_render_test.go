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
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hk9890/task-manager/sdk/tasks"
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

// commandFlags is the flag set a command parses with — its own flags and the
// root's persistent ones — without running it.
func commandFlags(c *cobra.Command) *pflag.FlagSet {
	c.InheritedFlags()
	return c.Flags()
}

// TestQueryInvocation_RewritesTheCallersOwnCommand: every guessed field flag and
// the -q already given become one -q expression, and everything else the caller
// typed stays in place.
func TestQueryInvocation_RewritesTheCallersOwnCommand(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		// Several fields, and a query the caller already gave.
		{[]string{"list", "--status", "open", "--type", "bug"}, `To filter by status and type: taskmgr list -q 'status == "open" && type == "bug"'`},
		{[]string{"list", "--label", "a", "--label", "b"}, `To filter by label: taskmgr list -q 'label == "a" && label == "b"'`},
		{[]string{"list", "-q", "priority <= 1", "--status", "open"}, `To filter by status: taskmgr list -q 'priority <= 1 && status == "open"'`},
		{[]string{"list", "--status", "open", "--query=ready || priority == 0"}, `To filter by status: taskmgr list -q '(ready || priority == 0) && status == "open"'`},
		{[]string{"list", "-qready", "--type", "bug"}, `To filter by type: taskmgr list -q 'ready && type == "bug"'`},
		{[]string{"list", "-q=ready", "--type", "bug"}, `To filter by type: taskmgr list -q 'ready && type == "bug"'`},
		{[]string{"list", "-q", "", "--type", "bug"}, `To filter by type: taskmgr list -q 'type == "bug"'`},

		// The rest of the invocation is kept, quoted where a shell needs it.
		{[]string{"-C", "/x", "--json", "list", "--all", "--type", "bug"}, `To filter by type: taskmgr -C /x --json list --all -q 'type == "bug"'`},
		{[]string{"--store-name", "my store", "list", "--type", "bug", "--sort", "created", "--limit=5", "--reverse"}, `To filter by type: taskmgr --store-name 'my store' list -q 'type == "bug"' --sort created --limit=5 --reverse`},
		{[]string{"list", "--sort", "--status", "--type", "bug"}, `To filter by type: taskmgr list --sort --status -q 'type == "bug"'`},
		{[]string{"search", "drill", "--type", "bug"}, `To filter by type: taskmgr search drill -q 'type == "bug"'`},
		{[]string{"search", "it's here", "--type", "bug", "more"}, `To filter by type: taskmgr search 'it'\''s here' -q 'type == "bug"' more`},
		{[]string{"list", "--status", "open", "--", "--type", "bug"}, `To filter by status: taskmgr list -q 'status == "open"' -- --type bug`},

		// The value: both spellings, an empty one, a missing one.
		{[]string{"list", "--parent=at-abc123"}, `To filter by parent: taskmgr list -q 'parent == "at-abc123"'`},
		{[]string{"list", "--parent="}, `To filter by parent: taskmgr list -q 'parent == ""'`},
		{[]string{"list", "--assignee="}, `To filter by assignee: taskmgr list -q 'assignee == ""'`},
		{[]string{"list", "--status"}, `To filter by status: taskmgr list -q 'status == "<value>"'`},
		{[]string{"list", "--assignee"}, `To filter by assignee: taskmgr list -q 'assignee == "<value>"'`},
		{[]string{"list", "--status", "wip"}, `To filter by status: taskmgr list -q 'status == "wip"'`},

		// What ends a value: a flag of the command or another field, not any dash.
		{[]string{"list", "--status", "--all"}, `To filter by status: taskmgr list -q 'status == "<value>"' --all`},
		{[]string{"list", "--status", "-C", "/x"}, `To filter by status: taskmgr list -q 'status == "<value>"' -C /x`},
		{[]string{"list", "--status", "--type", "bug"}, `To filter by status and type: taskmgr list -q 'status == "<value>" && type == "bug"'`},
		{[]string{"list", "--status", "--"}, `To filter by status: taskmgr list -q 'status == "<value>"' --`},
		{[]string{"list", "--label", "-wontfix"}, `To filter by label: taskmgr list -q 'label == "-wontfix"'`},
		{[]string{"list", "--label", "--wontfix"}, `To filter by label: taskmgr list -q 'label == "--wontfix"'`},

		// Priority is a bare integer, in either form the tool prints.
		{[]string{"list", "--priority", "1"}, `To filter by priority: taskmgr list -q 'priority == 1'`},
		{[]string{"list", "--priority", "P1"}, `To filter by priority: taskmgr list -q 'priority == 1'`},
		{[]string{"list", "--priority=p0"}, `To filter by priority: taskmgr list -q 'priority == 0'`},
		{[]string{"list", "--priority"}, `To filter by priority: taskmgr list -q 'priority == <0-4>'`},
		{[]string{"list", "--priority="}, `To filter by priority: taskmgr list -q 'priority == <0-4>'`},
		{[]string{"list", "--priority", "high"}, `To filter by priority: taskmgr list -q 'priority == <0-4>'`},
		{[]string{"list", "--priority", "-1"}, `To filter by priority: taskmgr list -q 'priority == <0-4>'`},
		{[]string{"list", "--priority", "99999999999999999999"}, `To filter by priority: taskmgr list -q 'priority == <0-4>'`},

		// Dates, and --closed alone, which asks for the closed issues.
		{[]string{"list", "--created", "2026-01-01"}, `To filter by created: taskmgr list -q 'created >= "2026-01-01"'`},
		{[]string{"list", "--closed=2026-01-01T09:00:00Z"}, `To filter by closed: taskmgr list -q 'closed >= "2026-01-01T09:00:00Z"'`},
		{[]string{"list", "--created"}, `To filter by created: taskmgr list -q 'created >= "<YYYY-MM-DD>"'`},
		{[]string{"list", "--updated="}, `To filter by updated: taskmgr list -q 'updated >= "<YYYY-MM-DD>"'`},
		{[]string{"list", "--closed"}, `To filter by closed: taskmgr list --all -q 'status == "closed"'`},
		{[]string{"list", "--all", "--closed", "--type", "bug"}, `To filter by closed and type: taskmgr list --all -q 'status == "closed" && type == "bug"'`},
		{[]string{"search", "login", "--closed", "bug"}, `To filter by closed: taskmgr search login --all -q 'status == "closed"' bug`},
		{[]string{"search", "login", "--closed", "2026-01-01"}, `To filter by closed: taskmgr search login -q 'closed >= "2026-01-01"'`},

		// The two escapes of the expression, inside the one of the shell.
		{[]string{"list", "--text", `it's "x"`}, `To filter by text: taskmgr list -q 'text ~ "it'\''s \"x\""'`},
		{[]string{"list", "--assignee", `a\b`}, `To filter by assignee: taskmgr list -q 'assignee == "a\\b"'`},
		{[]string{"list", "--created", `a\"b`}, `To filter by created: taskmgr list -q 'created >= "a\\\"b"'`},

		// Nothing to fold.
		{[]string{"list", "--nope"}, ""},
		{[]string{"list", "--", "--status", "open"}, ""},
	} {
		cmd := listCmd
		if slices.Contains(tc.args, "search") {
			cmd = searchCmd
		}
		if got := queryInvocation(commandFlags(cmd), tc.args); got != tc.want {
			t.Errorf("%q:\n got %s\nwant %s", tc.args, got, tc.want)
		}
	}
}

// TestMisuse_FilterFieldFlag_HintsQueryExpression: a filter field guessed as a
// flag puts the rewritten command right under the headline, and the help block
// still follows.
func TestMisuse_FilterFieldFlag_HintsQueryExpression(t *testing.T) {
	root := newStore(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"list", "--status", "in_progress"}, `To filter by status: taskmgr --dir ` + root + ` list -q 'status == "in_progress"'`},
		{[]string{"search", "drill", "--type", "bug", "--parent=at-abc123"}, `To filter by type and parent: taskmgr --dir ` + root + ` search drill -q 'type == "bug" && parent == "at-abc123"'`},
	} {
		stdout, stderr, code := run(t, append([]string{"--dir", root}, tc.args...)...)
		if code != 1 {
			t.Errorf("%v: expected exit 1, got %d", tc.args, code)
		}
		if strings.TrimSpace(stdout) != "" {
			t.Errorf("%v: misuse must leave stdout empty; got %q", tc.args, stdout)
		}
		lines := strings.Split(stderr, "\n")
		if len(lines) < 3 || !strings.HasPrefix(lines[0], "taskmgr: unknown flag: --") || lines[1] != tc.want || lines[2] != "" {
			t.Errorf("%v: want the headline, then %q, then a blank line\n---\n%s", tc.args, tc.want, stderr)
		}
		if !strings.Contains(stderr, "usage:") {
			t.Errorf("%v: the hint must not replace the help block\n---\n%s", tc.args, stderr)
		}
	}
}

// TestMisuse_UnknownFlag_NoFilterHint: the hint is for a filter field on a command
// that takes -q. Any other unknown flag, and a field name on a command that cannot
// filter, keep the block as it was — the headline, then a blank line.
func TestMisuse_UnknownFlag_NoFilterHint(t *testing.T) {
	root := newStore(t)
	for _, tc := range []struct {
		args     []string
		headline string
	}{
		{[]string{"list", "--nope"}, "taskmgr: unknown flag: --nope\n\n"},
		{[]string{"list", "--nope", "--status", "open"}, "taskmgr: unknown flag: --nope\n\n"},
		{[]string{"show", "tst-0001", "--status", "open"}, "taskmgr: unknown flag: --status\n\n"},
	} {
		_, stderr, code := run(t, append([]string{"--dir", root}, tc.args...)...)
		if code != 1 {
			t.Errorf("%v: expected exit 1, got %d", tc.args, code)
		}
		if !strings.HasPrefix(stderr, tc.headline) || strings.Contains(stderr, "To filter by") {
			t.Errorf("%v: want the bare headline %q and no filter hint\n---\n%s", tc.args, tc.headline, stderr)
		}
	}
}

// hintedQuery runs a rejected invocation and returns the arguments that follow
// "list" in the command its hint prints.
func hintedQuery(t *testing.T, root string, args ...string) []string {
	t.Helper()
	_, stderr, _ := run(t, append([]string{"--dir", root}, args...)...)
	lines := strings.Split(stderr, "\n")
	if len(lines) < 2 {
		t.Fatalf("%v: no line under the headline\n---\n%s", args, stderr)
	}
	_, after, found := strings.Cut(lines[1], " list ")
	flags, quoted, isQuery := strings.Cut(after, "-q '")
	if !found || !isQuery {
		t.Fatalf("%v: no filter hint under the headline\n---\n%s", args, stderr)
	}
	return append(strings.Fields(flags), "-q", strings.TrimSuffix(quoted, "'"))
}

// TestFilterFieldOps_EveryHintIsAnAcceptedQuery runs the hint printed for every
// entry of filterFieldOps back through `list`, which fails on a name that is not a
// field and on an operator or value form the field does not take.
func TestFilterFieldOps_EveryHintIsAnAcceptedQuery(t *testing.T) {
	samples := map[string]string{
		"status": "in_progress", "type": "bug", "priority": "P1",
		"assignee": "ada", "creator": "ada", "agent": "claude-code", "session": "s-1",
		"parent": "tst-0001", "label": "area:x", "text": "drill",
		"created": "2026-01-01", "updated": "2026-01-01", "closed": "2026-01-01T09:00:00Z",
	}
	root := newStore(t)
	for field := range filterFieldOps {
		sample, ok := samples[field]
		if !ok {
			t.Errorf("no sample value for filter field %q", field)
			continue
		}
		query := hintedQuery(t, root, "list", "--"+field, sample)
		if _, stderr, code := run(t, append([]string{"--dir", root, "list"}, query...)...); code != 0 {
			t.Errorf("--%s: list %q exited %d: %s", field, query, code, stderr)
		}
	}
}

// TestMisuse_ClosedFlagWithoutValue_HintListsClosedIssues: the command printed
// for a bare --closed is the one that returns the closed issues.
func TestMisuse_ClosedFlagWithoutValue_HintListsClosedIssues(t *testing.T) {
	root := newStore(t)
	open, closed := createIssue(t, root), createIssue(t, root)
	if _, stderr, code := run(t, "--dir", root, "close", closed); code != 0 {
		t.Fatalf("close: exit %d, stderr %q", code, stderr)
	}
	query := hintedQuery(t, root, "list", "--closed")
	stdout, stderr, code := run(t, append([]string{"--dir", root, "list"}, query...)...)
	if code != 0 {
		t.Fatalf("list %q exited %d: %s", query, code, stderr)
	}
	if !strings.Contains(stdout, closed) || strings.Contains(stdout, open) {
		t.Errorf("list %q: want %s and not %s\n---\n%s", query, closed, open, stdout)
	}
}

// A field added to the engine fails here until it has an operator in
// filterFieldOps.
func TestFilterFieldOps_MatchesTheEngineFields(t *testing.T) {
	engine, hinted := tasks.QueryFields(), slices.Sorted(maps.Keys(filterFieldOps))
	if !slices.Equal(engine, hinted) {
		t.Errorf("filterFieldOps is out of step with tasks.QueryFields\nengine: %v\nhinted: %v", engine, hinted)
	}
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
