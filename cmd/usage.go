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
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// usageError marks a misinvocation — wrong positional args, a bad/unknown flag, or
// an unknown subcommand — as distinct from a runtime failure. Execute renders it as
// a compact, agent-friendly help block (purpose, usage, example, flags, and a
// pointer to --help) instead of cobra's bare one-liner. Runtime errors stay terse:
// the SDK already returns teaching messages, and burying those under usage text is
// exactly the noise we avoid.
type usageError struct {
	cmd *cobra.Command
	msg string
	// unknownFlag is the name, without dashes, of the flag the command does not
	// have; empty for every other kind of misuse.
	unknownFlag string
	// hint is one more line for the headline; Run sets it from the arguments.
	hint string
}

func (e *usageError) Error() string { return e.msg }

// installUsageErrors wires misuse handling across the whole command tree. Each
// command's positional-args validator is wrapped to emit a usageError, command
// groups are made to reject unknown subcommands (cobra otherwise prints help and
// exits 0), and the inherited flag-parse hook converts bad flags the same way.
func installUsageErrors(root *cobra.Command) {
	// FlagErrorFunc is inherited by subcommands, so setting it on the root covers
	// the whole tree.
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		ue := &usageError{cmd: cmd, msg: err.Error()}
		var unknown *pflag.NotExistError
		if errors.As(err, &unknown) {
			ue.unknownFlag = unknown.GetSpecifiedName()
		}
		return ue
	})

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Args != nil {
			inner := c.Args
			c.Args = func(cmd *cobra.Command, args []string) error {
				if err := inner(cmd, args); err != nil {
					return &usageError{cmd: cmd, msg: friendlyArgsMsg(cmd, args, err)}
				}
				return nil
			}
		}
		// A command group (subcommands, no action of its own) otherwise prints help
		// and exits 0 on an unknown subcommand — a silent no-op. Give it a dispatcher
		// that errors with a suggestion instead. The root is left alone: cobra already
		// gives a concise "unknown command … Did you mean?" for top-level typos.
		if c.HasParent() && c.HasAvailableSubCommands() && !c.Runnable() {
			// SuggestionsFor reads SuggestionsMinimumDistance directly (default 0);
			// cobra's own unknown-command path lazily defaults it to 2. Set it once
			// here, where the dispatcher is installed, so requireSubcommand's
			// near-miss suggestions match cobra's top-level behaviour.
			if c.SuggestionsMinimumDistance <= 0 {
				c.SuggestionsMinimumDistance = 2
			}
			c.RunE = requireSubcommand
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// requireSubcommand is the action for a command group: bare invocation prints the
// group's help (exit 0); an unrecognised subcommand becomes a usageError naming it
// and suggesting the closest match.
func requireSubcommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	// SuggestionsMinimumDistance was primed when this dispatcher was installed (see
	// installUsageErrors), so SuggestionsFor returns near-misses here.
	msg := fmt.Sprintf("unknown subcommand %q for %q", args[0], cmd.CommandPath())
	if sugg := cmd.SuggestionsFor(args[0]); len(sugg) > 0 {
		msg += "\n\nDid you mean this?\n\t" + strings.Join(sugg, "\n\t")
	}
	return &usageError{cmd: cmd, msg: msg}
}

// friendlyArgsMsg turns cobra's terse positional-args error ("accepts 1 arg(s),
// received 0") into a message that names what the command expects, derived from the
// placeholders in its Use line ("show <id>" -> "show needs <id>"). It falls back to
// cobra's own message when it cannot do better.
func friendlyArgsMsg(cmd *cobra.Command, args []string, err error) string {
	req, opt := positionalPlaceholders(cmd)
	name := displayName(cmd)
	switch {
	case len(args) < len(req):
		return fmt.Sprintf("%s needs %s", name, strings.Join(req[len(args):], " "))
	case len(req)+len(opt) == 0 && len(args) > 0:
		return fmt.Sprintf("%s takes no arguments, got %d", name, len(args))
	case len(args) > len(req)+len(opt):
		return fmt.Sprintf("%s takes at most %d argument(s), got %d", name, len(req)+len(opt), len(args))
	}
	return err.Error()
}

