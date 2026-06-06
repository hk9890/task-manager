package tasks

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hk9890/agent-tasks/sdk/tasks/internal/vfs"
)

// newConcurrentMemStore creates a vfs.Mem-backed store with a thread-safe
// clock suitable for concurrent access. The clock uses a mutex-protected
// counter so goroutines do not race on the tick variable.
func newConcurrentMemStore(t *testing.T) *Store {
	t.Helper()
	m := vfs.NewMem()
	if err := m.MkdirAll("/.tasks", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s := openWithFS("/", m)
	s.cfg = Config{Prefix: "agt"}

	// Thread-safe monotonic clock: each call returns a unique, strictly
	// increasing timestamp, which prevents identical Updated values that could
	// mask races in timestamp comparisons.
	var mu sync.Mutex
	tick := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		tick = tick.Add(time.Second)
		return tick
	}
	return s
}

// TestConcurrentWrites_NoRace verifies that many goroutines calling Create,
// Update, AddComment, AddDep, and RemoveDep concurrently on a single *Store
// all serialize correctly, produce no data race (run with -race), and leave
// the store in a consistent final state.
//
// This is an L2 test: it uses vfs.Mem so no real disk is touched.
func TestConcurrentWrites_NoRace(t *testing.T) {
	const numWorkers = 20

	s := newConcurrentMemStore(t)

	// Pre-create two seed issues that workers can reference and mutate.
	seed1, err := s.Create(CreateInput{Title: "seed-1"})
	if err != nil {
		t.Fatalf("Create seed-1: %v", err)
	}
	seed2, err := s.Create(CreateInput{Title: "seed-2"})
	if err != nil {
		t.Fatalf("Create seed-2: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, numWorkers*3)

	for i := range numWorkers {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			// Each goroutine creates a new issue.
			_, err := s.Create(CreateInput{
				Title:  fmt.Sprintf("concurrent-%d", n),
				Labels: []string{"concurrent"},
			})
			if err != nil {
				errs <- fmt.Errorf("worker %d Create: %w", n, err)
				return
			}

			// Update seed-1 title (concurrently with others doing the same).
			newTitle := fmt.Sprintf("seed-1-updated-by-%d", n)
			if _, err := s.Update(seed1.ID, UpdateInput{Title: &newTitle}); err != nil {
				errs <- fmt.Errorf("worker %d Update: %w", n, err)
				return
			}

			// Add a comment to seed-2.
			if _, err := s.AddComment(seed2.ID, "bot", fmt.Sprintf("comment from worker %d", n)); err != nil {
				errs <- fmt.Errorf("worker %d AddComment: %w", n, err)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}

	// Final state consistency: All() must return the two seed issues plus one
	// per worker — numWorkers+2 total, with no duplicate IDs.
	all, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	want := numWorkers + 2
	if len(all) != want {
		t.Errorf("All() = %d issues, want %d", len(all), want)
	}
	seen := make(map[string]struct{}, len(all))
	for _, iss := range all {
		if _, dup := seen[iss.ID]; dup {
			t.Errorf("duplicate ID %q in All()", iss.ID)
		}
		seen[iss.ID] = struct{}{}
	}

	// Comments on seed-2 must equal numWorkers (one per goroutine), with no
	// duplicates (each has a unique random ID).
	comments, err := s.Comments(seed2.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(comments) != numWorkers {
		t.Errorf("Comments(%s) = %d, want %d", seed2.ID, len(comments), numWorkers)
	}
	cids := make(map[string]struct{}, len(comments))
	for _, c := range comments {
		if _, dup := cids[c.ID]; dup {
			t.Errorf("duplicate comment ID %q", c.ID)
		}
		cids[c.ID] = struct{}{}
	}
}

// TestConcurrentWrites_DepRace exercises AddDep and RemoveDep concurrently to
// confirm those paths also serialize correctly under -race.
func TestConcurrentWrites_DepRace(t *testing.T) {
	const numWorkers = 10

	s := newConcurrentMemStore(t)

	dep, err := s.Create(CreateInput{Title: "dep"})
	if err != nil {
		t.Fatalf("Create dep: %v", err)
	}
	issue, err := s.Create(CreateInput{Title: "issue"})
	if err != nil {
		t.Fatalf("Create issue: %v", err)
	}

	// First, add the dep so RemoveDep has something to remove alternately.
	if err := s.AddDep(issue.ID, dep.ID); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, numWorkers)

	for i := range numWorkers {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Alternate between add and remove; ignore "already present" /
			// "not present" no-ops — they are idempotent, not errors.
			if n%2 == 0 {
				if err := s.AddDep(issue.ID, dep.ID); err != nil {
					errs <- fmt.Errorf("worker %d AddDep: %w", n, err)
				}
			} else {
				if err := s.RemoveDep(issue.ID, dep.ID); err != nil {
					errs <- fmt.Errorf("worker %d RemoveDep: %w", n, err)
				}
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}

	// Final state: no data race — the issue must still be readable.
	if _, err := s.Get(issue.ID); err != nil {
		t.Fatalf("Get after concurrent dep ops: %v", err)
	}
}
