// Copyright 2026 Hans Kohlreiter
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package tasks

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// L2: overflow through the real Store write/read paths, on vfs.Mem.

// bigBody returns a body guaranteed to overflow, with a findable marker in it so
// text search can be exercised against content that only exists in the sidecar.
func bigBody(marker string) string {
	return marker + "\n" + strings.Repeat("padding line\n", MaxInlineBody/8)
}

// readRaw returns the raw bytes of a file in the mem store.
func readRaw(t *testing.T, s *Store, path string) []byte {
	t.Helper()
	data, err := s.fs.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	return data
}

func exists(s *Store, path string) bool {
	_, err := s.fs.Stat(path)
	return err == nil
}

// TestOverflow_CreateSplitsBody is the core storage claim: an oversized body
// never lands in the hot directory. The .md keeps frontmatter only, the bytes go
// to content/<id>, and the flag says so.
func TestOverflow_CreateSplitsBody(t *testing.T) {
	s, _ := newMemStore(t)
	body := bigBody("needle-alpha")
	iss := mustCreate(t, s, CreateInput{Title: "big", Description: body})

	md := readRaw(t, s, s.filePath(iss.ID))
	if !strings.Contains(string(md), "body_external: true") {
		t.Fatalf("md must carry the flag, got:\n%s", md)
	}
	if strings.Contains(string(md), "needle-alpha") {
		t.Fatal("the body must not be in the hot .md")
	}
	if len(md) > MaxInlineBody {
		t.Fatalf("hot file must stay bounded, got %d bytes", len(md))
	}

	sidecar := readRaw(t, s, s.contentPath(iss.ID))
	if string(sidecar) != strings.TrimSpace(body) {
		t.Fatalf("sidecar must hold the whole body: got %d bytes, want %d", len(sidecar), len(strings.TrimSpace(body)))
	}
	if filepath.Base(filepath.Dir(s.contentPath(iss.ID))) != contentDirName {
		t.Fatal("sidecar must live under content/")
	}
}

// TestOverflow_GetResolvesTransparently: the single-issue read path hides the
// split entirely, and what it returns is safe to Marshal (the flag is cleared,
// so a round-trip cannot produce a file that points at a sidecar while also
// carrying an inline body).
func TestOverflow_GetResolvesTransparently(t *testing.T) {
	s, _ := newMemStore(t)
	body := bigBody("needle-beta")
	iss := mustCreate(t, s, CreateInput{Title: "big", Description: body})

	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description != strings.TrimSpace(body) {
		t.Fatalf("Get must resolve the body: got %d bytes, want %d", len(got.Description), len(strings.TrimSpace(body)))
	}
	if got.bodyExternal {
		t.Fatal("Get must clear the flag so the result is safe to Marshal")
	}
	data, err := Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "body_external") {
		t.Fatal("marshalling a resolved issue must not claim the body is external")
	}
}

// TestOverflow_BulkReadsDoNotResolve pins the deliberate contract break: list
// paths return an empty Description for an overflowed issue so that listing a
// thousand of them can never materialize gigabytes.
func TestOverflow_BulkReadsDoNotResolve(t *testing.T) {
	s, _ := newMemStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "big", Description: bigBody("needle-gamma")})

	all, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("want 1 issue, got %d", len(all))
	}
	if all[0].Description != "" {
		t.Fatalf("bulk read must not resolve the body, got %d bytes", len(all[0].Description))
	}
	if !all[0].bodyExternal {
		t.Fatal("bulk read must report that the body is external")
	}

	// ResolveBody is the explicit way back to the content.
	if err := s.ResolveBody(all[0]); err != nil {
		t.Fatalf("ResolveBody: %v", err)
	}
	if !strings.Contains(all[0].Description, "needle-gamma") {
		t.Fatal("ResolveBody must fill in the body")
	}
	if all[0].bodyExternal {
		t.Fatal("ResolveBody must clear the flag")
	}

	// And it is a harmless no-op on an issue that was never overflowed.
	small := mustCreate(t, s, CreateInput{Title: "small", Description: "inline"})
	if err := s.ResolveBody(small); err != nil {
		t.Fatalf("ResolveBody on an inline issue: %v", err)
	}
	if small.Description != "inline" {
		t.Fatalf("ResolveBody must not disturb an inline body, got %q", small.Description)
	}
	_ = iss
}

