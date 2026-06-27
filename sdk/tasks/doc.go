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

// Package tasks is the storage engine and SDK for task-manager: a file-based
// task tracker.
//
// Each issue is a single Markdown file with a YAML frontmatter header, living
// under a per-project .tasks directory. This package is the only component that
// reads or writes those files; the taskmgr CLI and any external viewers are
// both thin layers over it. Centralizing file access here is deliberate — it is
// the single place that enforces the on-disk format, validates input, and
// serializes concurrent writers with an advisory lock, so nothing can write
// malformed state.
//
// Relationships are stored on the dependent issue only (Parent, BlockedBy,
// Related); the inverse edges (children, "blocks") are always derived by
// scanning, never persisted, so the graph cannot contradict itself.
//
// Typical use:
//
//	store, err := tasks.Open("")        // discover .tasks upward from cwd
//	iss, err := store.Create(tasks.CreateInput{Title: "Fix nav", Type: tasks.TypeBug})
//	ready, err := store.Ready()
package tasks
