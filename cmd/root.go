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
	"io"
	"os"
	"runtime/debug"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// Build info, overridable via -ldflags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// buildInfo returns the effective version/commit/date. The vars above are
// stamped via -ldflags for `make` and GoReleaser builds; for a plain
// `go install …@vX.Y.Z` (which sets no ldflags) it falls back to the module
// version and VCS settings the Go toolchain embeds in the binary.
func buildInfo() (version, commit, date string) {
	version, commit, date = Version, Commit, Date
	if version != "dev" {
		return // already stamped via -ldflags
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		version = v
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) > 7 {
				commit = s.Value[:7]
			} else if s.Value != "" {
				commit = s.Value
			}
		case "vcs.time":
			if s.Value != "" {
				date = s.Value
			}
		}
	}
	return
}

var (
	flagJSON      bool
	flagDir       string
	flagStoreName string
)

var rootCmd = &cobra.Command{
	Use:   "taskmgr",
	Short: "Task Manager — a file-based task tracker",
	Long: `taskmgr is a lean, file-based task tracker. Each issue is a Markdown file
with YAML frontmatter under a project's .tasks directory. taskmgr is the only
thing that should write those files — it validates everything and serializes
concurrent writers.

Agents: run 'taskmgr guide' for a how-to, 'taskmgr commands' for the full catalog.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// silentError marks an error whose output the command already emitted to stdout
// (e.g. a hook_denied JSON object); Execute then exits non-zero without printing
// the usual "taskmgr: …" stderr line, so a --json consumer sees only the JSON.
type silentError struct{ err error }

func (e silentError) Error() string { return e.err.Error() }
func (e silentError) Unwrap() error { return e.err }

// Execute runs taskmgr as a process: one invocation, then exit.
//
// It is a wrapper over Run so that owning os.Exit and owning the command logic
// are two different things. Everything worth testing is in Run.
func Execute() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}

// Run executes one taskmgr invocation with the given arguments, writing
// everything the command emits to stdoutW and stderrW — log records included —
// and returns the exit code the process would use: 0 on success, 1 on any
// failure. A nil args is the empty argument list, not the process's own.
//
// This is the entry point tests use. Asserting on output previously meant
// `go build` plus a fork, which is why 120 of the CLI's 121 tests sat behind the
// integration tag and its pure helpers had no tests at all.
//
// Run is NOT safe for concurrent use within one process: the command tree and
// the output writers are package-level state, reset per invocation rather than
// rebuilt. Sequential calls are independent — every flag returns to its default
// and the writers are restored — but two Runs at once would interleave. Do not
// call t.Parallel in a test that uses it.
func Run(args []string, stdoutW, stderrW io.Writer) int {
	// SetArgs(nil) does not mean "no arguments": cobra treats a nil slice as
	// "never set" and falls back to os.Args[1:], so Run(nil, …) would parse the
	// host process's own command line — a test binary's -test.* flags, or under an
	// embedder whatever subcommand that binary was invoked with.
	if args == nil {
		args = []string{}
	}
	restore := setOutput(stdoutW, stderrW)
	defer restore()

	resetFlags(rootCmd)
	// Once, not per call: it wraps each command's Args validator, so calling it
	// again would nest the wrapper one layer deeper on every invocation.
	installUsageErrorsOnce()
	rootCmd.SetArgs(args)
	// Cobra's own writers matter for --help and completion, which it prints
	// itself rather than through a RunE.
	rootCmd.SetOut(stdoutW)
	rootCmd.SetErr(stderrW)

	// ExecuteC returns the command that ran, so a required-flag failure (which cobra
	// reports outside the args/flag hooks) can still be rendered as misuse-help.
	cmd, err := rootCmd.ExecuteC()
	if err == nil {
		return 0
	}

	var ue *usageError
	var se silentError
	switch {
	case errors.As(err, &ue):
		// Misinvocation: render the compact help block (purpose, usage, example,
		// flags/subcommands, --help pointer) instead of the bare one-liner.
		renderUsageError(ue)
	case errors.As(err, &se):
		// Output already emitted to stdout (e.g. a hook_denied JSON object).
	default:
		// Cobra reports missing required flags outside the args/flag hooks as a
		// plain error; detect them structurally and render as misuse-help too,
		// naming the flags. Anything else is a genuine runtime error: stay terse.
		if missing := missingRequiredFlags(cmd); len(missing) > 0 {
			renderUsageError(&usageError{cmd: cmd, msg: requiredFlagsMsg(missing)})
		} else {
			_, _ = fmt.Fprintln(stderr, "taskmgr: "+err.Error())
		}
	}
	return 1
}

// installUsageErrorsOnce wires the misuse handling into the shared command tree
// the first time it is needed.
var installUsageErrorsOnce = sync.OnceFunc(func() { installUsageErrors(rootCmd) })

// setOutput points the package's writers at w/e for one invocation and returns
// the function that puts them back. Restoring matters for the in-process case:
// a test's buffer must not stay installed after its Run returns.
func setOutput(w, e io.Writer) func() {
	prevOut, prevErr := stdout, stderr
	stdout, stderr = w, e
	return func() { stdout, stderr = prevOut, prevErr }
}

// resetFlags returns every flag in the tree to its default and clears its
// Changed marker, so one invocation cannot see what a previous one parsed.
//
// This is what stands in for building a fresh command tree. The tree is package
// state assembled by init functions, and rebuilding it per call would mean a
// constructor for each of ~30 commands; resetting the flags clears the state
// that actually carries between invocations, since every value a command reads
// from its arguments is bound to one.
func resetFlags(cmd *cobra.Command) {
	reset := func(fs *pflag.FlagSet) {
		fs.VisitAll(func(f *pflag.Flag) {
			// A slice flag appends on Set once it has been marked changed, so
			// setting it back to its default would grow it instead of clearing
			// it. Replace is the only way to empty one.
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(nil)
			} else {
				_ = f.Value.Set(f.DefValue)
			}
			f.Changed = false
		})
	}
	reset(cmd.Flags())
	reset(cmd.PersistentFlags())
	for _, sub := range cmd.Commands() {
		resetFlags(sub)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "emit machine-readable JSON")
	rootCmd.PersistentFlags().StringVarP(&flagDir, "dir", "C", "", "start directory for locating .tasks (default: current directory)")
	rootCmd.PersistentFlags().StringVar(&flagStoreName, "store-name", "", "operate on the central store with this registry name (also names the store on 'init --central')")

	rootCmd.AddCommand(versionCmd)
}

// resolveOptions builds the SDK resolution request from the global flags
// (CONFIG-SPEC §4). The same flags drive every command.
func resolveOptions() tasks.ResolveOptions {
	return tasks.ResolveOptions{
		WorkDir:   flagDir,
		StoreName: flagStoreName,
	}
}

// openStore resolves and opens the store for the current context, honouring the
// --dir / --store-name flags and the central registry
// (CONFIG-SPEC §4). When no store resolves it turns the SDK's generic ErrNoStore
// into actionable CLI guidance — wrapping with %w so errors.Is(err,
// tasks.ErrNoStore) still holds.
func openStore() (*tasks.Store, error) {
	s, _, err := tasks.Resolve(resolveOptions(), logOption())
	if errors.Is(err, tasks.ErrNoStore) {
		return nil, fmt.Errorf("%w — run 'taskmgr init' to create one", err)
	}
	return s, err
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	RunE: func(cmd *cobra.Command, args []string) error {
		version, commit, date := buildInfo()
		if flagJSON {
			return printJSON(map[string]string{"version": version, "commit": commit, "date": date})
		}
		_, _ = fmt.Fprintf(stdout, "taskmgr %s (commit %s, built %s)\n", version, commit, date)
		return nil
	},
}
