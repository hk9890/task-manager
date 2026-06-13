package tasks

import (
	"errors"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
)

// L2: the public mutation methods, gated by hooks end-to-end (HOOK-SPEC §4/§6.2).
// White-box so a fake runner can be injected; uses helpers from hookrun_test.go
// (hookTestStore, byHookID).

func TestCreate_PreCreateDenyAbortsWrite(t *testing.T) {
	fake := &exec.Fake{Func: byHookID(map[string]exec.Result{"gate": exec.Deny(1, "no new issues")})}
	s, _ := hookTestStore(t, fake, []Hook{{ID: "gate", Event: "pre-create", Run: []string{"g"}}})

	res, err := s.Create(CreateInput{Title: "blocked"})
	if res != nil || err == nil {
		t.Fatalf("denied create must return (nil, error), got (%v, %v)", res, err)
	}
	var de *HookDeniedError
	if !errors.As(err, &de) {
		t.Fatalf("error must be *HookDeniedError, got %T", err)
	}
	if de.Event != "pre-create" || de.Hook != "gate" || de.Reason != "no new issues" {
		t.Errorf("denial = %+v", de)
	}
	// Nothing was written: the store is empty.
	all, _ := s.All()
	if len(all) != 0 {
		t.Errorf("denied create wrote %d issues, want 0", len(all))
	}
}

func TestClose_PreCloseDenyLeavesStoreUnchanged(t *testing.T) {
	fake := &exec.Fake{Func: byHookID(map[string]exec.Result{"tests": exec.Deny(1, "3 tests failing")})}
	s, _ := hookTestStore(t, fake, []Hook{{ID: "tests", Event: "pre-close", Run: []string{"make", "test"}}})

	// Create succeeds (no pre-create hook configured).
	created, err := s.Create(CreateInput{Title: "to close"})
	if err != nil {
		t.Fatal(err)
	}
	before := created.Issue

	res, err := s.Close(before.ID, "done")
	if res != nil || err == nil {
		t.Fatalf("denied close must return (nil, error), got (%v, %v)", res, err)
	}
	var de *HookDeniedError
	if !errors.As(err, &de) || de.Event != "pre-close" {
		t.Fatalf("want pre-close HookDeniedError, got %v", err)
	}
	// The issue is byte-for-byte unchanged: still open, still in the hot dir,
	// Updated not advanced (HOOK-SPEC §4: a denied transition writes nothing).
	got, err := s.Get(before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status.IsClosed() {
		t.Error("denied close must leave the issue open")
	}
	if inClosed, _ := s.isInClosed(before.ID); inClosed {
		t.Error("denied close must not move the file to closed/")
	}
	if !got.Updated.Equal(before.Updated) {
		t.Errorf("denied close must not advance Updated: %v -> %v", before.Updated, got.Updated)
	}
}

func TestCreate_HintsFromPreAndPostAggregate(t *testing.T) {
	fake := &exec.Fake{Func: byHookID(map[string]exec.Result{
		"pre":  exec.Allow("pre hint"),
		"post": exec.Allow("post hint"),
	})}
	s, _ := hookTestStore(t, fake, []Hook{
		{ID: "pre", Event: "pre-create", Run: []string{"a"}},
		{ID: "post", Event: "post-create", Run: []string{"b"}},
	})

	res, err := s.Create(CreateInput{Title: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Issue == nil || res.Issue.Title != "ok" {
		t.Fatalf("issue not returned: %+v", res.Issue)
	}
	if len(res.Hints) != 2 || res.Hints[0] != "pre hint" || res.Hints[1] != "post hint" {
		t.Errorf("hints = %v, want [pre hint, post hint] (pre then post)", res.Hints)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", res.Warnings)
	}
}

func TestClose_PostHookWarningDoesNotRollBack(t *testing.T) {
	fake := &exec.Fake{Func: byHookID(map[string]exec.Result{
		"notify": exec.Deny(1, "notify failed"),
	})}
	s, _ := hookTestStore(t, fake, []Hook{{ID: "notify", Event: "post-close", Run: []string{"n"}}})

	created, err := s.Create(CreateInput{Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Close(created.Issue.ID, "done")
	if err != nil {
		t.Fatalf("post-hook failure must not fail the close: %v", err)
	}
	if !res.Issue.Status.IsClosed() {
		t.Error("close must have committed despite the post-hook warning")
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("warnings = %v, want 1 (the post-hook failure)", res.Warnings)
	}
	// The write really committed.
	if inClosed, _ := s.isInClosed(created.Issue.ID); !inClosed {
		t.Error("issue must be in closed/ after a successful close")
	}
}

func TestImport_OmitsHooksByDefaultRunsWhenAsked(t *testing.T) {
	newStore := func() *Store {
		fake := &exec.Fake{Func: byHookID(map[string]exec.Result{"gate": exec.Deny(1, "denied")})}
		s, _ := hookTestStore(t, fake, []Hook{{ID: "gate", Event: "pre-create", Run: []string{"g"}}})
		return s
	}

	// Default: hooks omitted -> the pre-create deny does not apply.
	s := newStore()
	iss, err := s.Import(ImportInput{Title: "bulk", Creator: "importer"})
	if err != nil {
		t.Fatalf("default import must omit hooks, got %v", err)
	}
	if got, _ := s.Get(iss.ID); got == nil {
		t.Fatal("imported issue must be present")
	}

	// RunHooks: true -> the gate applies and denies.
	s = newStore()
	_, err = s.Import(ImportInput{Title: "gated", Creator: "importer", RunHooks: true})
	var de *HookDeniedError
	if !errors.As(err, &de) {
		t.Fatalf("import with RunHooks must be gated, got %v", err)
	}
	if all, _ := s.All(); len(all) != 0 {
		t.Errorf("denied import wrote %d issues, want 0", len(all))
	}
}

func TestUpdate_NoOpFiresNoHooks(t *testing.T) {
	// A pre-update gate that would deny — but a no-op update never reaches it.
	fake := &exec.Fake{Func: byHookID(map[string]exec.Result{"gate": exec.Deny(1, "denied")})}
	s, _ := hookTestStore(t, fake, []Hook{{ID: "gate", Event: "pre-update", Run: []string{"g"}}})

	created, err := s.Create(CreateInput{Title: "orig"})
	if err != nil {
		t.Fatal(err)
	}
	same := "orig"
	res, err := s.Update(created.Issue.ID, UpdateInput{Title: &same})
	if err != nil {
		t.Fatalf("no-op update must not fire the pre-update gate, got %v", err)
	}
	if res.Issue.Title != "orig" {
		t.Errorf("issue = %+v", res.Issue)
	}
	// The gate never ran.
	for _, c := range fake.Calls() {
		if envVal(c, "TASKMGR_HOOK_ID") == "gate" {
			t.Error("pre-update gate must not run on a no-op")
		}
	}
}

func TestConfigError_FailsMutationsClosed(t *testing.T) {
	s, err := Init(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Hooks = []Hook{{Event: "bogus-event", Run: []string{"x"}}} // malformed

	if _, err := s.Create(CreateInput{Title: "x"}); err == nil {
		t.Error("malformed hooks config must fail Create closed (§3.4)")
	}
	// Reads remain unaffected.
	if _, err := s.All(); err != nil {
		t.Errorf("reads must be unaffected by malformed hooks: %v", err)
	}
}
