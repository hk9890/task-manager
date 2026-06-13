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

// validateAndIndex validates iss, builds the hot index once, and runs reference
// checks against it, returning the index so a gated mutation can reuse it for
// the hook `when` row instead of scanning the store a second time under the lock.
func (s *Store) validateAndIndex(iss *Issue) (map[string]*Issue, error) {
	if err := validateFields(iss); err != nil {
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
		s.logIOError(trans, newIss.ID, err)
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