// TestOverflow_DetailResolvesAndReports: Detail is a single-issue path, so it
// resolves like Get, and it is the one public place that still says where the
// bytes live.
func TestOverflow_DetailResolvesAndReports(t *testing.T) {
	s, _ := newMemStore(t)
	big := mustCreate(t, s, CreateInput{Title: "big", Description: bigBody("needle-delta")})
	small := mustCreate(t, s, CreateInput{Title: "small", Description: "stays inline"})

	d, err := s.Detail(big.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if !d.BodyExternal {
		t.Fatal("Detail must report an external body")
	}
	if !strings.Contains(d.Description, "needle-delta") {
		t.Fatal("Detail must resolve the body")
	}

	d2, err := s.Detail(small.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if d2.BodyExternal {
		t.Fatal("an inline issue must not report an external body")
	}
}

// TestOverflow_UpdateHysteresis walks a body down through the band and out the
// bottom, checking the layout changes only where it should — and that the stale
// sidecar is actually removed on the way back inline.
func TestOverflow_UpdateHysteresis(t *testing.T) {
	s, _ := newMemStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "big", Description: bigBody("needle-eps")})
	if !exists(s, s.contentPath(iss.ID)) {
		t.Fatal("setup: expected an external body")
	}

	// Shrink into the band (below the cap, above the floor): stays external.
	inBand := strings.Repeat("b", (MaxInlineBody+joinInlineBody)/2)
	if _, err := unwrap(s.Update(iss.ID, UpdateInput{Description: &inBand})); err != nil {
		t.Fatalf("Update into band: %v", err)
	}
	if !exists(s, s.contentPath(iss.ID)) {
		t.Fatal("a body inside the hysteresis band must stay external")
	}
	md := readRaw(t, s, s.filePath(iss.ID))
	if !strings.Contains(string(md), "body_external: true") {
		t.Fatal("flag must still be set while in the band")
	}

	// Shrink below the floor: rejoins, and the sidecar is gone.
	small := "small again"
	if _, err := unwrap(s.Update(iss.ID, UpdateInput{Description: &small})); err != nil {
		t.Fatalf("Update below floor: %v", err)
	}
	if exists(s, s.contentPath(iss.ID)) {
		t.Fatal("the stale sidecar must be removed once the body is inline again")
	}
	md = readRaw(t, s, s.filePath(iss.ID))
	if strings.Contains(string(md), "body_external") {
		t.Fatal("flag must be cleared once the body is inline")
	}
	if !strings.Contains(string(md), "small again") {
		t.Fatal("body must be back in the .md")
	}

	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description != "small again" {
		t.Fatalf("body after rejoin = %q", got.Description)
	}
}

// TestOverflow_UpdateGrowsIntoSidecar covers the other direction: an ordinary
// small issue that later grows past the cap.
func TestOverflow_UpdateGrowsIntoSidecar(t *testing.T) {
	s, _ := newMemStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "starts small", Description: "tiny"})
	if exists(s, s.contentPath(iss.ID)) {
		t.Fatal("setup: a small issue must not have a sidecar")
	}

	body := bigBody("needle-grow")
	if _, err := unwrap(s.Update(iss.ID, UpdateInput{Description: &body})); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !exists(s, s.contentPath(iss.ID)) {
		t.Fatal("growing past the cap must create the sidecar")
	}
	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(got.Description, "needle-grow") {
		t.Fatal("body must survive the split")
	}
}

