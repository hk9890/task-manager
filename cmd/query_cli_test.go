//go:build integration

// L4 CLI tests for `atctl list -q <expr>` and `atctl search`.
//
// Coverage:
//   - All §6 example expressions from QUERY-SPEC.md
//   - atctl search <text> equivalence with list -q 'text ~ "<text>"'
//   - Malformed -q: exit code 1, message on stderr prefixed "atctl: "
//   - JSON output: array of issueDTO
package cmd_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hk9890/agent-tasks/sdk/tasks"
)

// ── fixture helpers ───────────────────────────────────────────────────────────

// queryFixture builds a store populated with issues suitable for exercising all
// §6 example expressions. Returns the root dir and a map of logical names to
// issue IDs for assertions.
//
// Issues created:
//   - "open-p0":    open, bug,   priority 0, assignee "hans", label "area:db"
//   - "open-p1":    open, bug,   priority 1, assignee "hans", label "area:ui"
//   - "open-p2":    open, chore, priority 2, assignee "hans"
//   - "open-task":  open, task,  priority 2 (not a bug/chore; no match for
//     assignee hans && (bug||chore)); tests negative
//   - "child":      open, task,  priority 2, parent = open-p0.ID
//   - "blocker":    open, task,  priority 1 (will block "blocked-iss")
//   - "blocked-iss": open, task, priority 3, blocked_by = blocker.ID
//   - "drill-iss":  open, task,  priority 2, title contains "drill"
//   - "closed-old": closed 2025-12-31 (before "2026-01-01")
//   - "closed-new": closed 2026-06-01 (after "2026-01-01")
func queryFixture(t *testing.T) (root string, ids map[string]string) {
	t.Helper()
	root = t.TempDir()
	s, err := tasks.Init(root, "qfx")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Deterministic clock; each Create advances by 1s.
	tick := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.SetNow(func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	})

	create := func(in tasks.CreateInput) *tasks.Issue {
		t.Helper()
		iss, err := s.Create(in)
		if err != nil {
			t.Fatalf("Create(%q): %v", in.Title, err)
		}
		return iss
	}

	p := func(n int) *int { return &n }

	openP0 := create(tasks.CreateInput{
		Title:    "open-p0 bug with area:db",
		Type:     tasks.TypeBug,
		Priority: p(0),
		Assignee: "hans",
		Labels:   []string{"area:db"},
	})
	openP1 := create(tasks.CreateInput{
		Title:    "open-p1 bug with area:ui",
		Type:     tasks.TypeBug,
		Priority: p(1),
		Assignee: "hans",
		Labels:   []string{"area:ui"},
	})
	openP2 := create(tasks.CreateInput{
		Title:    "open-p2 chore hans",
		Type:     tasks.TypeChore,
		Priority: p(2),
		Assignee: "hans",
	})
	openTask := create(tasks.CreateInput{
		Title:    "open-task feature no match",
		Type:     tasks.TypeFeature,
		Priority: p(2),
	})
	child := create(tasks.CreateInput{
		Title:  "child issue",
		Parent: openP0.ID,
	})
	blocker := create(tasks.CreateInput{
		Title:    "blocker issue",
		Priority: p(1),
	})
	blockedIss := create(tasks.CreateInput{
		Title:    "blocked issue drill navigation",
		Priority: p(3),
	})
	drillIss := create(tasks.CreateInput{
		Title: "drill nav issue",
	})

	// Wire the blocking dependency: blockedIss blocked by blocker.
	if err := s.AddDep(blockedIss.ID, blocker.ID); err != nil {
		t.Fatalf("AddDep: %v", err)
	}

	// Create closed issues with controlled close times.
	closedOld := create(tasks.CreateInput{Title: "closed old"})
	// Rewind clock to before 2026-01-01 for the close timestamp.
	beforeDate := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	s.SetNow(func() time.Time { return beforeDate })
	if _, err := s.Close(closedOld.ID, "old"); err != nil {
		t.Fatalf("Close old: %v", err)
	}

	closedNew := create(tasks.CreateInput{Title: "closed new"})
	afterDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	s.SetNow(func() time.Time { return afterDate })
	if _, err := s.Close(closedNew.ID, "new"); err != nil {
		t.Fatalf("Close new: %v", err)
	}

	ids = map[string]string{
		"open-p0":     openP0.ID,
		"open-p1":     openP1.ID,
		"open-p2":     openP2.ID,
		"open-task":   openTask.ID,
		"child":       child.ID,
		"blocker":     blocker.ID,
		"blocked-iss": blockedIss.ID,
		"drill-iss":   drillIss.ID,
		"closed-old":  closedOld.ID,
		"closed-new":  closedNew.ID,
	}
	return root, ids
}