// displayName is the command's path without the binary name ("dep add"), so a
// subcommand reads unambiguously in messages.
func displayName(cmd *cobra.Command) string {
	return strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")
}

// missingRequiredFlags returns the "--name" of each required flag on cmd that was
// not set. Cobra validates required flags after the args-validator and
// FlagErrorFunc hooks and reports them as a plain error, so Execute detects the
// case structurally — via the required annotation — rather than by matching
// cobra's English message (which would silently degrade if the wording changed).
// A non-empty result is unambiguous: cobra runs RunE only once required flags are
// satisfied, so any unset required flag means the run failed on that validation,
// not at runtime.
func missingRequiredFlags(cmd *cobra.Command) []string {
	if cmd == nil {
		return nil
	}
	var missing []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if _, req := f.Annotations[cobra.BashCompOneRequiredFlag]; req && !f.Changed {
			missing = append(missing, "--"+f.Name)
		}
	})
	return missing
}

// requiredFlagsMsg renders the missing required flags as a misuse headline.
func requiredFlagsMsg(missing []string) string {
	if len(missing) == 1 {
		return "missing required flag " + missing[0]
	}
	return "missing required flags " + strings.Join(missing, ", ")
}

// filterFieldOps is the filter fields (QUERY-SPEC §2) that get a hint when guessed
// as a flag, each with the operator the caller most likely meant. A date gets ">="
// because "==" compares one instant, which is never what "--created 2026-01-01"
// asks for. The map is a hand copy: cmd/ cannot import the engine's field table,
// and TestFilterFieldOps_MatchesTheEngineFields fails when the two differ.
var filterFieldOps = map[string]string{
	"status":   "==",
	"type":     "==",
	"priority": "==",
	"assignee": "==",
	"creator":  "==",
	"agent":    "==",
	"session":  "==",
	"parent":   "==",
	"label":    "==",
	"text":     "~",
	"created":  ">=",
	"updated":  ">=",
	"closed":   ">=",
}

// filterHint is the line that follows the headline when a command that filters
// through --query rejected a flag named after a filter field: the caller's own
// invocation, with every such flag and any -q it already carried folded into one
// -q expression. It is empty for any other misuse.
func filterHint(e *usageError, args []string) string {
	flags := e.cmd.Flags()
	if _, isField := filterFieldOps[e.unknownFlag]; !isField || flags.Lookup("query") == nil {
		return ""
	}
	return queryInvocation(flags, args)
}

// queryInvocation rewrites args, read as flags reads them, into the command that
// filters through -q. Everything that is not a filter field or the query stays
// where the caller put it.
func queryInvocation(flags *pflag.FlagSet, args []string) string {
	var (
		kept, fields, terms []string
		query               string
		queryAt             = -1
		hasAll, needsAll    bool
	)
	for i := 0; i < len(args); i++ {
		arg, first := args[i], i
		if arg == "--" {
			kept = append(kept, args[i:]...)
			break
		}
		var (
			flag                 *pflag.Flag
			name, stacked, value string
			hasValue             bool
		)
		switch {
		case strings.HasPrefix(arg, "--"):
			name, value, hasValue = strings.Cut(arg[2:], "=")
			flag = flags.Lookup(name)
		case strings.HasPrefix(arg, "-"):
			flag, stacked, value, hasValue = shorthandWithValue(flags, arg)
		}
		_, isField := filterFieldOps[name]
		guessed := flag == nil && isField
		takesNext := guessed || flag != nil && flag.NoOptDefVal == ""
		if takesNext && !hasValue && i+1 < len(args) && (!guessed || !endsGuessedValue(flags, args[i+1])) {
			i++
			value, hasValue = args[i], true
		}
		switch {
		case guessed:
			term, closedStatus := filterTerm(name, value, hasValue)
			terms = append(terms, term)
			needsAll = needsAll || closedStatus
			if !slices.Contains(fields, name) {
				fields = append(fields, name)
			}
		case flag != nil && flag.Name == "query":
			query = value
			if stacked != "" {
				kept = append(kept, "-"+stacked)
			}
		default:
			hasAll = hasAll || flag != nil && flag.Name == "all"
			kept = append(kept, args[first:i+1]...)
			continue
		}
		if queryAt < 0 {
			queryAt = len(kept)
		}
	}
	if len(terms) == 0 {
		return ""
	}

	expr := strings.Join(terms, " && ")
	if strings.Contains(query, "||") {
		query = "(" + query + ")"
	}
	if query != "" {
		expr = query + " && " + expr
	}
	filter := []string{"-q", expr}
	if needsAll && !hasAll {
		filter = []string{"--all", "-q", expr}
	}
	words := []string{"taskmgr"}
	for _, arg := range slices.Insert(kept, queryAt, filter...) {
		words = append(words, shellQuote(arg))
	}
	return fmt.Sprintf("To filter by %s: %s", strings.Join(fields, " and "), strings.Join(words, " "))
}