// TestOverflow_UpdateOtherFieldsKeepsBody guards the case that would silently
// destroy data: touching an unrelated field on an overflowed issue must not
// truncate its body or orphan its sidecar.
func TestOverflow_UpdateOtherFieldsKeepsBody(t *testing.T) {
	s, _ := newMemStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "big", Description: bigBody("needle-keep")})

	title := "renamed"
	if _, err := unwrap(s.Update(iss.ID, UpdateInput{Title: &title})); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "renamed" {
		t.Fatalf("title = %q", got.Title)
	}
	if !strings.Contains(got.Description, "needle-keep") {
		t.Fatal("body must survive an unrelated field update")
	}
	if !exists(s, s.contentPath(iss.ID)) {
		t.Fatal("sidecar must still exist")
	}
}

// TestOverflow_CloseKeepsSidecarInPlace: only the .md moves to closed/, exactly
// like the comment sidecar rule. The body stays reachable afterwards.
func TestOverflow_CloseKeepsSidecarInPlace(t *testing.T) {
	s, _ := newMemStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "big", Description: bigBody("needle-close")})

	if _, err := unwrap(s.Close(iss.ID, "done")); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if exists(s, s.filePath(iss.ID)) {
		t.Fatal("the .md must leave the hot directory")
	}
	if !exists(s, s.closedFilePath(iss.ID)) {
		t.Fatal("the .md must land in closed/")
	}
	if !exists(s, s.contentPath(iss.ID)) {
		t.Fatal("the content sidecar must stay in content/ (only the .md moves)")
	}

	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get after close: %v", err)
	}
	if !strings.Contains(got.Description, "needle-close") {
		t.Fatal("a closed issue's body must still resolve")
	}

	// And back again.
	if _, err := unwrap(s.Reopen(iss.ID)); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if !exists(s, s.contentPath(iss.ID)) {
		t.Fatal("sidecar must survive a reopen")
	}
	got, err = s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if !strings.Contains(got.Description, "needle-close") {
		t.Fatal("body must survive a reopen")
	}
}

// TestOverflow_OrphanSidecarIsInert is the crash-safety claim. A sidecar left
// behind by an interrupted write must never override the .md — that is exactly
// what the flag buys, and without it a stale file would silently resurrect an
// older body.
func TestOverflow_OrphanSidecarIsInert(t *testing.T) {
	s, _ := newMemStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "inline", Description: "the real body"})

	// Simulate the debris of a crash between the sidecar write and the .md write.
	if err := s.fs.MkdirAll(s.contentDir(), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := s.fs.WriteAtomic(s.contentPath(iss.ID), []byte("STALE ORPHAN CONTENT"), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description != "the real body" {
		t.Fatalf("an orphan sidecar must be ignored, got %q", got.Description)
	}

	d, err := s.Detail(iss.ID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if d.BodyExternal || d.Description != "the real body" {
		t.Fatal("Detail must ignore an orphan sidecar too")
	}
}

// TestOverflow_QueryTextReadsSidecars: the text virtual field spans the body, so
// an expression using it must see overflowed content — otherwise a large issue
// is silently unmatchable.
func TestOverflow_QueryTextReadsSidecars(t *testing.T) {
	s, _ := newMemStore(t)
	big := mustCreate(t, s, CreateInput{Title: "big one", Description: bigBody("zebrafish")})
	mustCreate(t, s, CreateInput{Title: "small one", Description: "nothing here"})

	got, err := s.List(Filter{Expr: `text ~ "zebrafish"`})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || got[0].ID != big.ID {
		t.Fatalf("text query must match content only present in the sidecar, got %d matches", len(got))
	}
	// The match is returned unresolved: evaluating a predicate must not change
	// what a bulk read hands back (SDK-SPEC §4).
	if got[0].Description != "" {
		t.Fatalf("a matched issue must still come back unresolved, got %d bytes", len(got[0].Description))
	}

	// The same search through the free-text entry point must agree exactly.
	viaSearch, err := s.List(Filter{Expr: SearchExpr("zebrafish")})
	if err != nil {
		t.Fatalf("Query(SearchExpr): %v", err)
	}
	if len(viaSearch) != 1 || viaSearch[0].ID != big.ID {
		t.Fatalf("search and a raw text expression must agree, got %d matches", len(viaSearch))
	}
}

// TestOverflow_QueryWithoutTextSkipsSidecars: structured filters never look at a
// body, so they must not pay to read one. A missing sidecar would surface as an
// error if the query path read it, so removing it is a usable probe.
func TestOverflow_QueryWithoutTextSkipsSidecars(t *testing.T) {
	s, _ := newMemStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "big", Description: bigBody("needle-skip")})

	if err := s.fs.Remove(s.contentPath(iss.ID)); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	got, err := s.List(Filter{Expr: `status == "open"`})
	if err != nil {
		t.Fatalf("a structured query must not touch content sidecars: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 match, got %d", len(got))
	}

	// A text query, by contrast, genuinely needs the body and now cannot read it.
	if _, err := s.List(Filter{Expr: `text ~ "anything"`}); err == nil {
		t.Fatal("a text query must surface a missing sidecar rather than silently miss")
	}
}

