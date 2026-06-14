//go:build integration

package cmd_test

import "github.com/hk9890/task-manager/sdk/tasks"

// unwrap adapts the (*MutationResult, error) returned by the SDK's gated
// mutation methods back to (*Issue, error) for L4 CLI tests that seed fixtures
// via the SDK and only need the resulting issue.
func unwrap(r *tasks.MutationResult, err error) (*tasks.Issue, error) {
	if err != nil {
		return nil, err
	}
	return r.Issue, nil
}