// shorthandWithValue reads a "-abc" group as pflag does: shorthands that take no
// value stack, and the first one that takes a value owns the rest of the group,
// or the next argument when nothing is left. It returns that flag and the
// shorthands stacked before it; flag is nil when the group has none.
func shorthandWithValue(flags *pflag.FlagSet, group string) (flag *pflag.Flag, stacked, value string, hasValue bool) {
	for i := 1; i < len(group); i++ {
		f := flags.ShorthandLookup(group[i : i+1])
		if f == nil || f.NoOptDefVal != "" {
			continue
		}
		rest := group[i+1:]
		if len(rest) > 1 {
			rest = strings.TrimPrefix(rest, "=")
		}
		return f, group[1:i], rest, rest != ""
	}
	return nil, "", "", false
}

// endsGuessedValue reports whether arg, the argument after a guessed "--field",
// is the next flag and not the field's value. pflag would take any argument as
// the value of a flag it knows; for a flag it does not know, only something the
// caller clearly meant as a flag ends it — "--", a flag of the command, or another
// filter field — so a value that merely starts with a dash ("--label -wontfix") is
// still read as the value.
func endsGuessedValue(flags *pflag.FlagSet, arg string) bool {
	if long, isLong := strings.CutPrefix(arg, "--"); isLong {
		name, _, _ := strings.Cut(long, "=")
		_, isField := filterFieldOps[name]
		return name == "" || isField || flags.Lookup(name) != nil
	}
	return len(arg) > 1 && arg[0] == '-' && flags.ShorthandLookup(arg[1:2]) != nil
}

// filterTerm is the predicate for one guessed "--field value". A field without a
// value gets a placeholder that shows the form the value takes, except "--closed":
// its caller wants the closed issues, which is a status and needs the cold
// partition — closedStatus tells the caller to add --all.
func filterTerm(field, value string, given bool) (term string, closedStatus bool) {
	switch field {
	case "priority":
		n, err := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(value), "P"))
		if err != nil || n < 0 {
			return "priority == <0-4>", false
		}
		return "priority == " + strconv.Itoa(n), false
	case "created", "updated", "closed":
		if value == "" && field == "closed" {
			term, _ = filterTerm("status", string(tasks.StatusClosed), true)
			return term, true
		}
		if value == "" {
			value = "<YYYY-MM-DD>"
		}
	default:
		if !given {
			value = "<value>"
		}
	}
	if expr, err := fieldCriteria(field, value).Build(); err == nil && expr != "" {
		return expr, false
	}
	return field + " " + filterFieldOps[field] + " " + quoteFilterValue(value), false
}