// TestDocs_ExcludedFromReadyAndBlocked: a document is not work. It must never
// appear as ready or blocked, through either the engine methods or the query
// predicates that ask the same question.
func TestDocs_ExcludedFromReadyAndBlocked(t *testing.T) {
	s, _ := newMemStore(t)
	task := mustCreate(t, s, CreateInput{Title: "real work", Type: TypeTask})
	doc := mustCreate(t, s, CreateInput{Title: "design page", Type: TypeDoc})

	ready, err := s.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != task.ID {
		t.Fatalf("Ready must contain only the task, got %d issues", len(ready))
	}

	// The `ready` query predicate must agree with Store.Ready.
	viaQuery, err := s.List(Filter{Expr: "ready"})
	if err != nil {
		t.Fatalf("Query(ready): %v", err)
	}
	if len(viaQuery) != 1 || viaQuery[0].ID != task.ID {
		t.Fatalf("the ready predicate must agree with Ready(), got %d", len(viaQuery))
	}

	// Blocked: give both a blocker so only the type distinguishes them.
	blocker := mustCreate(t, s, CreateInput{Title: "blocker"})
	if err := s.AddDep(task.ID, blocker.ID); err != nil {
		t.Fatalf("AddDep: %v", err)
	}
	if err := s.AddDep(doc.ID, blocker.ID); err != nil {
		t.Fatalf("AddDep: %v", err)
	}
	blocked, err := s.Blocked()
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	if len(blocked) != 1 || blocked[0].Issue.ID != task.ID {
		t.Fatalf("Blocked must contain only the task, got %d", len(blocked))
	}
	viaQuery, err = s.List(Filter{Expr: "blocked"})
	if err != nil {
		t.Fatalf("Query(blocked): %v", err)
	}
	if len(viaQuery) != 1 || viaQuery[0].ID != task.ID {
		t.Fatalf("the blocked predicate must agree with Blocked(), got %d", len(viaQuery))
	}
}

// TestDocs_StillListedAndSearchable: docs are excluded from work views only.
// They remain ordinary issues everywhere else.
func TestDocs_StillListedAndSearchable(t *testing.T) {
	s, _ := newMemStore(t)
	doc := mustCreate(t, s, CreateInput{
		Title:       "auth redesign",
		Type:        TypeDoc,
		Labels:      []string{"kind:design"},
		Description: "the redesign covers token refresh",
	})

	all, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 || all[0].ID != doc.ID {
		t.Fatal("a doc must still appear in All")
	}

	got, err := s.List(Filter{Expr: `text ~ "refresh"`})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatal("a doc must still be searchable")
	}

	got, err = s.List(Filter{Expr: `type == "doc" && label ~ "kind:design"`})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatal("a doc must be selectable by type and label")
	}
}

