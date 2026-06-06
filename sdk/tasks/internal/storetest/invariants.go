// Package storetest: shared store-invariant checker.
//
// AssertStoreInvariants is exported for use by any test that wants to verify
// cross-issue consistency after a mutation. It checks the four invariants that
// no single-operation test can verify on its own:
//
//  1. No ID exists in BOTH the hot directory and closed/ (split-brain, root cause C1).
//  2. No duplicate IDs within a partition.
//  3. The hot set contains NO issue whose status field is "closed".
//  4. Every referenced ID (parent, blocked_by, related) resolves to an existing
//     issue (hot or closed/) — no dangling refs.
//
// The checker is implemented via the public Store API (Dir()) plus a single
// os.ReadDir call for the closed partition. storetest is a test-only support
// package (it never ships in a binary), so importing os here is acceptable.
package storetest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hk9890/agent-tasks/sdk/tasks"
)

// AssertStoreInvariants verifies the four store-wide consistency invariants
// described in the package doc. It calls t.Errorf (non-fatal) for each
// violation so all invariants are reported in a single test run.
//
// Intended usage: call after every mutating operation in a test, especially
// in table-driven lifecycle-matrix tests.
//
//	storetest.AssertStoreInvariants(t, store)
func AssertStoreInvariants(t testing.TB, s *tasks.Store) {
	t.Helper()

	// --- collect the hot (active) set via the public API --------------------
	hotIssues, err := s.All()
	if err != nil {
		t.Errorf("AssertStoreInvariants: All() failed: %v", err)
		return
	}

	// --- collect the closed set via direct directory scan -------------------
	// We use os.ReadDir here because the closed partition is not exposed by the
	// public Store API. storetest is test-only infrastructure, so importing os
	// is acceptable (the import-boundary test skips this package).
	closedDir := filepath.Join(s.Dir(), "closed")
	closedIssues, err := readClosedIssues(s, closedDir)
	if err != nil {
		t.Errorf("AssertStoreInvariants: reading closed/ failed: %v", err)
		return
	}

	// --- build lookup maps --------------------------------------------------
	hotByID := make(map[string]*tasks.Issue, len(hotIssues))
	for _, iss := range hotIssues {
		if _, dup := hotByID[iss.ID]; dup {
			t.Errorf("invariant violated: duplicate ID %q in hot dir", iss.ID)
		}
		hotByID[iss.ID] = iss
	}

	closedByID := make(map[string]*tasks.Issue, len(closedIssues))
	for _, iss := range closedIssues {
		if _, dup := closedByID[iss.ID]; dup {
			t.Errorf("invariant violated: duplicate ID %q in closed/", iss.ID)
		}
		closedByID[iss.ID] = iss
	}

	// --- invariant 1: no ID in BOTH hot and closed/ (C1 split-brain) --------
	for id := range hotByID {
		if _, inClosed := closedByID[id]; inClosed {
			t.Errorf("invariant C1 violated: ID %q exists in BOTH hot dir and closed/ (split-brain)", id)
		}
	}

	// --- invariant 3: hot set must not contain a closed-status issue ---------
	for id, iss := range hotByID {
		if iss.Status == tasks.StatusClosed {
			t.Errorf("invariant violated: hot-dir issue %q has status %q — closed issues must live in closed/",
				id, iss.Status)
		}
	}

	// allExists reports whether refID is found in hot or closed/.
	allExists := func(refID string) bool {
		if _, ok := hotByID[refID]; ok {
			return true
		}
		_, ok := closedByID[refID]
		return ok
	}

	// --- invariant 4: no dangling references --------------------------------
	allIssues := append(hotIssues, closedIssues...) //nolint:gocritic // not appending to hotIssues slice
	for _, iss := range allIssues {
		if iss.Parent != "" && !allExists(iss.Parent) {
			t.Errorf("invariant violated: issue %q has dangling parent ref %q", iss.ID, iss.Parent)
		}
		for _, blk := range iss.BlockedBy {
			if !allExists(blk) {
				t.Errorf("invariant violated: issue %q has dangling blocked_by ref %q", iss.ID, blk)
			}
		}
		for _, rel := range iss.Related {
			if !allExists(rel) {
				t.Errorf("invariant violated: issue %q has dangling related ref %q", iss.ID, rel)
			}
		}
	}
}

// readClosedIssues reads and parses all issue .md files in closedDir.
// Returns an empty slice if closedDir does not exist.
func readClosedIssues(s *tasks.Store, closedDir string) ([]*tasks.Issue, error) {
	entries, err := os.ReadDir(closedDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("ReadDir %s: %w", closedDir, err)
	}
	var issues []*tasks.Issue
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".md") {
			continue
		}
		id := strings.TrimSuffix(name, ".md")
		iss, err := s.Get(id)
		if err != nil {
			return nil, fmt.Errorf("Get %q from closed/: %w", id, err)
		}
		issues = append(issues, iss)
	}
	return issues, nil
}
