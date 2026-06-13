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

// logHook records one hook invocation with its wall-clock duration — the main
// signal for the in-lock cost of pre-hooks (HOOK-SPEC §4/§8, MONITORING.md).
// An allow logs at debug, a deny at info, a hook error at warn, so a timeout or
// a failed gate is never silent.
func (s *Store) logHook(event, hookID, issueID string, dec hookDecision, res exec.Result) {
	level := slog.LevelDebug
	decision := "allow"
	switch dec {
	case decDeny:
		level, decision = slog.LevelInfo, "deny"
	case decError:
		level, decision = slog.LevelWarn, "error"
	}
	s.logger.LogAttrs(context.Background(), level, "hook",
		slog.String("event", event),
		slog.String("hook", hookID),
		slog.String("issue", issueID),
		slog.String("decision", decision),
		slog.Int64("duration_ms", res.Duration.Milliseconds()),
	)
}

// logWrite records a committed write (MONITORING.md "Write committed", debug).
func (s *Store) logWrite(trans transition, issueID string) {
	s.logger.LogAttrs(context.Background(), slog.LevelDebug, "write",
		slog.String("op", string(trans)),
		slog.String("issue", issueID),
	)
}

// logIOError records a failed write through the store (MONITORING.md
// "Store / IO error", error).
func (s *Store) logIOError(trans transition, issueID string, err error) {
	s.logger.LogAttrs(context.Background(), slog.LevelError, "io_error",
		slog.String("op", string(trans)),
		slog.String("issue", issueID),
		slog.String("error", err.Error()),
	)
}