// TestOverflow_ImportSplitsInBothPartitions: Import is a separate public write
// path that lands an issue directly in either partition, so it must apply
// overflow in both — otherwise a bulk import would be the one way to get an
// oversized file into the hot directory.
func TestOverflow_ImportSplitsInBothPartitions(t *testing.T) {
	s, _ := newMemStore(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	openID := "agt-imp001"
	if _, err := unwrap(s.Import(ImportInput{
		ID: openID, Title: "imported open", Status: StatusOpen, Type: TypeDoc,
		Description: bigBody("needle-imp-open"), Created: now, Updated: now,
	})); err != nil {
		t.Fatalf("Import open: %v", err)
	}
	if !exists(s, s.contentPath(openID)) {
		t.Fatal("an imported open issue must overflow like any other")
	}
	if exists(s, s.filePath(openID)) {
		md := readRaw(t, s, s.filePath(openID))
		if len(md) > MaxInlineBody {
			t.Fatalf("imported hot file must stay bounded, got %d bytes", len(md))
		}
	}

	closedID := "agt-imp002"
	if _, err := unwrap(s.Import(ImportInput{
		ID: closedID, Title: "imported closed", Status: StatusClosed, Type: TypeTask,
		Description: bigBody("needle-imp-closed"), Created: now, Updated: now, Closed: now,
	})); err != nil {
		t.Fatalf("Import closed: %v", err)
	}
	if !exists(s, s.contentPath(closedID)) {
		t.Fatal("an imported closed issue must overflow too")
	}
	if !exists(s, s.closedFilePath(closedID)) {
		t.Fatal("an imported closed issue must land in closed/")
	}

	for _, id := range []string{openID, closedID} {
		got, err := s.Get(id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if !strings.Contains(got.Description, "needle-imp") {
			t.Fatalf("imported body must resolve for %s", id)
		}
	}
}

// TestOverflow_ConcurrentWritesStayConsistent: a body write is now two files, so
// interleaving between them would be a new way to corrupt an issue. It cannot
// happen — writeFiles runs inside the store lock like every other write — and
// this pins that. Workers push one issue across the split and join thresholds
// repeatedly while others create their own overflowed issues; afterwards every
// issue must still resolve, with a body that is one of the values actually
// written rather than a mix.
//
// Run with -race, which is how the suite runs in CI.
func TestOverflow_ConcurrentWritesStayConsistent(t *testing.T) {
	const workers = 12

	s, _ := newMemStore(t)
	shared := mustCreate(t, s, CreateInput{Title: "contended", Description: "seed"})

	big := bigBody("concurrent-big")
	small := "concurrent-small"

	var wg sync.WaitGroup
	errs := make(chan error, workers*2)
	for i := range workers {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Alternate the shared issue across the split/join thresholds.
			body := big
			if n%2 == 0 {
				body = small
			}
			if _, err := s.Update(shared.ID, UpdateInput{Description: &body}); err != nil {
				errs <- fmt.Errorf("worker %d Update: %w", n, err)
				return
			}
			// And create an overflowed issue of its own.
			if _, err := s.Create(CreateInput{
				Title:       fmt.Sprintf("worker doc %d", n),
				Type:        TypeDoc,
				Description: bigBody(fmt.Sprintf("worker-%d", n)),
			}); err != nil {
				errs <- fmt.Errorf("worker %d Create: %w", n, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write failed: %v", err)
	}

	// The contended issue must have landed on exactly one of the two bodies —
	// never a torn mixture, never an unreadable pointer to a missing sidecar.
	got, err := s.Get(shared.ID)
	if err != nil {
		t.Fatalf("Get contended issue: %v", err)
	}
	if got.Description != small && got.Description != strings.TrimSpace(big) {
		t.Fatalf("contended body is neither value written (%d bytes)", len(got.Description))
	}

	// And every issue in the store must still resolve.
	all, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != workers+1 {
		t.Fatalf("want %d issues, got %d", workers+1, len(all))
	}
	for _, iss := range all {
		if err := s.ResolveBody(iss); err != nil {
			t.Fatalf("issue %s did not resolve after concurrent writes: %v", iss.ID, err)
		}
	}
}

// TestOverflow_ClosedIssueTextSearch: a closed issue's .md lives in closed/ but
// its sidecar stays in content/, so the resolver must not assume the hot
// partition. Without this, superseded design docs — the exact thing a doc gets
// closed for — would become unfindable by content the moment they are closed.
func TestOverflow_ClosedIssueTextSearch(t *testing.T) {
	s, _ := newMemStore(t)
	doc := mustCreate(t, s, CreateInput{
		Title: "Auth redesign v1", Type: TypeDoc, Description: bigBody("supersededmarker"),
	})
	if _, err := unwrap(s.Close(doc.ID, "superseded")); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Hot-only scope must not see it (it is closed), and must not error.
	hot, err := s.List(Filter{Expr: `text ~ "supersededmarker"`})
	if err != nil {
		t.Fatalf("hot-scope text query: %v", err)
	}
	if len(hot) != 0 {
		t.Fatalf("a closed issue must not appear in hot scope, got %d", len(hot))
	}

	// With the closed partition in scope, the body must still be reachable.
	got, err := s.List(Filter{Expr: `text ~ "supersededmarker"`, IncludeClosed: true})
	if err != nil {
		t.Fatalf("closed-scope text query: %v", err)
	}
	if len(got) != 1 || got[0].ID != doc.ID {
		t.Fatalf("a closed overflowed body must stay searchable, got %d matches", len(got))
	}
}

// TestOverflow_FailedMDWriteKeepsPreviousBody pins that a failed write does not
// commit the new body of an issue whose body was ALREADY external.
//
// The obvious write order — overwrite the sidecar, then rewrite the .md — makes
// the failure lie: Update returns an error and fires no post-hooks, while the
// next read serves the new body under the old frontmatter. writeFiles therefore
// stages the new bytes and renames them into place only after the .md lands, so
// a failure anywhere before that leaves the previous body exactly where it was
// (TASK-STORAGE-SPEC §4.6, docs/MONITORING.md).
func TestOverflow_FailedMDWriteKeepsPreviousBody(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/", "x", m)
	if err != nil {
		t.Fatal(err)
	}
	iss := mustCreate(t, s, CreateInput{Title: "original title", Description: bigBody("firstbody")})

	// Fail only the .md write; the sidecar write ahead of it succeeds. That is
	// exactly a crash landing between the two.
	newBody := bigBody("secondbody")
	newTitle := "renamed title"
	m.FailOn("WriteAtomic", s.filePath(iss.ID), errors.New("simulated crash"))
	if _, err := unwrap(s.Update(iss.ID, UpdateInput{Title: &newTitle, Description: &newBody})); err == nil {
		t.Fatal("expected the injected fault to fail the update")
	}

	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("the issue must remain readable after a failed write: %v", err)
	}
	// Neither half of the mutation landed: the write was reported as failed, so
	// nothing about the issue may have changed.
	if got.Title != "original title" {
		t.Fatalf("title = %q, want the un-rewritten frontmatter", got.Title)
	}
	if !strings.Contains(got.Description, "firstbody") {
		t.Fatalf("body must still be the previous one after a failed write")
	}
	if strings.Contains(got.Description, "secondbody") {
		t.Fatal("a write reported as failed must not commit the new body")
	}

	// The staged bytes are not left where a reader could pick them up.
	if _, err := m.ReadFile(s.stagedContentPath(iss.ID)); !vfs.IsNotExist(err) {
		t.Errorf("staged sidecar should have been cleaned up, got err = %v", err)
	}

	// And the store still works: the fault was consumed, so the same update
	// succeeds on a retry.
	if _, err := unwrap(s.Update(iss.ID, UpdateInput{Title: &newTitle, Description: &newBody})); err != nil {
		t.Fatalf("retry after the fault cleared: %v", err)
	}
	got, err = s.Get(iss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != newTitle || !strings.Contains(got.Description, "secondbody") {
		t.Errorf("retry did not land: title = %q, body has secondbody = %v", got.Title, strings.Contains(got.Description, "secondbody"))
	}
}

// TestOverflow_SidecarFailureLogsIOError: a body write now touches a second
// file, so the MONITORING.md contract — "nothing fails silently", every failed
// store write emits io_error with op and issue — has to hold for the content
// sidecar too, not just the .md. The sidecar write happens inside the same
// gated closure, so the existing call sites cover it; this pins that rather
// than assuming it.
func TestOverflow_SidecarFailureLogsIOError(t *testing.T) {
	cases := []struct {
		name   string
		op     string
		mutate func(s *Store, target *Issue) error
	}{
		{"create", "create", nil}, // handled inline below
		{"update", "update", func(s *Store, target *Issue) error {
			body := bigBody("needle-io-update")
			_, err := unwrap(s.Update(target.ID, UpdateInput{Description: &body}))
			return err
		}},
		{"dep add", "dep_add", nil}, // handled inline below
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			lg := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			m := vfs.NewMem()
			s, err := InitWithVFS("/", "x", m)
			if err != nil {
				t.Fatal(err)
			}
			s.logger = lg

			var target *Issue
			var mutate func() error

			switch c.op {
			case "create":
				mutate = func() error {
					_, err := unwrap(s.Create(CreateInput{Title: "big", Description: bigBody("needle-io-create")}))
					return err
				}
			case "update":
				target = mustCreate(t, s, CreateInput{Title: "small", Description: "tiny"})
				mutate = func() error { return c.mutate(s, target) }
			case "dep_add":
				// An overflowed issue whose sidecar is rewritten by a non-transition
				// edit: the op must be dep_add, not a transition name.
				target = mustCreate(t, s, CreateInput{Title: "big", Description: bigBody("needle-io-dep")})
				other := mustCreate(t, s, CreateInput{Title: "other"})
				mutate = func() error { return s.AddDep(target.ID, other.ID) }
			}

			buf.Reset()
			m.FailOn("WriteAtomic", filepath.Join(s.contentDir(), "*"), errors.New("simulated disk full"))
			if err := mutate(); err == nil {
				t.Fatal("a failed content-sidecar write must fail the mutation")
			}

			rec := find(records(t, &buf), "io_error")
			if rec == nil {
				t.Fatal("a failed content-sidecar write must emit an io_error record")
			}
			if rec["op"] != c.op {
				t.Errorf("op = %v, want %v", rec["op"], c.op)
			}
			if rec["error"] == nil || rec["error"] == "" {
				t.Error("io_error must carry the underlying error")
			}
			if target != nil && rec["issue"] != target.ID {
				t.Errorf("issue = %v, want %v", rec["issue"], target.ID)
			}
		})
	}
}

// TestOverflow_SidecarFailureLeavesIssueReadable: when the sidecar write fails,
// the .md is never written, so the issue keeps its previous body rather than
// being left pointing at content that does not exist.
func TestOverflow_SidecarFailureLeavesIssueReadable(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/", "x", m)
	if err != nil {
		t.Fatal(err)
	}
	iss := mustCreate(t, s, CreateInput{Title: "small", Description: "ORIGINAL BODY"})

	body := bigBody("never-lands")
	m.FailOn("WriteAtomic", filepath.Join(s.contentDir(), "*"), errors.New("simulated disk full"))
	if _, err := unwrap(s.Update(iss.ID, UpdateInput{Description: &body})); err == nil {
		t.Fatal("expected the injected fault to fail the update")
	}

	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatalf("the issue must still be readable after a failed sidecar write: %v", err)
	}
	if got.Description != "ORIGINAL BODY" {
		t.Fatalf("body = %q, want the previous body intact", got.Description)
	}
}

// TestComments_CapEnforcedThroughStore: the cap is enforced on the real write
// path, not just in the pure validator.
func TestComments_CapEnforcedThroughStore(t *testing.T) {
	s, _ := newMemStore(t)
	iss := mustCreate(t, s, CreateInput{Title: "issue"})

	if _, err := s.AddComment(iss.ID, "hans", strings.Repeat("c", MaxCommentBody+1)); err == nil {
		t.Fatal("an oversized comment must be rejected")
	}
	if _, err := s.AddComment(iss.ID, "hans", "a normal comment"); err != nil {
		t.Fatalf("a normal comment must still be accepted: %v", err)
	}
	// The rejected comment must not have been written.
	cs, err := s.Comments(iss.ID)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(cs) != 1 {
		t.Fatalf("want exactly the accepted comment, got %d", len(cs))
	}
}

// TestComments_MetadataReadsDoNotNeedTheSidecar pins that operations which only need an
// issue's metadata or its existence do not read its body.
//
// Resolving inside the shared read primitive made every such caller pay a full
// body read — and turned a missing sidecar into a hard failure for operations
// that never touch the body, so a comment could not be added to a doc whose
// sidecar had gone missing. A removed sidecar is the observable stand-in for
// "did it read the body?".
func TestComments_MetadataReadsDoNotNeedTheSidecar(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatal(err)
	}
	iss := mustCreate(t, s, CreateInput{Title: "doc", Type: TypeDoc, Description: bigBody("body")})
	if err := m.Remove(s.contentPath(iss.ID)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Comments(iss.ID); err != nil {
		t.Errorf("Comments: %v", err)
	}
	if _, err := s.AddComment(iss.ID, "hans", "a note"); err != nil {
		t.Errorf("AddComment: %v", err)
	}
	// A duplicate-ID check reports "taken" without materializing the body.
	_, err = s.Create(CreateInput{ID: iss.ID, Title: "clash"})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("Create with a taken id: err = %v, want ErrAlreadyExists", err)
	}

	// Get still resolves — that is its contract — and still reports the loss.
	if _, err := s.Get(iss.ID); err == nil {
		t.Error("Get must still surface a missing sidecar")
	}
}

