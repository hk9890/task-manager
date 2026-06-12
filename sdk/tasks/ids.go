package tasks

import (
	"math/rand"
	"regexp"
	"strings"
)

// idAlphabet is the base36 character set used for issue-ID tokens. It matches
// the comment-ID scheme (see newCommentID), so the two ID families look alike.
const idAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// idTokenLen is the default length of the random base36 token in a generated
// issue ID. 6 base36 chars ≈ 2.18×10^9 distinct values — large enough that
// issues created in parallel on different branches effectively never collide on
// merge (the failure mode sequential numbering suffered), while staying short
// enough to type. Same-store collisions are additionally eliminated by retrying
// against existing IDs in newIDFromNames.
const idTokenLen = 6

// maxIDLen bounds a full issue ID (TASK-STORAGE-SPEC §3).
const maxIDLen = 64

// idRe is the issue-ID grammar: a prefix, a dash, then a base36 token. Legacy
// sequential IDs ("<prefix>-0042") are a subset and remain valid.
var idRe = regexp.MustCompile(`^[a-z][a-z0-9]*-[0-9a-z]+$`)

// idStem returns the "<prefix>-<token>" stem of a "<prefix>-<token>.md" entry
// for the given prefix, or ("", false) if name is not such an entry. The token
// may be a legacy numeric suffix or a base36 token; both are recognised. It is
// a pure function: no filesystem access.
func idStem(prefix, name string) (string, bool) {
	if !strings.HasSuffix(name, FileExt) {
		return "", false
	}
	stem := strings.TrimSuffix(name, FileExt)
	want := prefix + "-"
	if !strings.HasPrefix(stem, want) || stem == want {
		return "", false
	}
	return stem, true
}

// randToken returns a random base36 token of length n.
func randToken(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = idAlphabet[rand.Intn(len(idAlphabet))]
	}
	return string(b)
}

// newIDFromNames returns a fresh, collision-resistant issue ID for prefix.
//
// The random base36 token removes the parallel-branch merge-collision class
// that sequential numbering suffered: two branches no longer pick the same
// "next number", so no renumber/doctor step is ever needed. As defence in
// depth it also retries against the provided existing entry names so an ID can
// never collide within a single store. names are file-system entry names (as
// from vfs.ReadDir); non-matching names are ignored. It performs no I/O.
func newIDFromNames(prefix string, names []string) string {
	existing := make(map[string]struct{}, len(names))
	for _, n := range names {
		if stem, ok := idStem(prefix, n); ok {
			existing[stem] = struct{}{}
		}
	}
	// Grow the token length only under (astronomically unlikely) repeated
	// collisions; this guarantees termination without an unbounded loop at a
	// single length.
	for length := idTokenLen; ; length++ {
		for i := 0; i < 16; i++ {
			id := prefix + "-" + randToken(length)
			if _, taken := existing[id]; !taken {
				return id
			}
		}
	}
}
