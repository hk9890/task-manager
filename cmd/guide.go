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

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// guideText is the canonical, binary-owned how-to for taskmgr. It is the prose
// sibling of `commands` (the machine catalog): different jobs, both kept. Keep it
// lean — the model, the everyday loop, the filter basics, and, above all, where to
// find more. Because it ships in the binary, a consuming skill collapses to "run
// `taskmgr guide` and follow it".
//
// Unlike `commands` (derived from the live command tree, so it literally cannot
// drift), this prose is hand-maintained. guide_test.go guards the model lists
// (statuses, types) against the SDK so they cannot silently fall out of step; keep
// the rest honest by hand when the CLI changes.
//
// Plain text on purpose (this is read in a terminal); no backticks so it can live
// in a raw string literal.
const guideText = `taskmgr — how to use it

taskmgr is an issue tracker you drive entirely through this CLI — create issues,
link them, find what is ready to work on, and record progress. It operates on the
project you run it from; pass -C <path> to target a project elsewhere.

## The model

Each issue has a type, a status, and a numeric priority:

  type      task (default) · bug · feature · epic · chore
  status    open · in_progress · blocked · deferred · closed
  priority  0 critical · 1 high · 2 normal (default) · 3 low · 4 trivial

Issues relate three ways:

  parent      grouping under an epic (one parent per issue)
  blocked-by  a hard dependency: the dependent is not "ready" until every
              blocker is closed (enforced acyclic)
  related     a non-blocking, symmetric link (set on one side, shown on both)

Two views are derived from the dependency graph:

  ready    open issues with no open blockers — what you can start now
           (epics appear here too; add type != epic for leaf tasks only)
  blocked  non-closed issues waiting on at least one open blocker

IDs are opaque (e.g. rep-fev72z), not sequential. Never guess one — capture it
from --json output and reuse it.

## The core loop

  # Create — only --title is required.
  taskmgr create --title "Add export endpoint" --type feature --priority 1

  # Find work, then inspect one issue
  taskmgr ready                 # actionable now, priority then age
  taskmgr blocked               # what is waiting, and on what
  taskmgr show <id>             # full detail: fields, edges, description, comments

  # Make progress
  taskmgr update <id> --status in_progress
  taskmgr comment add <id> "Chose ISO-8601 to match the reports module."
  taskmgr close <id> --reason "shipped in <commit>"

  # Wire relationships after the fact
  taskmgr dep add <dependent> <blocker>   # dependent becomes blocked by blocker
  taskmgr rel add <a> <b>                 # symmetric related link

## The description body

Each issue has one Markdown description body — put acceptance criteria,
instructions, and context there (there is no separate field for them).

--description "..." takes one inline string, fine for a single line. For a
multi-line body, --description-file reads a path, or "-" reads stdin — feed it a
heredoc so you do not fight shell quoting. The same pair works on create and
update; comments take --file the same way.

  taskmgr update <id> --description-file - <<'EOF'
  ## Acceptance criteria
  - [ ] UTF-8 with BOM
  - [ ] ISO-8601 dates
  EOF

  taskmgr create --title "Schema" --description-file notes.md   # ...or a file
  echo "scaffold pushed" | taskmgr comment add <id> --file -

Do not rely on --description "a\nb" — the \n is stored literally. Use
--description-file - (or, inline, $'a\nb' ANSI-C quoting).

update --description replaces the body — it does not append. To amend, run show,
then resubmit the full modified text. Prefer close --reason over
update --status closed so history explains itself. A mutation's --json echoes the
issue's scalar fields but not the description or comments — run show to confirm.

## Finding work with filters

taskmgr list -q '<expr>' selects issues with <field> <op> <value> predicates
joined by && || ! and parentheses:

  taskmgr list -q 'status == "open" && priority <= 1'
  taskmgr list -q 'type == bug && label ~ "area:reports"'
  taskmgr list -q 'ready && priority <= 2'
  taskmgr list --all -q 'closed > "2026-01-01"'
  taskmgr search export          # shorthand for: list -q 'text ~ "export"'
  taskmgr search drill nav       # every word must match: text ~ "drill" && text ~ "nav"

Fields:    status, type, priority, assignee, creator, parent, label,
           text (id/title/description), created, updated, closed,
           and the booleans ready / blocked
Operators: == != < <= > >= and ~ (case-insensitive substring)
Values:    quote strings ("open"); numbers and dates are bare or quoted;
           quote multi-word values — text ~ "drill nav", not text ~ drill nav

~ matches a substring, not a whole word: text ~ "rate" also matches "separate".
ready and blocked come from the dependency graph, not the status field — blocked
is not the same as status == "blocked" (an issue can be open yet blocked, or
carry the blocked status with no open blocker).

Closed issues are excluded unless the expression selects them or you pass --all.
taskmgr labels / statuses / types list the values actually in use.

## Output and exit conventions

Add --json to any command for stable snake_case output — parse that, do not
scrape the human table. Exit 0 on success, non-zero on error; the message goes to
stderr prefixed "taskmgr:" and names the offending field and the allowed values.

## Get more information

  taskmgr commands          machine catalog of every command (YAML; --json for JSON)
  taskmgr <command> --help  one command's flags, usage, and an example
  taskmgr show <id>         everything known about a single issue
`

var guideCmd = &cobra.Command{
	Use:   "guide",
	Short: "Print a short how-to for working with taskmgr (start here)",
	Long: `Print a compact, workflow-shaped how-to for taskmgr: the issue model, the
everyday command loop, the filter language in brief, and where to find more. It is
the prose companion to "taskmgr commands" (the machine catalog) and is emitted by
the binary, so it travels with the CLI.

Plain text to stdout; pass --json to wrap it as {"guide": "..."}.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagJSON {
			return printJSON(map[string]string{"guide": guideText})
		}
		fmt.Print(guideText)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(guideCmd)
}
