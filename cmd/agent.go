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

// agent.go — recognising the coding agent that invoked this process, so a write
// records who really made it (CLI-SPEC §1.1). Detection lives here in cmd/ and
// nowhere in the SDK: the store writes what it is handed, so a library caller
// and an import record the provenance they were given rather than the machine
// they happened to run on.

package cmd

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// noAgentEnv turns detection off for one run. It suppresses only detection;
// an explicit --agent still records what it names.
const noAgentEnv = "TASKMGR_NO_AGENT"

// codingAgents maps the marker variable a harness sets in its child processes to
// the slug recorded in the store, and to the variable carrying that harness's
// session id when it publishes one.
//
// A session id is an opaque local handle, so most harnesses expose none: those
// rows record the agent and leave the session empty rather than inventing one.
//
// The list is ordered, and the first marker present wins. Harnesses nest — one
// agent's shell tool can launch another — and a child's marker sits in the
// environment beside its parent's with nothing to say which of them is closer.
// Order is therefore a fixed convention, not a claim about the truth.
var codingAgents = []struct {
	marker     string // the variable whose presence identifies the harness
	slug       string // what goes in the store
	sessionVar string // the variable carrying its session id, "" when it has none
}{
	{"CLAUDECODE", "claude-code", "CLAUDE_CODE_SESSION_ID"},
	{"GEMINI_CLI", "gemini-cli", ""},
	{"COPILOT_CLI", "copilot-cli", ""},
	{"CURSOR_AGENT", "cursor", ""},
	{"OPENCODE", "opencode", ""},
	{"PI_CODING_AGENT", "pi", ""},
	{"AGENT_CONTEXT_OUT", "kiro", ""},
}

// detectAgent reports the coding agent that invoked this process and its session
// id, both empty when no harness marker is present or TASKMGR_NO_AGENT is set.
//
// The environment is read through getenv rather than os.Getenv directly so the
// table above can be exercised without a process environment.
//
// A marker is inherited by every descendant, so a script an agent launched is
// detected as that agent too. That is the intended reading: the write was still
// made on the agent's behalf.
func detectAgent(getenv func(string) string) (agent, session string) {
	if envSet(getenv(noAgentEnv)) {
		return "", ""
	}
	for _, a := range codingAgents {
		if !envSet(getenv(a.marker)) {
			continue
		}
		if a.sessionVar != "" {
			session = strings.TrimSpace(getenv(a.sessionVar))
		}
		return a.slug, session
	}
	return "", ""
}

// envSet reports whether an environment variable counts as set. A harness that
// exports its marker as "0" or "false" is saying the opposite of what its
// presence would otherwise mean, so those read as unset.
func envSet(v string) bool {
	v = strings.TrimSpace(v)
	switch strings.ToLower(v) {
	case "", "0", "false":
		return false
	}
	return true
}

// resolveActor builds the Actor recorded for one write. An explicit --agent wins
// over detection and clears any detected session with it: the session id belongs
// to the harness that published it, and pinning it to a different agent's name
// would attribute the write to a session that never made it.
func resolveActor(cmd *cobra.Command, name, agentFlag, sessionFlag string) tasks.Actor {
	agent, session := detectAgent(os.Getenv)
	if cmd.Flags().Changed("agent") {
		agent, session = agentFlag, ""
	}
	if cmd.Flags().Changed("session") {
		session = sessionFlag
	}
	return tasks.Actor{Name: defaultUser(name), Agent: agent, Session: session}
}

// addActorFlags registers the two provenance flags every write-with-an-author
// command carries. They are per-command variables (never shared between
// siblings) so each command's default line can name its own subject.
func addActorFlags(cmd *cobra.Command, agent, session *string) {
	cmd.Flags().StringVar(agent, "agent", "", "coding agent making this write (default: detected from the environment)")
	cmd.Flags().StringVar(session, "session", "", "agent session id (default: detected from the environment)")
}
