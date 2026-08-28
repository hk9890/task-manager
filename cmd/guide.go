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
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// guide.go — `taskmgr guide`: the canonical, binary-owned how-to. It is the prose
// sibling of `commands` (the machine catalog): different jobs, both kept.
//
// The primary reader is an agent, not a person — a person has docs/user-guide/ —
// and an agent does not read this in a terminal. It pastes the output into its
// own instructions before it does anything, so two properties are load-bearing:
//
//   - The command must never fail. A non-zero exit is what a caller injecting
//     this output sees as "abort"; a guide that failed because a store did not
//     resolve, or because someone else's package is broken, would take down every
//     caller on the machine. Nothing here returns an error but a usage mistake.
//   - Every line is paid for on every invocation. That is what topics are for: a
//     caller that needs the filter language asks for it, and one that needs the
//     everyday loop does not pay for the rest. The bare command therefore prints
//     the *overview* — the roster and where to go next — and never the whole
//     guide, so what every caller pays is a constant a new section does not move.
//
// The core sections below are hand-maintained. guide_test.go guards them against
// the SDK — the model lists, and the flags the prose promises — so they cannot
// silently fall out of step; keep the rest honest by hand when the CLI changes.
//
// Packages contribute two things (HOOK-SPEC §3.7). A `guide:` section is fetched
// by name, like a core one. An `overview:` fragment goes into the overview itself,
// so a project states a convention to a caller that has not asked a question yet —
// which is the only moment a rule can be learned before it is broken. Its cap is
// an order of magnitude tighter for exactly that reason.
//
// Plain text on purpose; no backticks, so every section can live in a raw string
// literal.

// guideSection is one core topic of the guide: the id a caller names to select
// it, a one-line summary for `--list`, and the prose itself.
type guideSection struct {
	id      string
	summary string
	text    string
}

// guideSections is the guide, in print order. The ids are the caller's stable
// handle on a part of it, so they are named after what the part holds.
var guideSections = []guideSection{
	{
		id:      "model",
		summary: "types, statuses, priorities, the three edges, ready and blocked",
		text: `## The model

Each issue has a type, a status, and a numeric priority:

  type      task (default) · bug · feature · epic · chore · doc
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

Documents (type doc) never appear in either view — they are not work. They are
otherwise ordinary issues: listed, searchable, closable, and linkable with
related. Use them for design pages, session notes, handovers and reviews, and
say which kind in a label (kind:design, kind:session, ...):

  taskmgr create --title "Auth redesign" --type doc \
      --label kind:design --description-file page.html
  taskmgr rel add <doc-id> <task-id>

A body of any size is accepted; large ones are stored beside the issue rather
than in it. show truncates a long body and says so — use --json when you need
all of it.

IDs are opaque (e.g. rep-fev72z), not sequential. There is no way to derive one,
so capture it from --json and reuse it:

  id=$(taskmgr create --title "Schema" --type task --json | jq -r .id)
`,
	},
	{
		id:      "loop",
		summary: "create, find work, record progress, wire relationships",
		text: `## The core loop

  # Create — only --title is required.
  taskmgr create --title "Add export endpoint" --type feature --priority 1

  # Create with its place in the graph already set. --blocked-by and --label
  # repeat; --parent takes one epic. Every id must already exist.
  taskmgr create --title "Export UI" --type feature \
      --parent <epic-id> --blocked-by <api-id> --label area:reports

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

Filing a set that depends on itself: create in dependency order, because an id
does not exist until its issue does. Take each id from --json (see "The model"),
pass it to the next create as --parent or --blocked-by, and use dep add for an
edge you only discover later.
`,
	},
	{
		id:      "body",
		summary: "the one Markdown body, and how to write a multi-line one",
		text: `## The description body

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
`,
	},
	{
		id:      "query",
		summary: "the filter language: fields, operators, and what ~ matches",
		text: `## Finding work with filters

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
`,
	},
	{
		id:      "output",
		summary: "--json, exit codes, and where an error message goes",
		text: `## Output and exit conventions

Add --json to any command for stable snake_case output — parse that, do not
scrape the human table. Exit 0 on success, non-zero on error; the message goes to
stderr prefixed "taskmgr:" and names the offending field and the allowed values.
`,
	},
}