// listQuery runs `atctl --json list -q <expr>` and returns the issue IDs in
// the result.
func listQuery(t *testing.T, root, expr string) []string {
	t.Helper()
	out, _, code := atctl(t, root, "--json", "list", "-q", expr)
	if code != 0 {
		t.Fatalf("list -q %q failed (exit %d): %s", expr, code, out)
	}
	return issueIDsFromJSON(t, out)
}

// listQueryAllFlags runs `atctl --json list -q <expr> --all` and returns ids.
func listQueryAllFlags(t *testing.T, root, expr string, extraArgs ...string) []string {
	t.Helper()
	args := append([]string{"--json", "list", "-q", expr}, extraArgs...)
	out, _, code := atctl(t, root, args...)
	if code != 0 {
		t.Fatalf("list -q %q (%v) failed (exit %d): %s", expr, extraArgs, code, out)
	}
	return issueIDsFromJSON(t, out)
}

func hasID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// ── §6 example expressions ────────────────────────────────────────────────────

// TestL4_Query_StatusOpen — `status == "open"` selects hot issues.
func TestL4_Query_StatusOpen(t *testing.T) {
	root, ids := queryFixture(t)
	got := listQuery(t, root, `status == "open"`)

	// All active issues should be returned.
	for name, id := range ids {
		if strings.HasPrefix(name, "closed") {
			if hasID(got, id) {
				t.Errorf("status==\"open\" returned closed issue %s (%s)", name, id)
			}
		} else {
			if !hasID(got, id) {
				t.Errorf("status==\"open\" missing hot issue %s (%s); got: %v", name, id, got)
			}
		}
	}
}

// TestL4_Query_StatusOpenAndPriorityLTE1 — `status == "open" && priority <= 1`.
func TestL4_Query_StatusOpenAndPriorityLTE1(t *testing.T) {
	root, ids := queryFixture(t)
	got := listQuery(t, root, `status == "open" && priority <= 1`)

	// open-p0 (P0) and open-p1 (P1) and blocker (P1) match.
	// open-p2, open-task, child, blocked-iss, drill-iss (P2/P3) do not.
	for _, wantID := range []string{ids["open-p0"], ids["open-p1"], ids["blocker"]} {
		if !hasID(got, wantID) {
			t.Errorf("status==\"open\" && priority<=1: missing %q; got: %v", wantID, got)
		}
	}
	for _, noID := range []string{ids["open-p2"], ids["open-task"], ids["child"], ids["blocked-iss"], ids["drill-iss"]} {
		if hasID(got, noID) {
			t.Errorf("status==\"open\" && priority<=1: unexpected %q in result", noID)
		}
	}
}

// TestL4_Query_TypeBugLabelAreaDB — `type == bug && label ~ "area:db"`.
func TestL4_Query_TypeBugLabelAreaDB(t *testing.T) {
	root, ids := queryFixture(t)
	got := listQuery(t, root, `type == bug && label ~ "area:db"`)

	if !hasID(got, ids["open-p0"]) {
		t.Errorf(`type==bug && label~"area:db": missing open-p0 (%s); got: %v`, ids["open-p0"], got)
	}
	// open-p1 has label "area:ui" (no match) and is a bug but wrong label.
	if hasID(got, ids["open-p1"]) {
		t.Errorf(`type==bug && label~"area:db": open-p1 should not match (label "area:ui")`)
	}
}

// TestL4_Query_ReadyAndPriorityLTE2 — `ready && priority <= 2`.
func TestL4_Query_ReadyAndPriorityLTE2(t *testing.T) {
	root, ids := queryFixture(t)
	got := listQuery(t, root, `ready && priority <= 2`)

	// "blocked-iss" has an open blocker → not ready.
	if hasID(got, ids["blocked-iss"]) {
		t.Errorf("ready && priority<=2: blocked-iss should not appear (has open blocker)")
	}
	// "blocker" is open, no blockers, P1 → ready.
	if !hasID(got, ids["blocker"]) {
		t.Errorf("ready && priority<=2: blocker issue (%s) should appear; got: %v", ids["blocker"], got)
	}
}

