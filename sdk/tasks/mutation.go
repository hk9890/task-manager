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

// mutation.go — the result of a hook-gated write and the shared pre/post hook
// wrapping used by Create/Update/Close/Reopen (HOOK-SPEC §4/§6.2).
package tasks

// MutationResult is what a successful gated write returns: the resulting issue
// plus advisory output from hooks (HOOK-SPEC §6.2). Hints are collected from
// every pre- and post-hook that allowed; Warnings are post-hook failures, which
// never fail the write. Both are nil when no hooks ran or none had anything to
// say.
type MutationResult struct {
	Issue    *Issue
	Hints    []string
	Warnings []string
}

// validateWrite is field validation as the write path applies it: a violation
// this write *introduces* is refused, and one it *found* on disk is passed
// through.
//
// An issue can be invalid on disk — hand-edited, restored from a backup, or
// written by a build with looser rules. Refusing every write for it froze the
// issue instead of repairing it: `close`, `dep add` and even `update --title`
// failed on a field the mutation never touched, and for a field no command can
// rewrite (there is no `--creator`) the issue could then never be closed or
// re-linked again. The rule here is the one HOOK-SPEC §3.4 already states for a
// config file's `use:` list — a write checks what it introduces, not what it
// finds — and it keeps the invariant that matters: the engine never *writes* a
// violation it did not already have to live with.
//
// The stored issue is read only once a violation has been found, i.e. only on
// the path that is about to refuse the write, so the successful write is
// unchanged and the in-lock path does not lengthen (docs/REVIEWING.md).
func (s *Store) validateWrite(iss *Issue) error {
	violations := fieldViolations(iss)
	if len(violations) == 0 {
		return nil
	}
	prev, err := s.Get(iss.ID)
	if err != nil {
		// Nothing on disk to inherit from — a create, or a store that cannot be
		// read. Either way every violation is this write's own.
		return violations[0]
	}
	for _, v := range violations {
		if !fieldUnchanged(v.Field, prev, iss) {
			return v
		}
	}
	return nil
}

// validateAndIndex validates iss, builds the hot index once, and runs reference
// checks against it, returning the index so a gated mutation can reuse it for
// the hook `when` row instead of scanning the store a second time under the lock.
func (s *Store) validateAndIndex(iss *Issue) (map[string]*Issue, error) {
	if err := s.validateWrite(iss); err != nil {
		return nil, err
	}
	idx, _, err := s.index()
	if err != nil {
		return nil, err
	}
	if err := s.checkRefsWith(iss, idx); err != nil {
		return nil, err
	}
	return idx, nil
}

// gateWrite runs the pre-hooks for trans and then write, all inside the store
// lock (the caller holds it). idx is the pre-built hot index shared with
// reference-checking (nil → the hook row builds its own). It returns the hints
// collected from hooks that allowed. A denial (*HookDeniedError) or an engine
// error aborts: it is returned as err and nothing is written (HOOK-SPEC §4).
func (s *Store) gateWrite(hs *hookSet, trans transition, old, newIss *Issue, idx map[string]*Issue, write func() error) ([]string, error) {
	hints, denial, err := s.runPre(hs, trans.preEvent(), old, newIss, idx)
	if err != nil {
		return hints, err
	}
	if denial != nil {
		return hints, denial
	}
	if err := write(); err != nil {
		s.logIOError(string(trans), newIss.ID, err)
		return hints, err
	}
	s.logWrite(trans, newIss.ID)
	return hints, nil
}

// postFinish assembles the MutationResult after the lock is released, running
// the post-hooks for trans when a write actually fired (HOOK-SPEC §4 step 7).
// A no-op mutation (fired == false) ran no pre-hooks and runs no post-hooks.
func (s *Store) postFinish(hs *hookSet, fired bool, trans transition, old, newIss *Issue, preHints []string) *MutationResult {
	res := &MutationResult{Issue: newIss, Hints: preHints}
	if fired {
		postHints, warnings := s.runPost(hs, trans.postEvent(), old, newIss)
		res.Hints = append(res.Hints, postHints...)
		res.Warnings = warnings
	}
	return res
}
