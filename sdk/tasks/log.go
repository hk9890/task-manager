// log.go — observability hooks for the write path. This is a stub: the call
// sites (currently each hook invocation) are in place, and Phase 7 wires the
// body to log/slog with the MONITORING design — per-hook event/id/issue/
// decision/duration_ms, levels, and the TASKMGR_LOG env var. Keeping it a
// separate no-op now lets the orchestration declare *where* it logs without
// pulling the whole logging subsystem into this phase.
package tasks

import "github.com/hk9890/task-manager/sdk/tasks/internal/exec"

// logHook records one hook invocation (HOOK-SPEC §4 Observability;
// MONITORING.md). No-op until Phase 7 supplies the logger.
func (s *Store) logHook(event, hookID, issueID string, dec hookDecision, res exec.Result) {
	_ = event
	_ = hookID
	_ = issueID
	_ = dec
	_ = res
}