// fieldCriteria is the Criteria that selects on one field, for the fields
// Criteria.Build can express as "--field value" means them. It is the zero value
// for the rest: priority, which Build only bounds, and the dates, which Build
// rewrites to a full timestamp.
func fieldCriteria(field, value string) tasks.Criteria {
	switch field {
	case "status":
		return tasks.Criteria{Statuses: []tasks.Status{tasks.Status(value)}}
	case "type":
		return tasks.Criteria{Types: []tasks.Type{tasks.Type(value)}}
	case "label":
		return tasks.Criteria{Labels: []string{value}}
	case "assignee":
		return tasks.Criteria{Assignee: value}
	case "creator":
		return tasks.Criteria{Creator: value}
	case "agent":
		return tasks.Criteria{Agent: value}
	case "session":
		return tasks.Criteria{Session: value}
	case "parent":
		return tasks.Criteria{Parent: &value}
	case "text":
		return tasks.Criteria{Text: value}
	}
	return tasks.Criteria{}
}

// quoteFilterValue quotes a value as a QUERY-SPEC §3 string, for the predicates
// Criteria.Build declines: a date, an empty value, and a status or type the engine
// does not know, which is printed as typed so that running it returns the engine's
// own error.
func quoteFilterValue(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

// shellQuote returns arg as one POSIX shell word, quoted only when it needs it.
func shellQuote(arg string) string {
	if arg != "" && strings.Trim(arg, shellSafe) == "" {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

const shellSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-"

// positionalPlaceholders splits a command's Use line into its required (<...>) and
// optional ([...]) positional placeholders, skipping the command word itself.
func positionalPlaceholders(cmd *cobra.Command) (req, opt []string) {
	toks := strings.Fields(cmd.Use)
	if len(toks) > 0 {
		toks = toks[1:]
	}
	for _, t := range toks {
		switch {
		case strings.HasPrefix(t, "<"):
			req = append(req, t)
		case strings.HasPrefix(t, "["):
			opt = append(opt, t)
		}
	}
	return req, opt
}

// renderUsageError prints the compact misuse-help block to stderr. It mirrors the
// discipline of runtime errors (stderr, "taskmgr:" prefix, nothing on stdout): the
// error, a one-line purpose, the usage and a synthesised example, the command's own
// flags (or, for a group, its subcommands), and a pointer to --help.
func renderUsageError(e *usageError) {
	c := e.cmd
	var b strings.Builder
	fmt.Fprintf(&b, "taskmgr: %s\n", e.msg)
	if e.hint != "" {
		fmt.Fprintf(&b, "%s\n", e.hint)
	}
	b.WriteString("\n")
	if short := strings.TrimSpace(c.Short); short != "" {
		fmt.Fprintf(&b, "%s\n\n", short)
	}
	fmt.Fprintf(&b, "usage:   %s\n", c.UseLine())
	fmt.Fprintf(&b, "example: %s\n", exampleFor(c))

	if subs := subcommandLines(c); len(subs) > 0 {
		b.WriteString("\nsubcommands:\n")
		for _, line := range subs {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	} else if flags := localFlagLines(c); len(flags) > 0 {
		b.WriteString("\nflags:\n")
		for _, line := range flags {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}

	fmt.Fprintf(&b, "\nRun '%s --help' for the full help.\n", c.CommandPath())
	_, _ = fmt.Fprint(stderr, b.String())
}

// localFlagLines formats a command's own (non-inherited) flags for the help block,
// skipping the auto-added -h/--help and any hidden flags.
func localFlagLines(c *cobra.Command) []string {
	var lines []string
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		name := "--" + f.Name
		if f.Shorthand != "" {
			name = "-" + f.Shorthand + ", " + name
		}
		lines = append(lines, fmt.Sprintf("%-20s %s", name, f.Usage))
	})
	return lines
}

// subcommandLines lists a group's available subcommands for the help block.
func subcommandLines(c *cobra.Command) []string {
	var lines []string
	for _, sub := range c.Commands() {
		if sub.Hidden || !sub.IsAvailableCommand() || sub.Name() == "help" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%-12s %s", sub.Name(), sub.Short))
	}
	return lines
}
