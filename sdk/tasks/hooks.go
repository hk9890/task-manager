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

// globalHookIDPrefix marks every hook that came from the per-user config
// (HOOK-SPEC §3.5). It is applied to a declared id as well as to a defaulted
// "<event>#<index>": the two files number their hooks independently, so without
// it a denial reason could name "pre-create#0" without saying which file to fix.
const globalHookIDPrefix = "global:"

// HookID returns the effective id of the index-th hook of a config file: its
// declared id, or "<event>#<index>" when it declares none, prefixed "global:"
// when it comes from the per-user config (HOOK-SPEC §3.5).
//
// This is the id a denial reason and the logs report, so a caller that lists or
// removes hooks derives it here rather than re-deriving the rule.
func HookID(h Hook, index int, global bool) string {
	prefix := ""
	if global {
		prefix = globalHookIDPrefix
	}
	return hookLabel(h, index, prefix)
}

// buildHookSet validates and compiles the global and store hook configuration
// into one hookSet (HOOK-SPEC §3.4/§3.5). Global hooks come first, so a
// machine-wide gate is evaluated before project policy and its denial is the one
// that surfaces. It is pure: a configuration error is returned, never applied,
// and it touches no filesystem.
//
// The timeout is the store's when it sets one, else the global one, else the
// 2s default — one limit for every hook in the merged chain (HOOK-SPEC §3.1).
func buildHookSet(global GlobalConfig, cfg Config) (*hookSet, error) {
	raw := strings.TrimSpace(cfg.HookTimeout)
	if raw == "" {
		raw = strings.TrimSpace(global.HookTimeout)
	}
	timeout := defaultHookTimeout
	if raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid hook_timeout %q: %w", raw, err)
		}
		if d < 0 {
			return nil, fmt.Errorf("invalid hook_timeout %q: must not be negative", raw)
		}
		timeout = d // 0 disables the limit
	}

	hs := &hookSet{timeout: timeout}
	for i, h := range global.Hooks {
		ch, err := compileHook(h, i, globalHookIDPrefix)
		if err != nil {
			return nil, err
		}
		hs.hooks = append(hs.hooks, ch)
	}
	for i, h := range cfg.Hooks {
		ch, err := compileHook(h, i, "")
		if err != nil {
			return nil, err
		}
		hs.hooks = append(hs.hooks, ch)
	}
	return hs, nil
}

// compileHook validates a single hook entry and compiles its `when` predicate.
// idPrefix scopes the resolved id to the file the entry came from; it is empty
// for a store's own hooks.
func compileHook(h Hook, index int, idPrefix string) (compiledHook, error) {
	event := strings.TrimSpace(h.Event)
	if event == "" {
		return compiledHook{}, fmt.Errorf("hook %s: missing required field event", hookLabel(h, index, idPrefix))
	}
	if !validHookEvents[event] {
		return compiledHook{}, fmt.Errorf("hook %s: unknown event %q", hookLabel(h, index, idPrefix), h.Event)
	}
	if len(h.Run) == 0 || strings.TrimSpace(h.Run[0]) == "" {
		return compiledHook{}, fmt.Errorf("hook %s: run must be a non-empty argv array", hookLabel(h, index, idPrefix))
	}
	// '#' belongs to the defaulted "<event>#<index>" id and nothing else. A
	// declared id may not contain it, so the two id sources cannot produce the
	// same text: defaults are numbered over the whole list and are unique among
	// themselves, so a collision always needs a declared id wearing the default
	// shape. Without the rule, removing an earlier hook renumbers the rest onto
	// a declared id and strands the second one — unnameable by `config hook rm`
	// and ambiguous in a denial reason (HOOK-SPEC §3.2).
	if strings.Contains(h.ID, "#") {
		return compiledHook{}, fmt.Errorf("hook %s: declared id must not contain '#' (reserved for the defaulted \"<event>#<index>\" id)", hookLabel(h, index, idPrefix))
	}

	id := hookLabel(h, index, idPrefix)

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

// hookLabel names a hook for error messages and as its resolved id: its id when
// set, else "<event>#<index>", else "#<index>" when even the event is missing.
// idPrefix scopes the name to the config file the entry came from (HOOK-SPEC
// §3.5) and applies to a declared id too, so an error always says which file to
// edit.
func hookLabel(h Hook, index int, idPrefix string) string {
	if id := strings.TrimSpace(h.ID); id != "" {
		return idPrefix + id
	}
	if e := strings.TrimSpace(h.Event); e != "" {
		return fmt.Sprintf("%s%s#%d", idPrefix, e, index)
	}
	return fmt.Sprintf("%s#%d", idPrefix, index)
}
