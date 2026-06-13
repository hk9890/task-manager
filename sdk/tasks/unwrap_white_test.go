package tasks

// unwrap adapts the (*MutationResult, error) returned by the gated mutation
// methods (Create/Update/Close/Reopen) back to (*Issue, error) for the many
// tests that predate hooks and only need the resulting issue. Hook-aware tests
// use the MutationResult (Issue/Hints/Warnings) directly. White-box variant
// (package tasks); see unwrap_black_test.go for the tasks_test counterpart.
func unwrap(r *MutationResult, err error) (*Issue, error) {
	if err != nil {
		return nil, err
	}
	return r.Issue, nil
}
