// Package tasks is the storage engine and SDK for agent-tasks: a file-based
// task tracker.
//
// Each issue is a single Markdown file with a YAML frontmatter header, living
// under a per-project .tasks directory. This package is the only component that
// reads or writes those files; the atctl CLI and the beads-workbench viewer are
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