// TestL4_Query_TextDrillAndNotBlocked — `text ~ "drill" && !blocked`.
func TestL4_Query_TextDrillAndNotBlocked(t *testing.T) {
	root, ids := queryFixture(t)
	got := listQuery(t, root, `text ~ "drill" && !blocked`)

	// "drill-iss" title = "drill nav issue", not blocked → should match.
	if !hasID(got, ids["drill-iss"]) {
		t.Errorf(`text~"drill" && !blocked: drill-iss missing; got: %v`, got)
	}
	// "blocked-iss" title = "blocked issue drill navigation", is blocked → must not appear.
	if hasID(got, ids["blocked-iss"]) {
		t.Errorf(`text~"drill" && !blocked: blocked-iss should not appear (blocked); got: %v`, got)
	}
}

// TestL4_Query_AssigneeHansAndBugOrChore — `assignee == "hans" && (type == bug || type == chore)`.
func TestL4_Query_AssigneeHansAndBugOrChore(t *testing.T) {
	root, ids := queryFixture(t)
	got := listQuery(t, root, `assignee == "hans" && (type == bug || type == chore)`)

	// open-p0: hans + bug ✓
	if !hasID(got, ids["open-p0"]) {
		t.Errorf("assignee=hans && (bug||chore): open-p0 missing; got: %v", got)
	}
	// open-p1: hans + bug ✓
	if !hasID(got, ids["open-p1"]) {
		t.Errorf("assignee=hans && (bug||chore): open-p1 missing; got: %v", got)
	}
	// open-p2: hans + chore ✓
	if !hasID(got, ids["open-p2"]) {
		t.Errorf("assignee=hans && (bug||chore): open-p2 missing; got: %v", got)
	}
	// open-task: no assignee, type=feature ✗
	if hasID(got, ids["open-task"]) {
		t.Errorf("assignee=hans && (bug||chore): open-task should not appear")
	}
}

// TestL4_Query_ClosedAfterDate — `closed > "2026-01-01"`.
func TestL4_Query_ClosedAfterDate(t *testing.T) {
	root, ids := queryFixture(t)
	// closed > "2026-01-01" auto-includes the cold partition (QUERY-SPEC §5).
	got := listQueryAllFlags(t, root, `closed > "2026-01-01"`)

	// closed-new was closed 2026-06-01 → after 2026-01-01 ✓
	if !hasID(got, ids["closed-new"]) {
		t.Errorf(`closed>"2026-01-01": closed-new missing; got: %v`, got)
	}
	// closed-old was closed 2025-12-31 → before 2026-01-01 ✗
	if hasID(got, ids["closed-old"]) {
		t.Errorf(`closed>"2026-01-01": closed-old should not appear (closed before threshold)`)
	}
}

// TestL4_Query_ParentID — `parent == "<id>"`.
func TestL4_Query_ParentID(t *testing.T) {
	root, ids := queryFixture(t)
	parentID := ids["open-p0"]
	expr := `parent == "` + parentID + `"`
	got := listQuery(t, root, expr)

	if !hasID(got, ids["child"]) {
		t.Errorf("parent==%q: child issue missing; got: %v", parentID, got)
	}
	// Other issues have no parent (or a different one).
	for name, id := range ids {
		if name == "child" {
			continue
		}
		if hasID(got, id) {
			t.Errorf("parent==%q: unexpected issue %s (%s) in result", parentID, name, id)
		}
	}
}

// ── JSON output shape ──────────────────────────────────────────────────────────

// TestL4_Query_JSONIsArray verifies that `list -q <expr> --json` returns a JSON
// array of issueDTOs with required fields.
func TestL4_Query_JSONIsArray(t *testing.T) {
	root, _ := queryFixture(t)
	out, _, code := atctl(t, root, "--json", "list", "-q", `status == "open"`)
	if code != 0 {
		t.Fatalf("list -q failed (exit %d): %s", code, out)
	}

	var arr []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("output is not a JSON array: %v\noutput: %s", err, out)
	}
	if len(arr) == 0 {
		t.Fatal("expected at least one issueDTO in the array")
	}
	// Each element must have at minimum: id, title, status, type, priority.
	for i, dto := range arr {
		for _, field := range []string{"id", "title", "status", "type", "priority"} {
			if _, ok := dto[field]; !ok {
				t.Errorf("issueDTO[%d] missing field %q; keys: %v", i, field, mapKeys(dto))
			}
		}
	}
}

