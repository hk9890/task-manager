package cmd

import (
	"log/slog"
	"os"
	"strings"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// newLogger builds the CLI's structured logger from the TASKMGR_LOG environment
// variable (MONITORING.md). The default level is warn — a successful run stays
// silent. Records go to stderr as text, never stdout, so a machine consumer
// parsing --json output is never polluted.
func newLogger() *slog.Logger {
	level := slog.LevelWarn
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TASKMGR_LOG"))) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// logOption returns the store option that wires in the CLI logger.
func logOption() tasks.Option { return tasks.WithLogger(newLogger()) }
