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
