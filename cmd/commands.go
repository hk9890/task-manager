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
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

// flagDoc describes a single flag in the command catalog.
type flagDoc struct {
	Name      string `yaml:"name" json:"name"`
	Shorthand string `yaml:"shorthand,omitempty" json:"shorthand,omitempty"`
	Type      string `yaml:"type" json:"type"`
	Usage     string `yaml:"usage" json:"usage"`
	Default   string `yaml:"default,omitempty" json:"default,omitempty"`
	Required  bool   `yaml:"required,omitempty" json:"required,omitempty"`
}

// commandDoc describes a single command in the catalog.
type commandDoc struct {
	Name    string    `yaml:"name" json:"name"`
	Purpose string    `yaml:"purpose" json:"purpose"`
	Usage   string    `yaml:"usage" json:"usage"`
	Example string    `yaml:"example" json:"example"`
	Flags   []flagDoc `yaml:"flags,omitempty" json:"flags,omitempty"`
	// Output is the shape the command prints under --json, absent for a command
	// that prints none.
	Output *outputDoc `yaml:"output,omitempty" json:"output,omitempty"`
}

// catalogDoc is the full machine-readable CLI catalog.
type catalogDoc struct {
	Binary      string       `yaml:"binary" json:"binary"`
	Description string       `yaml:"description" json:"description"`
	GlobalFlags []flagDoc    `yaml:"global_flags,omitempty" json:"global_flags,omitempty"`
	Commands    []commandDoc `yaml:"commands" json:"commands"`
}

var commandsCmd = &cobra.Command{
	Use:   "commands [name...]",
	Short: "Print a YAML catalog of every command, or of the ones named (for agents)",
	Long: `Print a structured catalog of the CLI surface — one entry per command with
its purpose, flags, and a usage example. The catalog is derived from the live
command tree, so it never drifts from the actual CLI.

With no arguments it prints every command. Name commands to print only their
entries; a name is what the catalog calls the command, so a subcommand is one
quoted argument: taskmgr commands show "comment add".

A command that prints JSON under --json also carries "output": whether the
result is an object or an array, and each field with its JSON type. A field
marked optional is absent when it is empty, and one marked recursive holds
objects with the fields of the object it is in.

Output is YAML by default (compact and agent-friendly); pass --json for JSON.`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cat := buildCatalog(rootCmd)
		if len(args) > 0 {
			selected, misuse := selectCommands(cat.Commands, args)
			if misuse != "" {
				return &usageError{cmd: cmd, msg: misuse}
			}
			cat.Commands = selected
		}
		if flagJSON {
			return printJSON(cat)
		}
		out, err := yaml.Marshal(cat)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprint(stdout, string(out))
		return nil
	},
}

// selectCommands returns the entries with the given catalog names, in catalog
// order. An unknown name yields a misuse message instead, naming the entries it
// could have meant: the ones it is a word of ("add" for "comment add"), or the
// ones it starts.
func selectCommands(all []commandDoc, names []string) (selected []commandDoc, misuse string) {
	for _, name := range names {
		if slices.ContainsFunc(all, func(c commandDoc) bool { return c.Name == name }) {
			continue
		}
		misuse = fmt.Sprintf("unknown command %q", name)
		var near []string
		for _, c := range all {
			if name != "" && (slices.Contains(strings.Fields(c.Name), name) || strings.HasPrefix(c.Name, name)) {
				near = append(near, fmt.Sprintf("%q", c.Name))
			}
		}
		if len(near) > 0 {
			misuse += ": did you mean " + strings.Join(near, " or ") + "?"
		}
		return nil, misuse + "\n\nRun taskmgr commands for every name. A subcommand is one quoted argument."
	}
	for _, c := range all {
		if slices.Contains(names, c.Name) {
			selected = append(selected, c)
		}
	}
	return selected, ""
}

// buildCatalog walks the command tree rooted at root and returns its catalog.
func buildCatalog(root *cobra.Command) catalogDoc {
	cat := catalogDoc{
		Binary:      root.Name(),
		Description: root.Short,
		GlobalFlags: collectFlags(root.PersistentFlags(), nil),
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			cat.Commands = append(cat.Commands, docFor(sub))
			walk(sub)
		}
	}
	walk(root)
	sort.Slice(cat.Commands, func(i, j int) bool {
		return cat.Commands[i].Name < cat.Commands[j].Name
	})
	return cat
}

// docFor builds the catalog entry for a single command.
func docFor(c *cobra.Command) commandDoc {
	name := strings.TrimPrefix(c.CommandPath(), c.Root().Name()+" ")
	return commandDoc{
		Name:    name,
		Purpose: c.Short,
		Usage:   c.UseLine(),
		Example: exampleFor(c),
		Flags:   collectFlags(c.LocalFlags(), c),
		Output:  outputFor(name),
	}
}

// exampleFor synthesises a concrete usage example from the command's own
// metadata: positional args declared in Use, plus any required flags. A command
// whose required flag depends on another flag states its own Example instead.
func exampleFor(c *cobra.Command) string {
	if c.Example != "" {
		return c.Example
	}
	// Group commands (subcommands, no positional args of their own) are invoked via
	// a subcommand. Detect this structurally rather than by Runnable(): the misuse-
	// help layer attaches a dispatcher RunE to groups at startup, which would
	// otherwise make them look runnable.
	if c.HasAvailableSubCommands() && len(strings.Fields(c.Use)) <= 1 {
		return c.CommandPath() + " <subcommand>"
	}
	parts := []string{c.CommandPath()}
	if toks := strings.Fields(c.Use); len(toks) > 1 {
		parts = append(parts, strings.Join(toks[1:], " "))
	}
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if _, required := f.Annotations[cobra.BashCompOneRequiredFlag]; required {
			parts = append(parts, fmt.Sprintf("--%s <%s>", f.Name, f.Name))
		}
	})
	return strings.Join(parts, " ")
}

// collectFlags renders a flag set into flagDocs, skipping hidden flags and
// noise defaults. When c is non-nil, flags marked required are flagged as such.
func collectFlags(fs *pflag.FlagSet, c *cobra.Command) []flagDoc {
	var out []flagDoc
	fs.VisitAll(func(f *pflag.Flag) {
		// The auto-added -h/--help flag is on every command; it is noise here.
		if f.Hidden || f.Name == "help" {
			return
		}
		fd := flagDoc{
			Name:      f.Name,
			Shorthand: f.Shorthand,
			Type:      f.Value.Type(),
			Usage:     f.Usage,
		}
		// Surface only meaningful defaults; "", "false", "0" are just noise.
		switch f.DefValue {
		case "", "false", "0":
		default:
			fd.Default = f.DefValue
		}
		if c != nil {
			if _, required := f.Annotations[cobra.BashCompOneRequiredFlag]; required {
				fd.Required = true
			}
		}
		out = append(out, fd)
	})
	return out
}

func init() {
	rootCmd.AddCommand(commandsCmd)
}
