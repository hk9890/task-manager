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

// guideSections is the guide, in print order. A topic is one **job**, not one
// concept, and that is the whole of its design: the reader arrives knowing what
// it is about to do and not which of this tool's ideas that touches, so a roster
// of concepts costs it a translation step and, measurably, a second fetch to
// correct the guess. One job is therefore one command, and the answer it returns
// is sufficient — a caller filing an issue should never need a second topic.
//
// The cost of that is duplication: filing and progress both explain the body,
// because both need it. That is a maintenance cost paid once by whoever edits
// this file, in place of a token cost paid by every caller on every run.
var guideSections = []guideSection{
	{
		id:      "filing",
		summary: "file or edit an issue: types, priorities, edges, and the body",
		text: `## Filing an issue

Only --title is required.

  taskmgr create --title "Add export endpoint" --type feature --priority 1

  type      task (default) · bug · feature · epic · chore · doc
  priority  0 critical · 1 high · 2 normal (default) · 3 low · 4 trivial
  status    open (default) · in_progress · blocked · deferred · closed

Three edges place an issue in the graph. Set them here when the other issue
already exists, or add them later (taskmgr guide progress):

  --parent <epic-id>    grouping under an epic (one parent per issue)
  --blocked-by <id>     a hard dependency, repeatable, enforced acyclic
  --related <id>        a non-blocking, symmetric link

IDs are opaque (e.g. rep-fev72z), not sequential. There is no way to derive or
guess one, so capture it from --json and reuse it:

  id=$(taskmgr create --title "Schema" --type task --json | jq -r .id)

Filing a set that depends on itself: create in dependency order, because an id
does not exist until its issue does.

## The description body

Each issue has one Markdown description body. Acceptance criteria, instructions
and context all go in it — there is no separate field for any of them.

--description takes one inline string, which is fine for a single line. For a
multi-line body use --description-file, which reads a path, or "-" for stdin:

  taskmgr create --title "Schema" --type task --description-file - <<'EOF'
  ## Acceptance criteria
  - [ ] UTF-8 with BOM
  EOF

  taskmgr create --title "Schema" --description-file notes.md   # ...or a file

Do not rely on --description "a\nb" — the \n is stored literally, as two
characters. Use --description-file - or, inline, $'a\nb' ANSI-C quoting.

A body of any size is accepted; a large one is stored beside the issue rather
than in it. show truncates a long body and says so — use --json for all of it.

A document (type doc) is not work and never appears in ready or blocked. Use one
for a design page, session notes or a handover, and say which kind in a label:

  taskmgr create --title "Auth redesign" --type doc --label kind:design \
      --description-file page.md
`,
	},
	{
		id:      "finding",
		summary: "find what to work on: the ready and blocked views, and filters",
		text: `## Finding work

Two views are derived from the dependency graph, not from the status field:

  taskmgr ready     open issues with no open blockers — what you can start now
  taskmgr blocked   non-closed issues waiting on at least one open blocker
  taskmgr show <id> full detail: fields, edges, description, comments

blocked is not the same as status == "blocked". An issue can be open and yet
blocked, or carry the blocked status with no open blocker at all — the status is
a label a person set, and the view is what the graph says. Epics appear in ready
too; add type != epic for leaf tasks only. Documents appear in neither.

## Filters

taskmgr list -q '<expr>' selects issues with <field> <op> <value> predicates
joined by && || ! and parentheses:

  taskmgr list -q 'status == "open" && priority <= 1'
  taskmgr list -q 'type == bug && label ~ "area:reports"'
  taskmgr list -q 'ready && priority <= 2'
  taskmgr list --all -q 'closed > "2026-01-01"'
  taskmgr search export          # shorthand for: list -q 'text ~ "export"'
  taskmgr search drill nav       # every word must match

Fields:    status, type, priority, assignee, creator, parent, label,
           text (id/title/description), created, updated, closed,
           and the booleans ready / blocked
Operators: == != < <= > >= and ~ (case-insensitive substring)
Values:    quote strings ("open"); numbers and dates are bare or quoted;
           quote multi-word values — text ~ "drill nav", not text ~ drill nav

~ matches a substring, not a whole word: text ~ "rate" also matches "separate".
Closed issues are excluded unless the expression selects them or you pass --all.
taskmgr labels / statuses / types list the values actually in use.
`,
	},
	{
		id:      "progress",
		summary: "record progress, wire edges, and close an issue",
		text: `## Recording progress

  taskmgr update <id> --status in_progress
  taskmgr comment add <id> "Chose ISO-8601 to match the reports module."
  echo "scaffold pushed" | taskmgr comment add <id> --file -
  taskmgr close <id> --reason "shipped in <commit>"

Prefer close --reason over update --status closed: close stamps the close time
and moves the issue to the cold partition, and the reason is what explains the
history to whoever reads it next.

Closing an issue is what releases the ones it was blocking, so run taskmgr ready
again afterwards to see what opened up.

## Editing an issue that already exists

update --description replaces the body — it does not append. To amend one, run
show, take the text, and resubmit the whole modified body:

  taskmgr show <id> --json | jq -r .description   # ...edit, then resubmit
  taskmgr update <id> --description-file -

A mutation's --json echoes the issue's scalar fields, but not the description and
not the comments; run show to confirm those landed.

Labels are edited by name, not by rewriting the set:

  taskmgr update <id> --add-label area:reports --remove-label needs-triage

## Wiring edges after the fact

  taskmgr dep add <dependent> <blocker>   # dependent becomes blocked by blocker
  taskmgr rel add <a> <b>                 # symmetric related link
  taskmgr update <id> --parent <epic-id>
`,
	},
	{
		id:      "scripting",
		summary: "--json, exit codes, and choosing which store to act on",
		text: `## Output and exit conventions

Add --json to any command for stable snake_case output — parse that, never
scrape the human table. Exit 0 on success, non-zero on error; the message goes to
stderr prefixed "taskmgr:" and names the offending field and the allowed values.

## Which store a command acts on

taskmgr acts on the project you run it from. It never fails on this, so ask:

  taskmgr where                    which store resolves here, and why
  taskmgr -C <path> ready          act on a project elsewhere
  taskmgr --store-name <name> ready   act on a central store by its registry name
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

// guideOverviewHead is the fixed opening of the overview. It is two lines
// because everything else it could say is a line the caller pays for and cannot
// act on: what an issue tracker is, what this one is called, and what it is for
// are all things the reader either knows or does not need.
const guideOverviewHead = `taskmgr — file, find and finish issues from this CLI.
Pick the job, run its command, then act.

`

// guideOverviewTail closes the overview with the surfaces that are not jobs.
const guideOverviewTail = `
  taskmgr commands          every command and flag, as a catalog
  taskmgr <command> --help  one command
  taskmgr guide --list      every topic, as data (--json)
`

// renderGuideOverview builds what an unqualified `taskmgr guide` prints: the job
// list, and nothing else.
//
// It is a **router**, not a summary. Measurement is what decided that: the
// guide's own prose is about 2% of a session's tokens, while an agent that has
// to work out which parts it needs spends roughly a quarter of the session
// finding out. So the overview spends its lines on routing and none on teaching,
// and every caller pays for it on every run.
//
// A package reaches this output in two ways, both generated. A fragment placed
// into a built-in job marks that job's line, so a caller learns the store adds
// rules at the moment it is choosing — without paying for the rules themselves,
// which it is about to fetch anyway. A fragment the package owns outright gets
// its own line, because a topic no line names is a topic nothing can reach.
func renderGuideOverview(topics []tasks.GuideTopic) string {
	var b strings.Builder
	b.WriteString(guideOverviewHead)

	into := make(map[string][]string)
	for _, t := range topics {
		if t.Overview || t.Into == "" {
			continue
		}
		into[t.Into] = append(into[t.Into], t.Package)
	}

	w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	for _, s := range guideSections {
		summary := s.summary
		if pkgs := uniqueStrings(into[s.id]); len(pkgs) > 0 {
			summary += fmt.Sprintf(" — this store adds rules (%s)", strings.Join(pkgs, ", "))
		}
		_, _ = fmt.Fprintf(w, "  taskmgr guide %s\t%s\n", s.id, summary)
	}
	_ = w.Flush()

	// A package's own topic is a job this store has and the tool does not, so it
	// belongs in the same list rather than behind a second command. It gets its
	// own column block: an effective topic id is three times the width of a job
	// id, and sharing a block would indent every job line to match it.
	pw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	for _, t := range topics {
		if t.Overview || t.Into != "" {
			continue
		}
		_, _ = fmt.Fprintf(pw, "  taskmgr guide %s\t%s\n", t.ID, guidePackageSummary(t))
	}
	_ = pw.Flush()

	// An `into:` naming a job that does not exist is reported, not hidden and not
	// fatal: the fragment is still reachable by its own id, and saying so here is
	// what tells the package's author that the placement was silently dropped.
	if orphans := guideOrphans(topics); len(orphans) > 0 {
		b.WriteString("\n")
		for _, t := range orphans {
			fmt.Fprintf(&b, "  (%s asks to print inside %q, which is not a topic here — fetch it by name)\n", t.ID, t.Into)
		}
	}

	// The `overview:` fragment still reaches every caller. Placing a rule into a
	// job is the better tool for one that only matters to that job — it arrives
	// with the job, and costs nothing to a caller doing something else — but a
	// store with a rule that governs *every* command has no job to hang it on,
	// and this is where that goes. The 1 KiB cap is what keeps it affordable.
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
			// A cut fragment is marked wherever it is printed (HOOK-SPEC §3.7):
			// this text goes verbatim into a caller's context, and half a rule
			// that reads as a whole one is worse than a rule the reader knows is
			// incomplete. The overview cap is the tight one, so this is the path
			// that cuts most often.
			if t.Truncated {
				fmt.Fprintf(&b, "  (cut at %d bytes — the whole fragment is %s)\n", guideCap(t), t.Path)
			}
		}
	}

	b.WriteString(guideOverviewTail)
	return b.String()
}

// guidePackageSummary is the roster line for a topic a package owns outright.
func guidePackageSummary(t tasks.GuideTopic) string {
	if t.Detail != "" {
		return fmt.Sprintf("from package %s (unreadable: %s)", t.Package, t.Detail)
	}
	return fmt.Sprintf("from package %s", t.Package)
}

// guideOrphans lists the fragments whose `into:` names no built-in topic.
func guideOrphans(topics []tasks.GuideTopic) []tasks.GuideTopic {
	var out []tasks.GuideTopic
	for _, t := range topics {
		if t.Into == "" {
			continue
		}
		if _, ok := coreSection(t.Into); !ok {
			out = append(out, t)
		}
	}
	return out
}

// uniqueStrings removes duplicates while keeping first-seen order.
func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
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
			fmt.Fprintf(&b, "\n(truncated at %d bytes — the whole file is %s)\n", guideCap(t), t.Path)
		}
	}
	return b.String()
}

// guideCap is the byte cap the engine applied to one fragment. An overview is
// held to the tighter of the two, so naming the topic cap for it was wrong by a
// factor of eight: an author whose overview was being cut read "8192", concluded
// the cap was not what cut it, and trimmed to just under a limit that was never
// the one in force.
func guideCap(t tasks.GuideTopic) int {
	if t.Overview {
		return tasks.MaxGuideOverviewBytes
	}
	return tasks.MaxGuideFragmentBytes
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
	Into    string `json:"into,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

var guideCmd = &cobra.Command{
	Use:   "guide [topic...]",
	Short: "Print a short how-to for working with taskmgr (start here)",
	Long: `Print a how-to for taskmgr, in parts, one part per job: filing an issue,
finding work, recording progress, and reading output in a script. It is the prose
companion to "taskmgr commands" (the machine catalog) and is emitted by the
binary, so it travels with the CLI.

With no arguments it prints the job list — which job maps to which command, and
nothing else. Name a job to print it: one job is one command, and what it prints
is meant to be sufficient, so the rules this store's packages add to that job
print with it. --list gives every topic, the package ones included, and the topic
"packages" selects every package-contributed section at once.

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
				// caller — reported as one, at the moment it is written. The
				// suggestion is what makes that cheap to act on: the names that
				// get guessed are the ones this tool uses elsewhere, so a caller
				// reaching for a command name lands here with a near miss.
				return "", fmt.Errorf("unknown guide topic %q: %srun taskmgr guide for the job list, or taskmgr guide --list for every topic",
					name, guideSuggestion(name))
			}
			// A job is one command, and its answer has to be sufficient. So a
			// package's rules for this job print here, after the built-in text —
			// not behind a second topic the caller had no reason to know about.
			parts = append(parts, s.text)
			for _, t := range topics {
				if t.Into == name {
					parts = append(parts, renderGuideTopic(t))
				}
			}
		}
	}
	return strings.Join(parts, "\n"), nil
}

// guideAliases maps a name a caller is likely to reach for onto the job that now
// holds it. Two kinds are in here, and both were observed rather than imagined:
// the five concept ids this guide carried before jobs replaced them, and the
// command names that read like topics — an agent wanting to know what to work on
// guessed "ready", which is a command and a query field but never was a topic.
var guideAliases = map[string]string{
	"model":  "filing",
	"loop":   "filing",
	"body":   "filing",
	"query":  "finding",
	"output": "scripting",

	"create":  "filing",
	"file":    "filing",
	"ready":   "finding",
	"blocked": "finding",
	"list":    "finding",
	"search":  "finding",
	"update":  "progress",
	"close":   "progress",
	"comment": "progress",
	"json":    "scripting",
	"store":   "scripting",
}

// guideSuggestion names the topic a mistaken one probably meant, or "" when
// nothing is close enough to be worth printing. A wrong suggestion costs the
// reader a wasted fetch, so this stays deliberately literal: a known alias, or a
// job whose id starts with what was typed.
func guideSuggestion(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if to, ok := guideAliases[n]; ok {
		return fmt.Sprintf("did you mean %q? ", to)
	}
	if n != "" {
		for _, s := range guideSections {
			if strings.HasPrefix(s.id, n) {
				return fmt.Sprintf("did you mean %q? ", s.id)
			}
		}
	}
	return ""
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
		row := guideTopicDTO{ID: t.ID, Kind: "package", Package: t.Package, Scope: t.Scope, Into: t.Into, Detail: t.Detail}
		row.Summary = fmt.Sprintf("contributed by package %s", t.Package)
		if t.Overview {
			// Say that this one arrives on its own: a caller that already has the
			// overview has already read it, and does not need to spend a command.
			row.Kind = "overview"
			row.Summary = fmt.Sprintf("package %s, already printed in the overview", t.Package)
		}
		if t.Into != "" {
			// Where a fragment prints is the thing a caller needs from this row:
			// one placed into a job arrives with that job, so asking for it by id
			// is a command it did not have to spend.
			if _, ok := coreSection(t.Into); ok {
				row.Summary = fmt.Sprintf("package %s, printed inside %q", t.Package, t.Into)
			} else {
				row.Summary = fmt.Sprintf("package %s, asks for %q which is not a topic here", t.Package, t.Into)
			}
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
