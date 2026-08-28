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

// Hook is one configured lifecycle hook as parsed from a package manifest
// (HOOK-SPEC §3.6). Unknown keys in an entry are ignored by the YAML decoder for
// forward-compatibility, as in a config file (TASK-STORAGE-SPEC §4.2).
type Hook struct {
	// ID is the hook's label within its package, used in messages, logs, and the
	// structured denial. It is required and must not contain ':'; the effective
	// id a denial reports is "pkg:<package>:<id>" (packages.go). There is no
	// positional default: a package is replaced whole when it is updated, and a
	// numbered id would silently move to a different hook.
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

// buildHookSet validates and compiles a resolved hook chain into one hookSet
// (HOOK-SPEC §3.4/§3.5). The chain arrives flat and already ordered — the
// per-user config's packages first, then the store's — and every entry already
// carries the effective id it runs under, so this stays pure: it returns a
// configuration error, never applies one, and touches no filesystem.
//
// The timeout is the store's when it sets one, else the per-user one, else the
// 2s default — one limit for every hook in the chain (HOOK-SPEC §3.1). A package
// cannot contribute one: raising it would extend how long the store lock is
// held, for every project on the machine (HOOK-SPEC §8).
func buildHookSet(globalTimeout, storeTimeout string, hooks []packageHook) (*hookSet, error) {
	raw := strings.TrimSpace(storeTimeout)
	if raw == "" {
		raw = strings.TrimSpace(globalTimeout)
	}
	timeout, err := parseHookTimeout(raw)
	if err != nil {
		return nil, err
	}

	hs := &hookSet{timeout: timeout}
	for _, ph := range hooks {
		ch, err := compileHook(ph.hook, ph.id)
		if err != nil {
			return nil, err
		}
		hs.hooks = append(hs.hooks, ch)
	}
	return hs, nil
}

// parseHookTimeout turns a configured hook_timeout into a duration: empty means
// the 2s default, "0" disables the limit (HOOK-SPEC §3.1).
func parseHookTimeout(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultHookTimeout, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid hook_timeout %q: %w", raw, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("invalid hook_timeout %q: must not be negative", raw)
	}
	return d, nil
}

// compileHook validates a single hook entry and compiles its `when` predicate.
// id is the effective id the entry runs under, composed by the package it came
// from (packages.go); it is what a denial reason and the logs report.
func compileHook(h Hook, id string) (compiledHook, error) {
	event := strings.TrimSpace(h.Event)
	if event == "" {
		return compiledHook{}, fmt.Errorf("hook %s: missing required field event", id)
	}
	if !validHookEvents[event] {
		return compiledHook{}, fmt.Errorf("hook %s: unknown event %q", id, h.Event)
	}
	if len(h.Run) == 0 || strings.TrimSpace(h.Run[0]) == "" {
		return compiledHook{}, fmt.Errorf("hook %s: run must be a non-empty argv array", id)
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
