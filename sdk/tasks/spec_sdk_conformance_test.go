package tasks_test

// spec_sdk_conformance_test.go — SDK-SPEC conformance suite.
//
// Spec sections covered:
//   §1  Opening a store — ErrNoStore (Open), ErrStoreExists (Init duplicate)
//   §4  Store methods — ErrNotFound (Get, Reopen), ErrImmutable (Update on closed)
//   §6  Errors & validation — all five sentinel errors exist and are returned by
//       the documented operations (errors.Is); ParseError carries Pos and Message.
//
// Already well-covered elsewhere (not duplicated here):
//   - ErrImmutable for AddDep/RemoveDep on closed issues → dep_immutable_test.go
//   - ErrNotFound for Reopen → close_reopen_test.go (TestReopen_NotFound)
//   - Close idempotency → close_reopen_test.go (TestClose_Idempotent)
//   - ValidationError from validateFields → validate_fields_test.go
//   - ParseError from Query/List → query_test.go

import (
	"errors"
	"testing"

	"github.com/hk9890/agent-tasks/sdk/tasks"
)

// ── §6 / §1: sentinel errors exist and are returned by documented operations ──

// TestSpec_SDK_Sentinels_Declared verifies that all five sentinel error
// variables are non-nil and distinct — they must be importable and addressable
// by consumers (SDK-SPEC §6).
func TestSpec_SDK_Sentinels_Declared(t *testing.T) {
	sentinels := map[string]error{
		"ErrNotFound":      tasks.ErrNotFound,
		"ErrAlreadyExists": tasks.ErrAlreadyExists,
		"ErrNoStore":       tasks.ErrNoStore,
		"ErrStoreExists":   tasks.ErrStoreExists,
		"ErrImmutable":     tasks.ErrImmutable,
	}
	seen := map[string]struct{}{}
	for name, err := range sentinels {
		if err == nil {
			t.Errorf("sentinel %s must not be nil", name)
			continue
		}
		msg := err.Error()
		if _, dup := seen[msg]; dup {
			t.Errorf("sentinel %s has duplicate message %q — sentinels must be distinct", name, msg)
		}
		seen[msg] = struct{}{}
	}
}

// TestSpec_SDK_ErrNoStore_FromOpen verifies that Open returns ErrNoStore when
// there is no .tasks directory (SDK-SPEC §1, §6).
func TestSpec_SDK_ErrNoStore_FromOpen(t *testing.T) {
	// t.TempDir() is a real empty directory with no .tasks child.
	empty := t.TempDir()
	_, err := tasks.Open(empty)
	if err == nil {
		t.Fatal("Open on empty dir: expected ErrNoStore, got nil")
	}
	if !errors.Is(err, tasks.ErrNoStore) {
		t.Errorf("Open on empty dir: got %v, want errors.Is(ErrNoStore)", err)
	}
}

// TestSpec_SDK_ErrStoreExists_FromInit verifies that Init returns ErrStoreExists
// when a store already exists at that root (SDK-SPEC §1, §6).
func TestSpec_SDK_ErrStoreExists_FromInit(t *testing.T) {
	root := t.TempDir()
	if _, err := tasks.Init(root, "tst"); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	_, err := tasks.Init(root, "tst")
	if err == nil {
		t.Fatal("second Init: expected ErrStoreExists, got nil")
	}
	if !errors.Is(err, tasks.ErrStoreExists) {
		t.Errorf("second Init: got %v, want errors.Is(ErrStoreExists)", err)
	}
}

// TestSpec_SDK_ErrNotFound_FromGet verifies that Get returns ErrNotFound for an
// unknown ID (SDK-SPEC §4, §6).
func TestSpec_SDK_ErrNotFound_FromGet(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	_, err = s.Get("tst-9999")
	if err == nil {
		t.Fatal("Get unknown ID: expected ErrNotFound, got nil")
	}
	if !errors.Is(err, tasks.ErrNotFound) {
		t.Errorf("Get unknown ID: got %v, want errors.Is(ErrNotFound)", err)
	}
}

