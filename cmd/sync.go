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

// sync.go — `taskmgr sync`: publishing this machine's central stores to a git
// remote (CLI-SPEC §2.5).
//
// The operation is the SDK's (tasks.InitPublish, tasks.Publish): it runs git
// and takes the store locks. This file is the command surface over it — the
// flags, the two DTOs and the one-line summary.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// syncDTO is what `sync` prints (CLI-SPEC §6).
type syncDTO struct {
	Machine   string `json:"machine"`
	Branch    string `json:"branch"`
	Committed bool   `json:"committed"`
	Commit    string `json:"commit,omitempty"`
	Pushed    bool   `json:"pushed"`
}

// syncInitDTO is what `sync init` prints (CLI-SPEC §6).
type syncInitDTO struct {
	Machine string `json:"machine"`
	Branch  string `json:"branch"`
	Path    string `json:"path"`
	Remote  string `json:"remote"`
}

var syncInitFlags struct{ remote string }

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Publish this machine's central stores to its git remote",
	Long: `Commit every change under <central_root>/stores and push it to the branch
machine/<machine-id> of the remote 'sync init' configured.

Each machine publishes its own branch and nothing else pushes it, so a push is
always a fast-forward. A push the remote rejects means another machine uses
this machine ID, or this machine was set up again: pick another machine_id.

A run with nothing new commits nothing and reaches no network. Project-local
.tasks stores are not published.

One sync runs at a time: a second one, or a 'sync init', fails at once and
names the process that is running. A push that has not finished after 10
minutes, or an HTTPS push that moves almost nothing for a minute, is stopped,
and the sync fails; the next sync pushes the commit.
TASKMGR_LOG=debug logs each step on stderr.`,
	Example: "taskmgr sync",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSync()
	},
}

var syncInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Make the central stores a git work tree that publishes to <url>",
	Long: `Make <central_root>/stores a git repository on the branch machine/<machine-id>,
with <url> as its remote 'origin'. The engine's lock and temp files are kept
out of what it publishes.

The machine ID is the per-user config key machine_id, or the hostname when it is
unset. Running init again with the same remote changes nothing; a different
remote is refused rather than overwritten.`,
	Example: "taskmgr sync init --remote git@github.com:you/taskmgr-stores.git",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return initSync(syncInitFlags.remote)
	},
}

// initSync makes the central stores directory a work tree publishing to url.
func initSync(url string) error {
	info, err := tasks.InitPublish(url, logOption())
	if err != nil {
		return err
	}
	if flagJSON {
		return printJSON(syncInitDTO{Machine: info.Machine, Branch: info.Branch, Path: info.Path, Remote: info.Remote})
	}
	_, _ = fmt.Fprintf(stdout, "%s publishes to %s on branch %s\n", info.Path, info.Remote, info.Branch)
	_, _ = fmt.Fprintln(stdout, "next: taskmgr sync")
	return nil
}

// runSync commits what changed under the central stores directory and pushes
// it.
func runSync() error {
	res, err := tasks.Publish(logOption())
	if err != nil {
		return err
	}
	if flagJSON {
		return printJSON(syncDTO{Machine: res.Machine, Branch: res.Branch, Committed: res.Committed, Commit: res.Commit, Pushed: res.Pushed})
	}
	_, _ = fmt.Fprintln(stdout, syncSummary(res))
	return nil
}

// syncSummary is the one line `sync` prints.
func syncSummary(r tasks.PublishResult) string {
	switch {
	case r.Committed && r.Pushed:
		return fmt.Sprintf("Committed %s and pushed %s to %s", shortSHA(r.Commit), r.Branch, r.RemoteName)
	case r.Pushed:
		return fmt.Sprintf("Nothing new to commit; pushed %s to %s", r.Branch, r.RemoteName)
	default:
		return "Nothing to publish: no change since the last sync"
	}
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func init() {
	syncInitCmd.Flags().StringVar(&syncInitFlags.remote, "remote", "", "the git URL to publish to")
	_ = syncInitCmd.MarkFlagRequired("remote")

	syncCmd.AddCommand(syncInitCmd)
	rootCmd.AddCommand(syncCmd)
}
