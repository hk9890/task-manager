package tasks

import (
	"strings"
	"testing"
	"time"
)

// TestMarshalCommentDoc_TombstoneCreatedUnquoted guards the cosmetic fix for
// at-azw: every comment document — tombstones included — must emit the created
// timestamp as a bare scalar (not yaml.v3's quoted !!timestamp form), matching
// TASK-STORAGE-SPEC §6 and every non-tombstone doc.
func TestMarshalCommentDoc_TombstoneCreatedUnquoted(t *testing.T) {
	ts := time.Date(2026, 6, 5, 10, 34, 15, 0, time.UTC)
	out := string(marshalCommentDoc(Comment{
		ID:       "abcd1234",
		Author:   "hans",
		Created:  ts,
		Replaces: "ffff0000",
		Deleted:  true,
	}))

	if !strings.Contains(out, "created: 2026-06-05T10:34:15Z") {
		t.Errorf("tombstone created must be a bare scalar; got:\n%s", out)
	}
	if strings.Contains(out, `created: "`) {
		t.Errorf("tombstone created must not be quoted; got:\n%s", out)
	}
	if !strings.Contains(out, "deleted: true") {
		t.Errorf("tombstone must carry deleted: true; got:\n%s", out)
	}

	// Still round-trips back to a tombstone.
	docs, err := parseCommentStream([]byte(out))
	if err != nil {
		t.Fatalf("parseCommentStream: %v", err)
	}
	if len(docs) != 1 || !docs[0].Deleted || docs[0].Replaces != "ffff0000" {
		t.Errorf("tombstone did not round-trip: %+v", docs)
	}
}