// guidePackagesTopic selects every package-contributed section at once, for a
// caller that wants this store's conventions without the mechanics it already
// has.
const guidePackagesTopic = "packages"

// guideText is every word the guide can print, in section order. Nothing prints
// it — an unqualified `taskmgr guide` prints the overview — and it exists so the
// drift guards in guide_test.go can hold the whole corpus against the SDK and the
// live command tree at once.
var guideText = renderGuideSections(guideSections)

// renderGuideSections joins sections with one blank line between them. Each
// section's own text ends with a newline, so the join adds exactly the separator.
func renderGuideSections(sections []guideSection) string {
	parts := make([]string, 0, len(sections))
	for _, s := range sections {
		parts = append(parts, s.text)
	}
	return strings.Join(parts, "\n")
}

// guideOverviewHead is the fixed opening of the overview: what the tool is, and
// the instruction to fetch before acting. The roster below it is generated, so a
// section cannot be added without appearing here.
const guideOverviewHead = `taskmgr — how to use it

taskmgr is an issue tracker you drive entirely through this CLI: create issues,
link them, find what is ready to work on, record progress. It acts on the project
you run it from — taskmgr where reports which store that is, and -C <path> targets
a project elsewhere.

This is the overview. The guide itself is in parts, and each part is one command:
`

// guideOverviewTail closes the overview: the fetch-before-you-act table, the
// reason it is worth the round trip, and where the rest of the surface lives.
//
// The table is imperative on purpose. An overview that only lists its parts is a
// menu, and a caller under load skims a menu and proceeds on what it already
// believes it knows — which for this tool is wrong in three specific ways it
// cannot discover by guessing. Naming the command to run for each intent is what
// makes an index behave like an instruction.
const guideOverviewTail = `
Name several at once: taskmgr guide model loop.

Read before you act:

  filing or editing an issue    taskmgr guide model loop body packages
  finding work to pick up       taskmgr guide model query
  parsing output in a script    taskmgr guide output

Guessing instead of reading is what this guide exists to prevent, and three of
these cost a wasted attempt every time: IDs are opaque and cannot be derived,
--description stores a literal backslash-n, and this store can refuse a write for
reasons only its own sections state.

More:

  taskmgr commands          machine catalog of every command (YAML; --json for JSON)
  taskmgr <command> --help  one command's flags, usage, and an example
  taskmgr guide --list      this roster as data (--json for JSON)
`

// renderGuideOverview builds what an unqualified `taskmgr guide` prints: the
// fixed head, the generated roster of core sections, the roster line for this
// store's package sections, whatever those packages put in the overview itself,
// and the fixed tail.
//
// The package half is why this is generated rather than written out. A caller
// injecting the overview has to learn that this store expects something *and*
// which command states it, and only the store can say that.
func renderGuideOverview(topics []tasks.GuideTopic) string {
	var b strings.Builder
	b.WriteString(guideOverviewHead)

	w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	for _, s := range guideSections {
		_, _ = fmt.Fprintf(w, "  taskmgr guide %s\t%s\n", s.id, s.summary)
	}
	_, _ = fmt.Fprintf(w, "  taskmgr guide %s\t%s\n", guidePackagesTopic, packagesSummary(topics))
	_ = w.Flush()

	// A package's overview fragment is printed whole, under the package's name.
	// It is capped an order of magnitude tighter than a section (SDK
	// MaxGuideOverviewBytes) precisely so it can appear here in every caller's
	// context without crowding out the mechanics above.
	for _, t := range topics {
		if !t.Overview {
			continue
		}
		fmt.Fprintf(&b, "\nWhat %s expects of this store", t.Package)
		if t.Scope != "" {
			fmt.Fprintf(&b, " (%s)", t.Scope)
		}
		b.WriteString(":\n\n")
		switch {
		case t.Detail != "":
			fmt.Fprintf(&b, "  (this package's overview could not be read: %s)\n", t.Detail)
		default:
			b.WriteString(indentLines(strings.TrimRight(t.Text, "\n"), "  "))
			b.WriteString("\n")
		}
	}

	b.WriteString(guideOverviewTail)
	return b.String()
}

