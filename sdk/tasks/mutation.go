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

// gateWrite runs the pre-hooks for trans and then write, all inside the store
// lock (the caller holds it). It returns the hints collected from hooks that
// allowed. A denial (*HookDeniedError) or an engine error aborts: it is returned
// as err and nothing is written (HOOK-SPEC §4 steps 4–5).
func (s *Store) gateWrite(hs *hookSet, trans transition, old, newIss *Issue, write func() error) ([]string, error) {
	hints, denial, err := s.runPre(hs, trans.preEvent(), old, newIss)
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
