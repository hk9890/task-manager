package tasks

import "testing"

func ptrStatus(s Status) *Status { return &s }

func TestDeferredIsValidStatus(t *testing.T) {
	if !StatusDeferred.Valid() {
		t.Fatal("deferred must be a valid status")
	}
	if StatusDeferred.IsClosed() {
		t.Error("deferred is an active status, not closed")
	}
	found := false
	for _, s := range Statuses {
		if s == StatusDeferred {
			found = true
		}
	}
	if !found {
		t.Error("deferred must be listed in Statuses")
	}
}

func TestDeferredExcludedFromReady(t *testing.T) {
	s := newTestStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "postpone me"})
	// Open with no blockers → ready.
	ready, _ := s.Ready()
	if !hasRef(toRefs(ready), iss.ID) {
		t.Fatalf("issue should start ready")
	}
	if _, err := s.Update(iss.ID, UpdateInput{Status: ptrStatus(StatusDeferred)}); err != nil {
		t.Fatalf("Update to deferred: %v", err)
	}
	ready, _ = s.Ready()
	if hasRef(toRefs(ready), iss.ID) {
		t.Errorf("deferred issue must not be ready")
	}
}

func TestDeferredStaysInActivePartition(t *testing.T) {
	s := newTestStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "x"})
	if _, err := s.Update(iss.ID, UpdateInput{Status: ptrStatus(StatusDeferred)}); err != nil {
		t.Fatal(err)
	}
	inClosed, err := s.isInClosed(iss.ID)
	if err != nil || inClosed {
		t.Errorf("deferred issue must stay in the active partition (inClosed=%v)", inClosed)
	}
	got, _ := s.Get(iss.ID)
	if got.Status != StatusDeferred {
		t.Errorf("status = %q, want deferred", got.Status)
	}
}

func TestImportDeferredStatusPassesThrough(t *testing.T) {
	s := newTestStore(t)
	iss, err := unwrap(s.Import(ImportInput{Title: "imported deferred", Status: StatusDeferred, Created: tCreated}))
	if err != nil {
		t.Fatalf("Import deferred: %v", err)
	}
	got, _ := s.Get(iss.ID)
	if got.Status != StatusDeferred {
		t.Errorf("imported status = %q, want deferred", got.Status)
	}
}

// toRefs adapts a slice of *Issue to []Ref for hasRef.
func toRefs(issues []*Issue) []Ref {
	out := make([]Ref, 0, len(issues))
	for _, i := range issues {
		out = append(out, ref(i))
	}
	return out
}
