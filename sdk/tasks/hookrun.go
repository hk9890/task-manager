// hookrun.go — hook orchestration: selecting the hooks for a transition,
// running each through the process seam, and interpreting the result into a
// decision (HOOK-SPEC §4/§6/§7). This is the imperative half of the hook
// system; the pure config/classification/payload pieces live in hooks.go,
// transition.go, and hookpayload.go. It calls the Store's runner and index but
// imports neither vfs nor os/syscall directly.
package tasks

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
)

// HookDeniedError is returned when a pre-hook refuses (or errors on) a
// transition; the mutation is aborted and nothing is written (HOOK-SPEC §6.2).
// It names the gate that refused and carries any hints gathered from hooks that
// allowed before it.
type HookDeniedError struct {
	Event   string   // the event being gated, e.g. "pre-close"
	Hook    string   // the denying hook's id
	IssueID string   // the issue the transition targeted
	Exit    int      // the hook's exit code (-1 when it never exited: spawn error/timeout)
	Reason  string   // the denial reason (the hook's message, or a synthesized one)
	Hints   []string // advisory hints from hooks that allowed before the denial
}

func (e *HookDeniedError) Error() string {
	return fmt.Sprintf("%s denied for %s by hook %q: %s", e.Event, e.IssueID, e.Hook, e.Reason)
}

// hookDecision is the interpreted outcome of one hook run.
type hookDecision int

const (
	decAllow hookDecision = iota // exit 0
	decDeny                      // well-formed refusal (exit 1-125)
	decError                     // hook misbehaved: 126/127, spawn failure, timeout, signal
)

// classifyResult maps an exec.Result to a decision, a message, and the exit
// code to report (HOOK-SPEC §6.1/§7). The message is a hint on allow and the
// reason on deny/error.
func classifyResult(res exec.Result, hookID string) (dec hookDecision, message string, exit int) {
	switch res.Category {
	case exec.Completed:
		msg := hookMessage(res)
		switch {
		case res.ExitCode == 0:
			return decAllow, msg, 0
		case res.ExitCode <= 125:
			if msg == "" {
				msg = fmt.Sprintf("denied (exit %d)", res.ExitCode)
			}
			return decDeny, msg, res.ExitCode
		default: // 126 / 127: not executable / not found
			return decError, fmt.Sprintf("not executable (exit %d)", res.ExitCode), res.ExitCode
		}
	case exec.Timeout:
		return decError, "timed out", -1
	case exec.SpawnError:
		return decError, fmt.Sprintf("could not execute: %v", res.Err), -1
	case exec.Signaled:
		return decError, fmt.Sprintf("killed by signal %d", res.Signal), 128 + res.Signal
	default:
		return decError, "failed", -1
	}
}

// hookMessage extracts a hook's plain-text message: stdout, or stderr when
// stdout is empty (HOOK-SPEC §6.1).
func hookMessage(res exec.Result) string {
	if m := strings.TrimSpace(string(res.Stdout)); m != "" {
		return m
	}
	return strings.TrimSpace(string(res.Stderr))
}

// hookEnv builds the extra environment variables for a hook process (HOOK-SPEC
// §4). They are layered on top of the inherited parent environment by the seam.
func hookEnv(event string, h compiledHook, issueID, storeDir string) []string {
	return []string{
		"TASKMGR_HOOK_EVENT=" + event,
		"TASKMGR_HOOK_ID=" + h.id,
		"TASKMGR_ISSUE_ID=" + issueID,
		"TASKMGR_STORE=" + storeDir,
		"TASKMGR_PAYLOAD_SCHEMA=" + strconv.Itoa(hookPayloadSchema),
	}
}

// runOne executes a single hook with the shared payload and returns its raw
// result. cwd is the repo root (HOOK-SPEC §3.2/§4).
func (s *Store) runOne(h compiledHook, event, issueID string, payload []byte, timeout time.Duration) exec.Result {
	return s.runner.Run(exec.Spec{
		Argv:    h.run,
		Dir:     s.root,
		Env:     hookEnv(event, h, issueID, s.dir),
		Stdin:   payload,
		Timeout: timeout,
	})
}

