//go:build integration

// query_l3_test.go — L3 integration tests for Query / List on a real FS.
package tasks_test

import (
	"errors"
	"testing"

	"github.com/hk9890/agent-tasks/sdk/tasks"
	"github.com/hk9890/agent-tasks/sdk/tasks/internal/storetest"
)

// TestQuery_MalformedExpr_L3 verifies on a real (TempDir) store that a malformed
// expression returns a *tasks.ParseError and nothing is written to disk.
func TestQuery_MalformedExpr_L3(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001").
		TempDir(t)

	allBefore, err := store.All()
	if err != nil {
		t.Fatal(err)
	}

	_, queryErr := store.Query(`foobar == "x"`)
	if queryErr == nil {
		t.Fatal("Query with malformed expression: expected error, got nil")
	}
	var pe *tasks.ParseError
	if !errors.As(queryErr, &pe) {
		t.Fatalf("expected *tasks.ParseError, got %T: %v", queryErr, queryErr)
	}

	allAfter, err := store.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(allBefore) != len(allAfter) {
		t.Errorf("issue count changed after malformed query: %d → %d", len(allBefore), len(allAfter))
	}
}

// TestList_ExprFilter_L3 verifies on a real (TempDir) store that List with an
// Expr filter correctly selects issues and respects the closed scope rule.
func TestList_ExprFilter_L3(t *testing.T) {
	store := storetest.New(t).
		Issue("tst-0001", storetest.IssueType(tasks.TypeBug)).
		Issue("tst-0002", storetest.IssueType(tasks.TypeTask)).
		Closed("tst-0003").
		TempDir(t)

	// Type filter
	bugs, err := store.List(tasks.Filter{Expr: `type == bug`})
	if err != nil {
		t.Fatalf("List(type==bug): %v", err)
	}
	if len(bugs) != 1 || bugs[0].ID != "tst-0001" {
		t.Errorf("List(type==bug) = %v, want [tst-0001]", issueIDs(bugs))
	}

	// Closed scope auto-inclusion
	closed, err := store.List(tasks.Filter{Expr: `status == "closed"`})
	if err != nil {
		t.Fatalf("List(status==closed): %v", err)
	}
	if len(closed) != 1 || closed[0].ID != "tst-0003" {
		t.Errorf("List(status==closed) = %v, want [tst-0003]", issueIDs(closed))
	}
}
