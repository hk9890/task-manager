package tasks

import (
	"errors"
	"testing"
)

// hasRef reports whether refs contains an issue with the given id.
func hasRef(refs []Ref, id string) bool {
	for _, r := range refs {
		if r.ID == id {
			return true
		}
	}
	return false
}

func relDetail(t *testing.T, s *Store, id string) *Detail {
	t.Helper()
	d, err := s.Detail(id)
	if err != nil {
		t.Fatalf("Detail(%s): %v", id, err)
	}
	return d
}

func TestAddRelatedIsSymmetricInView(t *testing.T) {
	s := newTestStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	b := mustCreate(t, s, CreateInput{Title: "b"})

	if err := s.AddRelated(a.ID, b.ID); err != nil {
		t.Fatalf("AddRelated: %v", err)
	}
	// Forward: a lists b.
	if !hasRef(relDetail(t, s, a.ID).RelatedRefs, b.ID) {
		t.Errorf("a's RelatedRefs should include b")
	}
	// Inverse (derived): b shows a even though the edge is stored only on a.
	if !hasRef(relDetail(t, s, b.ID).RelatedRefs, a.ID) {
		t.Errorf("b's RelatedRefs should include a (derived inverse)")
	}
}

func TestAddRelatedNoDuplicateWhenStoredBothWays(t *testing.T) {
	s := newTestStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	b := mustCreate(t, s, CreateInput{Title: "b"})
	if err := s.AddRelated(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRelated(b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	// a stores b (forward) and b stores a (inverse) → b must appear exactly once.
	refs := relDetail(t, s, a.ID).RelatedRefs
	n := 0
	for _, r := range refs {
		if r.ID == b.ID {
			n++
		}
	}
	if n != 1 {
		t.Errorf("b should appear once in a's RelatedRefs, got %d", n)
	}
}

func TestAddRelatedIdempotentAndRejectsSelf(t *testing.T) {
	s := newTestStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	b := mustCreate(t, s, CreateInput{Title: "b"})
	if err := s.AddRelated(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRelated(a.ID, b.ID); err != nil { // idempotent
		t.Fatalf("second AddRelated should be a no-op: %v", err)
	}
	got, _ := s.Get(a.ID)
	if len(got.Related) != 1 {
		t.Errorf("want 1 related, got %d", len(got.Related))
	}
	if err := s.AddRelated(a.ID, a.ID); err == nil {
		t.Errorf("self-relation should be rejected")
	}
}

func TestAddRelatedRejectsDangling(t *testing.T) {
	s := newTestStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	err := s.AddRelated(a.ID, "agt-nope")
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "related" {
		t.Errorf("want related ValidationError, got %v", err)
	}
}

func TestRemoveRelatedClearsBothSides(t *testing.T) {
	s := newTestStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	b := mustCreate(t, s, CreateInput{Title: "b"})
	// Edge stored on BOTH sides.
	if err := s.AddRelated(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRelated(b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveRelated(a.ID, b.ID); err != nil {
		t.Fatalf("RemoveRelated: %v", err)
	}
	ga, _ := s.Get(a.ID)
	gb, _ := s.Get(b.ID)
	if len(ga.Related) != 0 || len(gb.Related) != 0 {
		t.Errorf("both sides should be cleared: a=%v b=%v", ga.Related, gb.Related)
	}
	if hasRef(relDetail(t, s, a.ID).RelatedRefs, b.ID) {
		t.Errorf("link should be fully severed in the view")
	}
}

func TestRelatedWriteToClosedIsImmutable(t *testing.T) {
	s := newTestStore(t)
	a := mustCreate(t, s, CreateInput{Title: "a"})
	b := mustCreate(t, s, CreateInput{Title: "b"})
	if _, err := s.Close(a.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRelated(a.ID, b.ID); !errors.Is(err, ErrImmutable) {
		t.Errorf("AddRelated on closed primary should be ErrImmutable, got %v", err)
	}
	if err := s.RemoveRelated(a.ID, b.ID); !errors.Is(err, ErrImmutable) {
		t.Errorf("RemoveRelated on closed primary should be ErrImmutable, got %v", err)
	}
}