// ── search equivalence ────────────────────────────────────────────────────────

// TestL4_Search_EquivalentToTextTilde verifies that `search <text>` returns the
// same issues as `list -q 'text ~ "<text>"'`.
func TestL4_Search_EquivalentToTextTilde(t *testing.T) {
	root, ids := queryFixture(t)

	searchOut, _, code := atctl(t, root, "--json", "search", "drill")
	if code != 0 {
		t.Fatalf("search drill failed (exit %d): %s", code, searchOut)
	}
	listOut, _, code := atctl(t, root, "--json", "list", "-q", `text ~ "drill"`)
	if code != 0 {
		t.Fatalf(`list -q 'text~"drill"' failed (exit %d): %s`, code, listOut)
	}

	searchIDs := issueIDsFromJSON(t, searchOut)
	listIDs := issueIDsFromJSON(t, listOut)

	if len(searchIDs) != len(listIDs) {
		t.Errorf("search vs list-q: different lengths; search=%v list=%v", searchIDs, listIDs)
	}
	// Both should contain drill-iss.
	if !hasID(searchIDs, ids["drill-iss"]) {
		t.Errorf("search drill: drill-iss missing from search; got: %v", searchIDs)
	}
	if !hasID(listIDs, ids["drill-iss"]) {
		t.Errorf(`list -q 'text~"drill"': drill-iss missing; got: %v`, listIDs)
	}
}

// TestL4_Search_AllIncludesClosed verifies `search <text> --all` includes the
// cold partition.
func TestL4_Search_AllIncludesClosed(t *testing.T) {
	root, ids := queryFixture(t)

	// "closed" is in the title of both closed issues.
	out, _, code := atctl(t, root, "--json", "search", "closed", "--all")
	if code != 0 {
		t.Fatalf("search --all failed (exit %d): %s", code, out)
	}
	got := issueIDsFromJSON(t, out)
	if !hasID(got, ids["closed-old"]) {
		t.Errorf("search --all: closed-old missing; got: %v", got)
	}
	if !hasID(got, ids["closed-new"]) {
		t.Errorf("search --all: closed-new missing; got: %v", got)
	}
}

// ── malformed expr ────────────────────────────────────────────────────────────

// TestL4_MalformedExpr_ExitOne verifies that a malformed -q expression causes
// atctl to exit with code 1 and print a message on stderr prefixed "atctl: ".
func TestL4_MalformedExpr_ExitOne(t *testing.T) {
	root, _ := queryFixture(t)

	malformed := []string{
		`foobar == "x"`,     // unknown field
		`status < "open"`,   // bad operator for enum
		`priority == 5`,     // priority out of range
		`(status == "open"`, // unbalanced paren
		`text == "x"`,       // text only allows ~
	}

	for _, expr := range malformed {
		t.Run(expr, func(t *testing.T) {
			_, stderr, code := atctl(t, root, "list", "-q", expr)
			if code != 1 {
				t.Errorf("list -q %q: expected exit 1, got %d", expr, code)
			}
			if !strings.HasPrefix(stderr, "atctl: ") {
				t.Errorf("list -q %q: stderr must start with 'atctl: '; got: %q", expr, stderr)
			}
		})
	}
}

// TestL4_SearchMalformedInternalExpr_ExitOne verifies that a malformed extra
// -q expression passed alongside search also yields exit 1.
// (search <text> builds text~"<text>"; a separate -q would be combined with &&.)
func TestL4_SearchMalformedInternalExpr_ExitOne(t *testing.T) {
	root, _ := queryFixture(t)

	// search with a malformed -q combined: `(foobar == "x") && text ~ "drill"`
	_, stderr, code := atctl(t, root, "search", "drill", "-q", `foobar == "x"`)
	if code != 1 {
		t.Errorf("search with malformed -q: expected exit 1, got %d", code)
	}
	if !strings.HasPrefix(stderr, "atctl: ") {
		t.Errorf("stderr must start with 'atctl: '; got: %q", stderr)
	}
}