// TestSpec_SDK_ErrImmutable_FromUpdate verifies that Update returns ErrImmutable
// when the target issue is closed (SDK-SPEC §6: "ErrImmutable is returned by
// Update … when the target issue lives in closed/").
func TestSpec_SDK_ErrImmutable_FromUpdate(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	iss, err := s.Create(tasks.CreateInput{Title: "immutable test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Close(iss.ID, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}

	newTitle := "attempted mutation"
	_, err = s.Update(iss.ID, tasks.UpdateInput{Title: &newTitle})
	if err == nil {
		t.Fatal("Update on closed issue: expected ErrImmutable, got nil")
	}
	if !errors.Is(err, tasks.ErrImmutable) {
		t.Errorf("Update on closed issue: got %v, want errors.Is(ErrImmutable)", err)
	}
}

// TestSpec_SDK_ParseError_HasPosAndMessage verifies that a malformed filter
// expression returns *ParseError carrying a non-negative Pos and a non-empty
// Message (SDK-SPEC §6; QUERY-SPEC §4).
func TestSpec_SDK_ParseError_HasPosAndMessage(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := s.Create(tasks.CreateInput{Title: "seed"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, queryErr := s.Query(`foobar == "x"`) // unknown field → ParseError
	if queryErr == nil {
		t.Fatal("Query with unknown field: expected error, got nil")
	}
	var pe *tasks.ParseError
	if !errors.As(queryErr, &pe) {
		t.Fatalf("expected *tasks.ParseError, got %T: %v", queryErr, queryErr)
	}
	if pe.Pos < 0 {
		t.Errorf("ParseError.Pos must be >= 0, got %d", pe.Pos)
	}
	if pe.Message == "" {
		t.Error("ParseError.Message must not be empty")
	}
}

// TestSpec_SDK_ValidationError_HasField verifies that a field-constraint
// violation returns *ValidationError carrying a non-empty Field (SDK-SPEC §6).
func TestSpec_SDK_ValidationError_HasField(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Empty title violates the §4 constraint — ValidationError must be returned.
	_, err = s.Create(tasks.CreateInput{Title: ""})
	if err == nil {
		t.Fatal("Create with empty title: expected ValidationError, got nil")
	}
	var ve *tasks.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *tasks.ValidationError, got %T: %v", err, err)
	}
	if ve.Field == "" {
		t.Error("ValidationError.Field must not be empty")
	}
	if ve.Field != "title" {
		t.Errorf("ValidationError.Field = %q, want \"title\"", ve.Field)
	}
}

// TestSpec_SDK_Close_Idempotent verifies the SDK-SPEC §4 contract:
// "Close is idempotent: calling it on an already-closed issue returns the
// existing issue and nil (no-op; no in-place write to closed/ is attempted)."
// (SDK-SPEC §6 / already tested at L2; this is the L3 traceability layer.)
func TestSpec_SDK_Close_Idempotent(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	iss, err := s.Create(tasks.CreateInput{Title: "close twice"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	first, err := s.Close(iss.ID, "first reason")
	if err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if first.Status != tasks.StatusClosed {
		t.Fatalf("status after first Close = %v, want closed", first.Status)
	}

	// Second Close on an already-closed issue: must succeed and return nil error.
	second, err := s.Close(iss.ID, "second reason")
	if err != nil {
		t.Fatalf("second Close (re-close) must return nil (idempotent), got: %v", err)
	}
	if second.Status != tasks.StatusClosed {
		t.Errorf("status after re-close = %v, want closed", second.Status)
	}
	// Original reason preserved — second reason is ignored on a no-op.
	if second.CloseReason != "first reason" {
		t.Errorf("CloseReason after re-close = %q, want %q (original preserved)", second.CloseReason, "first reason")
	}
}

// TestSpec_SDK_AddDep_Idempotent verifies that AddDep is idempotent:
// calling it twice with the same pair returns nil on both calls
// (SDK-SPEC §4: "AddDep … idempotent").
func TestSpec_SDK_AddDep_Idempotent(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	blocker, err := s.Create(tasks.CreateInput{Title: "blocker"})
	if err != nil {
		t.Fatalf("Create blocker: %v", err)
	}
	dep, err := s.Create(tasks.CreateInput{Title: "dependent"})
	if err != nil {
		t.Fatalf("Create dep: %v", err)
	}

	if err := s.AddDep(dep.ID, blocker.ID); err != nil {
		t.Fatalf("first AddDep: %v", err)
	}
	// Second call must be a no-op (return nil, not an error).
	if err := s.AddDep(dep.ID, blocker.ID); err != nil {
		t.Errorf("second AddDep (idempotent): got %v, want nil", err)
	}

	// Exactly one entry in BlockedBy (no duplicate).
	got, err := s.Get(dep.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.BlockedBy) != 1 {
		t.Errorf("BlockedBy len = %d after duplicate AddDep, want 1 (no duplicate)", len(got.BlockedBy))
	}
}

// TestSpec_SDK_AddDep_RejectsSelfDependency verifies that adding a self-loop is
// rejected (SDK-SPEC §4: "AddDep … rejects self-dependency").
func TestSpec_SDK_AddDep_RejectsSelfDependency(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	iss, err := s.Create(tasks.CreateInput{Title: "self"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.AddDep(iss.ID, iss.ID); err == nil {
		t.Error("AddDep self-loop: expected error, got nil")
	}
}

// TestSpec_SDK_AddDep_RejectsCycle verifies that AddDep rejects an edge that
// would introduce a cycle (SDK-SPEC §4: "AddDep … any edge that would create a
// cycle").
func TestSpec_SDK_AddDep_RejectsCycle(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	a, err := s.Create(tasks.CreateInput{Title: "A"})
	if err != nil {
		t.Fatalf("Create A: %v", err)
	}
	b, err := s.Create(tasks.CreateInput{Title: "B"})
	if err != nil {
		t.Fatalf("Create B: %v", err)
	}
	// A depends on B.
	if err := s.AddDep(a.ID, b.ID); err != nil {
		t.Fatalf("AddDep A->B: %v", err)
	}
	// B depends on A — would create a cycle.
	if err := s.AddDep(b.ID, a.ID); err == nil {
		t.Error("AddDep B->A (cycle): expected error, got nil")
	}
}

// TestSpec_SDK_AddComment_EmptyBodyRejected verifies that AddComment with an
// empty body is rejected (SDK-SPEC §4; TASK-STORAGE-SPEC §4.4 "Required unless
// deleted: true").
func TestSpec_SDK_AddComment_EmptyBodyRejected(t *testing.T) {
	root := t.TempDir()
	s, err := tasks.Init(root, "tst")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	iss, err := s.Create(tasks.CreateInput{Title: "has comment"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = s.AddComment(iss.ID, "alice", "")
	if err == nil {
		t.Error("AddComment with empty body: expected error, got nil")
	}
}
