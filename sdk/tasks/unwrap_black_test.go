package tasks_test

import "github.com/hk9890/task-manager/sdk/tasks"

// unwrap adapts the (*MutationResult, error) returned by the gated mutation
// methods back to (*Issue, error) for black-box tests that predate hooks and
// only need the resulting issue (see unwrap_white_test.go for the white-box
// counterpart).
func unwrap(r *tasks.MutationResult, err error) (*tasks.Issue, error) {
	if err != nil {
		return nil, err
	}
	return r.Issue, nil
}