// packagesSummary describes the `packages` roster line for this store, so a
// caller can tell whether the topic is worth a command before spending one.
func packagesSummary(topics []tasks.GuideTopic) string {
	switch n := len(topics); n {
	case 0:
		return "what this store expects on top of the above (nothing here)"
	case 1:
		return "what this store expects on top of the above (1 section here)"
	default:
		return fmt.Sprintf("what this store expects on top of the above (%d sections here)", n)
	}
}

// indentLines prefixes every line of s, so a package's overview text sits under
// its heading as one visibly quoted block rather than merging into the guide's
// own prose.
func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// renderGuideTopic renders one package fragment with a heading that names where
// it came from. The provenance is not decoration: the reader has to be able to
// tell a convention this store adds from the mechanics the tool guarantees, and
// the heading is the only thing that says which it is holding.
func renderGuideTopic(t tasks.GuideTopic) string {
	var b strings.Builder
	scope := t.Scope
	if scope == "" {
		scope = "store"
	}
	fmt.Fprintf(&b, "## %s\n\nFrom package %s (%s). This is a convention of this store, not a rule of taskmgr.\n",
		t.ID, t.Package, scope)
	switch {
	case t.Detail != "":
		fmt.Fprintf(&b, "\n(this topic could not be read: %s)\n", t.Detail)
	default:
		b.WriteString("\n")
		b.WriteString(t.Text)
		if !strings.HasSuffix(t.Text, "\n") {
			b.WriteString("\n")
		}
		if t.Truncated {
			fmt.Fprintf(&b, "\n(truncated at %d bytes — the whole file is %s)\n", tasks.MaxGuideFragmentBytes, t.Path)
		}
	}
	return b.String()
}

// guideFlags holds this command's own flags.
var guideFlags struct{ list bool }

// guideTopicDTO is one row of `guide --list` (CLI-SPEC §6).
type guideTopicDTO struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Summary string `json:"summary,omitempty"`
	Package string `json:"package,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

var guideCmd = &cobra.Command{
	Use:   "guide [topic...]",
	Short: "Print a short how-to for working with taskmgr (start here)",
	Long: `Print a compact, workflow-shaped how-to for taskmgr: the issue model, the
everyday command loop, the filter language in brief, and where to find more. It is
the prose companion to "taskmgr commands" (the machine catalog) and is emitted by
the binary, so it travels with the CLI.

With no arguments it prints everything, including any sections the packages this
store uses contribute. Name one or more topics to print only those, and --list to
see what the topics are. The topic "packages" selects every package-contributed
section at once.

This command never fails on the state of the machine: no store, an uninstalled
package, an unreadable section — each is reported in the output and exits 0, so a
caller that pastes this output into a script or a prompt is never left with
nothing. Only naming a topic that does not exist is an error.

