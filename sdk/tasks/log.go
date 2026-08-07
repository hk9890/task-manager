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

// log.go — structured observability for the write path (MONITORING.md). The
// store writes facts about each operation through a log/slog logger; anything to
// *measure* (hook cost, error rate) is derived by aggregating these logs, not by
// a second runtime system. Logging is off by default: the SDK uses a discard
// logger unless the caller injects one with WithLogger, and the SDK never reads
// the environment itself — the CLI maps TASKMGR_LOG to a level and destination
// and supplies the logger.
package tasks

import (
	"context"
	"log/slog"

	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
)

// discardLogger is the default: a store with no injected logger stays silent.
var discardLogger = slog.New(slog.DiscardHandler)

// Option configures a Store at construction (Open/Init).
type Option func(*Store)

// WithLogger sets the structured logger the store writes observability records
// to (MONITORING.md). A nil logger is ignored (the discard default stays).
func WithLogger(l *slog.Logger) Option {
	return func(s *Store) {
		if l != nil {
			s.logger = l
		}
	}
}

func (s *Store) applyOptions(opts []Option) {
	for _, opt := range opts {
		opt(s)
	}
}

// hookLogOutcome maps a hook's decision to the level and `decision` value it is
// logged with. An allow is debug, a refusal is info, a hook error is warn, so a
// timeout or a failed gate is never silent.
//
// Only a pre-hook can deny: it runs before the write and aborts it. A post-hook
// runs after the write committed, so a non-zero exit is a warning about a write
// that already happened and can no longer be stopped (HOOK-SPEC §7) — it is
// logged as `warn`, not `deny`. Logging both as `deny` would make the deny-rate
// query in MONITORING.md count writes that were never blocked.
func hookLogOutcome(event string, dec hookDecision) (slog.Level, string) {
	switch dec {
	case decDeny:
		if isPostEvent(event) {
			return slog.LevelInfo, "warn"
		}
		return slog.LevelInfo, "deny"
	case decError:
		return slog.LevelWarn, "error"
	default:
		return slog.LevelDebug, "allow"
	}
}

// logHook records one hook invocation with its wall-clock duration — the main
// signal for the in-lock cost of pre-hooks (HOOK-SPEC §4/§8, MONITORING.md).
// Post-hooks run outside the lock, so `event` is what separates lock cost from
// the rest.
func (s *Store) logHook(event, hookID, issueID string, dec hookDecision, res exec.Result) {
	level, decision := hookLogOutcome(event, dec)
	s.logger.LogAttrs(context.Background(), level, "hook",
		slog.String("event", event),
		slog.String("hook", hookID),
		slog.String("issue", issueID),
		slog.String("decision", decision),
		slog.Int64("duration_ms", res.Duration.Milliseconds()),
	)
}

// Ops for store writes that are not lifecycle transitions. They fire no hooks
// and emit no `write` record — `write` is keyed on transition (MONITORING.md) —
// but a failure still has to be visible, so they carry these op names into
// io_error.
const (
	opCommentAdd    = "comment_add"
	opCommentEdit   = "comment_edit"
	opCommentDelete = "comment_delete"
	opDepAdd        = "dep_add"
	opDepRemove     = "dep_remove"
	opRelAdd        = "rel_add"
	opRelRemove     = "rel_remove"
)

// logWrite records a committed write (MONITORING.md "Write committed", debug).
func (s *Store) logWrite(trans transition, issueID string) {
	s.logger.LogAttrs(context.Background(), slog.LevelDebug, "write",
		slog.String("op", string(trans)),
		slog.String("issue", issueID),
	)
}

// logIOError records a failed write through the store (MONITORING.md
// "Store / IO error", error). op is a transition name for a gated mutation and
// one of the op* constants above for the writes that are not transitions.
func (s *Store) logIOError(op, issueID string, err error) {
	s.logger.LogAttrs(context.Background(), slog.LevelError, "io_error",
		slog.String("op", op),
		slog.String("issue", issueID),
		slog.String("error", err.Error()),
	)
}