// hookRow builds a query.Row for newIss with ready/blocked computed against the
// store with newIss overlaid in memory (HOOK-SPEC §3.3). For a pre-hook the
// overlay makes the not-yet-written candidate visible; for a post-hook the
// committed store already reflects it, so the overlay is idempotent.
func (s *Store) hookRow(newIss *Issue) (*issueRow, error) {
	idx, _, err := s.index()
	if err != nil {
		return nil, err
	}
	idx[newIss.ID] = newIss // overlay the candidate
	return newIssueRow(newIss, idx, s.closedStatFn()), nil
}

// selectHooks filters candidates to those whose `when` matches newIss, in
// config order. Hooks without a `when` always match; the store index is read
// only when at least one candidate has a `when` clause.
func (s *Store) selectHooks(candidates []compiledHook, newIss *Issue) ([]compiledHook, error) {
	needRow := false
	for _, h := range candidates {
		if h.when != nil {
			needRow = true
			break
		}
	}
	if !needRow {
		return candidates, nil
	}
	row, err := s.hookRow(newIss)
	if err != nil {
		return nil, err
	}
	var out []compiledHook
	for _, h := range candidates {
		if h.when == nil || h.when.Match(row) {
			out = append(out, h)
		}
	}
	return out, nil
}

// runPre runs the pre-hooks for a transition, inside the write lock (HOOK-SPEC
// §4 step 4). It returns the hints gathered from hooks that allowed and, if a
// hook denies or errors, a *HookDeniedError carrying those hints — the caller
// must then abort the write. A non-nil error is an engine failure (e.g. reading
// the store to evaluate `when`), distinct from a denial.
func (s *Store) runPre(hs *hookSet, event string, old, newIss *Issue) (hints []string, denial *HookDeniedError, err error) {
	candidates := hs.forEvent(event)
	if len(candidates) == 0 {
		return nil, nil, nil
	}
	selected, err := s.selectHooks(candidates, newIss)
	if err != nil {
		return nil, nil, err
	}
	if len(selected) == 0 {
		return nil, nil, nil
	}
	payload, err := buildHookPayload(event, old, newIss)
	if err != nil {
		return nil, nil, err
	}

	for _, h := range selected {
		res := s.runOne(h, event, newIss.ID, payload, hs.timeout)
		dec, msg, exit := classifyResult(res, h.id)
		s.logHook(event, h.id, newIss.ID, dec, res)
		if dec == decAllow {
			if msg != "" {
				hints = append(hints, msg)
			}
			continue
		}
		// First non-allow stops the chain and aborts (§4): deny or hook error.
		return hints, &HookDeniedError{
			Event: event, Hook: h.id, IssueID: newIss.ID,
			Exit: exit, Reason: msg, Hints: hints,
		}, nil
	}
	return hints, nil, nil
}

// runPost runs the post-hooks for a transition, after the write committed and
// outside the lock (HOOK-SPEC §4 step 7). Post-hooks are non-vetoing: a deny or
// error becomes a warning, never a rollback. It never returns an engine error —
// a failure to evaluate `when` post-write is itself surfaced as a warning, since
// the write has already happened.
func (s *Store) runPost(hs *hookSet, event string, old, newIss *Issue) (hints, warnings []string) {
	candidates := hs.forEvent(event)
	if len(candidates) == 0 {
		return nil, nil
	}
	selected, err := s.selectHooks(candidates, newIss)
	if err != nil {
		return nil, []string{fmt.Sprintf("post-hooks skipped: %v", err)}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	payload, err := buildHookPayload(event, old, newIss)
	if err != nil {
		return nil, []string{fmt.Sprintf("post-hooks skipped: %v", err)}
	}

	for _, h := range selected {
		res := s.runOne(h, event, newIss.ID, payload, hs.timeout)
		dec, msg, _ := classifyResult(res, h.id)
		s.logHook(event, h.id, newIss.ID, dec, res)
		if dec == decAllow {
			if msg != "" {
				hints = append(hints, msg)
			}
			continue
		}
		warnings = append(warnings, fmt.Sprintf("post-hook %q: %s", h.id, msg))
	}
	return hints, warnings
}