Plain text to stdout; pass --json to wrap it as {"guide": "..."}, or with --list
to get the topics as an array.`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		topics := packageGuideTopics()
		if guideFlags.list {
			return runGuideList(topics)
		}
		text, err := guideOutput(args, topics)
		if err != nil {
			return err
		}
		if flagJSON {
			return printJSON(map[string]string{"guide": text})
		}
		_, _ = fmt.Fprint(stdout, text)
		return nil
	},
}

// guideOutput assembles the requested guide text.
//
// With no arguments it is the **overview** — what the tool is, the roster of
// parts, and the command that fetches each. Never the whole guide: the reader
// injects this output into its own instructions, and a caller that needs the
// filter language and a caller that needs the filing loop should not each pay for
// the other's sections. The overview is the constant, small thing every caller
// can afford, and it names what to fetch next.
//
// With arguments it is exactly the topics named, in the order they were named,
// so a caller composes the slice it wants rather than taking the order here.
func guideOutput(args []string, topics []tasks.GuideTopic) (string, error) {
	if len(args) == 0 {
		return renderGuideOverview(topics), nil
	}

	byID := make(map[string]tasks.GuideTopic, len(topics))
	for _, t := range topics {
		byID[t.ID] = t
	}
	var parts []string
	for _, name := range args {
		switch {
		case name == guidePackagesTopic:
			for _, t := range topics {
				parts = append(parts, renderGuideTopic(t))
			}
			if len(topics) == 0 {
				parts = append(parts, "(no package contributes a guide section here)\n")
			}
		case strings.HasPrefix(name, "pkg:"):
			// A package topic that is not here is reported, not refused. Whether
			// a package is installed is a property of the machine, and a caller
			// that names one must not be left with a failed command because a
			// colleague has not installed it yet.
			t, ok := byID[name]
			if !ok {
				parts = append(parts, fmt.Sprintf("(topic %s is not available here — run taskmgr package list to see which packages apply)\n", name))
				continue
			}
			parts = append(parts, renderGuideTopic(t))
		default:
			s, ok := coreSection(name)
			if !ok {
				// A core topic that does not exist can never be made to exist by
				// installing anything, so it is only ever a mistake in the
				// caller — reported as one, at the moment it is written.
				return "", fmt.Errorf("unknown guide topic %q: run taskmgr guide --list to see the topics", name)
			}
			parts = append(parts, s.text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

// coreSection finds a core section by id.
func coreSection(id string) (guideSection, bool) {
	for _, s := range guideSections {
		if s.id == id {
			return s, true
		}
	}
	return guideSection{}, false
}

// runGuideList prints the topic roster: the core sections, then the ones packages
// contribute. It is the discovery surface a caller reads before deciding which
// parts of the guide it wants, so it stays small enough to be worth asking for.
func runGuideList(topics []tasks.GuideTopic) error {
	out := make([]guideTopicDTO, 0, len(guideSections)+len(topics)+1)
	for _, s := range guideSections {
		out = append(out, guideTopicDTO{ID: s.id, Kind: "core", Summary: s.summary})
	}
	out = append(out, guideTopicDTO{
		ID:      guidePackagesTopic,
		Kind:    "core",
		Summary: fmt.Sprintf("every package section at once (%d here)", len(topics)),
	})
	for _, t := range topics {
		row := guideTopicDTO{ID: t.ID, Kind: "package", Package: t.Package, Scope: t.Scope, Detail: t.Detail}
		row.Summary = fmt.Sprintf("contributed by package %s", t.Package)
		if t.Overview {
			// Say that this one arrives on its own: a caller that already has the
			// overview has already read it, and does not need to spend a command.
			row.Kind = "overview"
			row.Summary = fmt.Sprintf("package %s, already printed in the overview", t.Package)
		}
		if t.Detail != "" {
			row.Summary = "unreadable"
		}
		out = append(out, row)
	}
	if flagJSON {
		return printJSON(out)
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "TOPIC\tKIND\tCONTENTS")
	for _, r := range out {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", r.ID, r.Kind, r.Summary)
	}
	return w.Flush()
}

// packageGuideTopics reads the guide sections the packages of this machine and
// this store contribute, and swallows every failure by design.
//
// A store that does not resolve is the ordinary case for this command — an agent
// runs the guide before it knows where it is standing — and then the per-user
// config is still worth reading on its own. Neither path returns an error to the
// caller: see the file comment on why this command may not fail.
func packageGuideTopics() []tasks.GuideTopic {
	if s, err := openStore(); err == nil {
		if ts, err := s.GuideTopics(); err == nil {
			return ts
		}
		return nil
	}
	ts, err := tasks.GlobalGuideTopics()
	if err != nil {
		return nil
	}
	return ts
}

func init() {
	guideCmd.Flags().BoolVar(&guideFlags.list, "list", false, "List the guide's topics instead of printing it")
	rootCmd.AddCommand(guideCmd)
}