// TestDetail_BodyExternalSurvivesClose pins that Detail reports where a body
// actually lives for a CLOSED issue too.
//
// A closed issue is never in the hot index, so Detail reaches it through the
// single-issue read. Resolving the body there clears the mirror flag before
// Detail copies it, and the flag reads false for exactly the issues most likely
// to be overflowed — a viewer stops warning about a huge body the moment the
// issue is closed.
func TestDetail_BodyExternalSurvivesClose(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatal(err)
	}
	iss := mustCreate(t, s, CreateInput{Title: "doc", Type: TypeDoc, Description: bigBody("body")})

	hot, err := s.Detail(iss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !hot.BodyExternal {
		t.Fatal("open overflowed issue: BodyExternal = false, want true")
	}
	if _, err := unwrap(s.Close(iss.ID, "done")); err != nil {
		t.Fatal(err)
	}
	closed, err := s.Detail(iss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !closed.BodyExternal {
		t.Error("closed overflowed issue: BodyExternal = false, want true")
	}
	if !strings.Contains(closed.Description, "body") {
		t.Error("the body must still be resolved for the caller")
	}
}

// TestOverflow_StaleSidecarRemovalDoesNotFailTheWrite pins that a failure while
// deleting a now-stale sidecar does not report a committed write as failed.
//
// The removal happens AFTER the .md has landed, so by then the mutation is real:
// returning an error makes the CLI exit non-zero and skips the post-hooks for a
// change the next read will show. The leftover file is inert — the .md says the
// body is inline, so nothing points at it.
func TestOverflow_StaleSidecarRemovalDoesNotFailTheWrite(t *testing.T) {
	m := vfs.NewMem()
	s, err := InitWithVFS("/p", "tst", m)
	if err != nil {
		t.Fatal(err)
	}
	iss := mustCreate(t, s, CreateInput{Title: "doc", Description: bigBody("big")})

	small := "now small"
	m.FailOn("Remove", s.contentPath(iss.ID), errors.New("read-only content dir"))
	if _, err := unwrap(s.Update(iss.ID, UpdateInput{Description: &small})); err != nil {
		t.Fatalf("the mutation was committed; it must not be reported as failed: %v", err)
	}
	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != small {
		t.Errorf("Description = %q, want the committed body", got.Description)
	}
}
