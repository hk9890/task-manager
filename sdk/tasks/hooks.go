// hooks.go — lifecycle-gate hook configuration: schema, validation, and the
// compiled in-memory form. The execution/orchestration logic (selecting and
// running hooks at a transition) lives in hookrun.go; this file is pure and
// filesystem-free, so it unit-tests at L1.
//
// HOOK-SPEC §3 (configuration), §3.4 (validation).
package tasks

import (
	"fmt"
	"strings"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/query"
)

// Hook is one configured lifecycle hook as parsed from config.yaml (HOOK-SPEC
// §3.2). Unknown keys in an entry are ignored by the YAML decoder for
// forward-compatibility (TASK-STORAGE-SPEC §4.2).
type Hook struct {
	// ID is an optional label used in messages, logs, and the structured denial.
	// Defaults to "<event>#<index>" when empty.
	ID string `yaml:"id,omitempty"`
	// Event is the lifecycle event that fires the hook; one of the eight in §2.
	Event string `yaml:"event"`
	// When is an optional QUERY-SPEC filter over the `new` issue (§3.3); empty
	// means the hook always runs for its event.
	When string `yaml:"when,omitempty"`
	// Run is the argv executed directly via execve (no shell); must be non-empty.
	Run []string `yaml:"run"`
}

// Hook event names (HOOK-SPEC §2). Each transition fires a pre-event (gates)
// and a post-event (notifies).
const (
	eventPreCreate  = "pre-create"
	eventPostCreate = "post-create"
	eventPreUpdate  = "pre-update"
	eventPostUpdate = "post-update"
	eventPreClose   = "pre-close"
	eventPostClose  = "post-close"
	eventPreReopen  = "pre-reopen"
	eventPostReopen = "post-reopen"
)

// validHookEvents is the closed set of the eight events; any other value in
// config is a configuration error (§3.4).
var validHookEvents = map[string]bool{
	eventPreCreate: true, eventPostCreate: true,
	eventPreUpdate: true, eventPostUpdate: true,
	eventPreClose: true, eventPostClose: true,
	eventPreReopen: true, eventPostReopen: true,
}

// defaultHookTimeout is the global per-hook limit when hook_timeout is unset
// (HOOK-SPEC §3.1). A value of 0 (configured as "0") disables the limit.
const defaultHookTimeout = 2 * time.Second

// compiledHook is a validated hook with its `when` predicate compiled once.
type compiledHook struct {
	id    string          // resolved label (never empty)
	event string          // one of validHookEvents
	when  query.Predicate // nil means "always" (no when clause)
	run   []string        // non-empty argv
}

// isPre reports whether the hook gates a transition (pre-*) as opposed to
// reacting after it (post-*).
func (h compiledHook) isPre() bool { return strings.HasPrefix(h.event, "pre-") }

// hookSet is the compiled, validated hook configuration for a store.
type hookSet struct {
	timeout time.Duration // per-hook wall-clock limit; 0 disables
	hooks   []compiledHook
}

// forEvent returns the hooks registered for event, preserving config order
// (HOOK-SPEC §4: hooks run in config order).
func (hs *hookSet) forEvent(event string) []compiledHook {
	var out []compiledHook
	for _, h := range hs.hooks {
		if h.event == event {
			out = append(out, h)
		}
	}
	return out
}

// empty reports whether no hooks are configured at all — a fast path so stores
// without hooks skip all selection work.
func (hs *hookSet) empty() bool { return hs == nil || len(hs.hooks) == 0 }

// buildHookSet validates and compiles the raw Config hook fields into a hookSet
// (HOOK-SPEC §3.4). It is pure: a configuration error is returned, never
// applied, and it touches no filesystem.
func buildHookSet(cfg Config) (*hookSet, error) {
	timeout := defaultHookTimeout
	if raw := strings.TrimSpace(cfg.HookTimeout); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid hook_timeout %q: %w", cfg.HookTimeout, err)
		}
		if d < 0 {
			return nil, fmt.Errorf("invalid hook_timeout %q: must not be negative", cfg.HookTimeout)
		}
		timeout = d // 0 disables the limit
	}

	hs := &hookSet{timeout: timeout}
	for i, h := range cfg.Hooks {
		ch, err := compileHook(h, i)
		if err != nil {
			return nil, err
		}
		hs.hooks = append(hs.hooks, ch)
	}
	return hs, nil
}

// compileHook validates a single hook entry and compiles its `when` predicate.
func compileHook(h Hook, index int) (compiledHook, error) {
	event := strings.TrimSpace(h.Event)
	if event == "" {
		return compiledHook{}, fmt.Errorf("hook %s: missing required field event", hookLabel(h, index))
	}
	if !validHookEvents[event] {
		return compiledHook{}, fmt.Errorf("hook %s: unknown event %q", hookLabel(h, index), h.Event)
	}
	if len(h.Run) == 0 || strings.TrimSpace(h.Run[0]) == "" {
		return compiledHook{}, fmt.Errorf("hook %s: run must be a non-empty argv array", hookLabel(h, index))
	}

	id := strings.TrimSpace(h.ID)
	if id == "" {
		id = fmt.Sprintf("%s#%d", event, index)
	}

	var pred query.Predicate
	if w := strings.TrimSpace(h.When); w != "" {
		p, err := query.Compile(w)
		if err != nil {
			return compiledHook{}, fmt.Errorf("hook %s: invalid when %q: %w", id, h.When, err)
		}
		pred = p
	}

	return compiledHook{id: id, event: event, when: pred, run: h.Run}, nil
}

// hookLabel names a hook for error messages: its id when set, else
// "<event>#<index>", else "#<index>" when even the event is missing.
func hookLabel(h Hook, index int) string {
	if id := strings.TrimSpace(h.ID); id != "" {
		return id
	}
	if e := strings.TrimSpace(h.Event); e != "" {
		return fmt.Sprintf("%s#%d", e, index)
	}
	return fmt.Sprintf("#%d", index)
}
