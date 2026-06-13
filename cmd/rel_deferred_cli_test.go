//go:build integration

// L4 CLI tests for `taskmgr rel add|rm` (symmetric related links) and the
// `deferred` status.
package cmd_test

import (
	"encoding/json"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// mkIssue creates an issue via the CLI and returns its id.
func mkIssue(t *testing.T, root, title string) string {
	t.Helper()
	out, errs, code := taskmgr(t, root, "create", "--title", title, "--json")
	if code != 0 {
		t.Fatalf("create exit=%d stderr=%q", code, errs)
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.ID == "" {
		t.Fatalf("bad create json %q: %v", out, err)
	}
	return res.ID
}

type detailRefs struct {
	Status      string `json:"status"`
	RelatedRefs []struct {
		ID string `json:"id"`
	} `json:"related_refs"`
}

func showDetail(t *testing.T, root, id string) detailRefs {
	t.Helper()
	out, errs, code := taskmgr(t, root, "show", id, "--json")
	if code != 0 {
		t.Fatalf("show %s exit=%d stderr=%q", id, code, errs)
	}
	var d detailRefs
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("bad show json %q: %v", out, err)
	}
	return d
}

func relatedHas(d detailRefs, id string) bool {
	for _, r := range d.RelatedRefs {
		if r.ID == id {
			return true
		}
	}
	return false
}

func TestL4_Rel_AddIsSymmetricThenRemove(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	a := mkIssue(t, root, "a")
	b := mkIssue(t, root, "b")

	if _, errs, code := taskmgr(t, root, "rel", "add", a, b); code != 0 {
		t.Fatalf("rel add exit=%d stderr=%q", code, errs)
	}
	// Symmetric: the link shows from BOTH issues, though stored only on a.
	if !relatedHas(showDetail(t, root, a), b) {
		t.Errorf("a should show b as related")
	}
	if !relatedHas(showDetail(t, root, b), a) {
		t.Errorf("b should show a as related (derived inverse)")
	}

	// rm severs it from both sides.
	if _, errs, code := taskmgr(t, root, "rel", "rm", a, b); code != 0 {
		t.Fatalf("rel rm exit=%d stderr=%q", code, errs)
	}
	if relatedHas(showDetail(t, root, a), b) || relatedHas(showDetail(t, root, b), a) {
		t.Errorf("link should be gone from both sides after rel rm")
	}
}

func TestL4_Update_DeferredStatus(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	id := mkIssue(t, root, "postpone")

	if _, errs, code := taskmgr(t, root, "update", id, "--status", "deferred"); code != 0 {
		t.Fatalf("update --status deferred exit=%d stderr=%q", code, errs)
	}
	if got := showDetail(t, root, id).Status; got != "deferred" {
		t.Errorf("status = %q, want deferred", got)
	}
	// A deferred issue is active (non-closed) → not in `ready`, but in default list.
	out, _, _ := taskmgr(t, root, "ready", "--json")
	var ready []struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(out), &ready)
	for _, r := range ready {
		if r.ID == id {
			t.Errorf("deferred issue must not appear in ready")
		}
	}
}

func TestL4_Statuses_IncludesDeferred(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	out, _, code := taskmgr(t, root, "statuses", "--json")
	if code != 0 {
		t.Fatalf("statuses exit=%d", code)
	}
	var statuses []string
	if err := json.Unmarshal([]byte(out), &statuses); err != nil {
		t.Fatalf("bad statuses json %q: %v", out, err)
	}
	found := false
	for _, s := range statuses {
		if s == "deferred" {
			found = true
		}
	}
	if !found {
		t.Errorf("statuses should include deferred, got %v", statuses)
	}
}
