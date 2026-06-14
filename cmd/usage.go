package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
		return &usageError{cmd: cmd, msg: err.Error()}
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
	// SuggestionsFor reads SuggestionsMinimumDistance directly (default 0); cobra's
	// unknown-command path lazily defaults it to 2. Match that so near-misses suggest.
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
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

// isRequiredFlagError reports whether err is cobra's "required flag(s) … not set",
// which ValidateRequiredFlags returns outside the args-validator and FlagErrorFunc
// hooks, so Execute classifies it as a usage error by message.
func isRequiredFlagError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "required flag(s)")
}

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
	fmt.Fprintf(&b, "taskmgr: %s\n\n", e.msg)
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
	fmt.Fprint(os.Stderr, b.String())
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
