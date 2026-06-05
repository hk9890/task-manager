package tasks

import (
	"fmt"
	"strconv"
	"strings"
)

// parseIDNum returns the numeric suffix of a file-name entry whose base name
// matches "<prefix>-<digits>.md". It is a pure function: no filesystem access.
//
// Returns (n, true) on success; (0, false) if the name does not match.
func parseIDNum(prefix, name string) (int, bool) {
	want := prefix + "-"
	if !strings.HasSuffix(name, FileExt) {
		return 0, false
	}
	base := strings.TrimSuffix(name, FileExt)
	if !strings.HasPrefix(base, want) {
		return 0, false
	}
	numStr := base[len(want):]
	if numStr == "" {
		return 0, false
	}
	n, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, false
	}
	return n, true
}

// nextIDFromNames computes the next sequential ID for prefix by scanning the
// provided list of file-system entry names (as returned by vfs.ReadDir) for
// the highest numeric suffix and adding one. It is a pure function: the caller
// is responsible for reading the directory; this function performs no I/O.
//
// Names that do not match "<prefix>-<digits>.md" are silently ignored.
// When no matching name exists the returned ID has suffix 0001.
func nextIDFromNames(prefix string, names []string) string {
	max := 0
	for _, name := range names {
		if n, ok := parseIDNum(prefix, name); ok && n > max {
			max = n
		}
	}
	return fmt.Sprintf("%s-%04d", prefix, max+1)
}
