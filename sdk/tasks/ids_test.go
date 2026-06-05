package tasks

import (
	"testing"
)

// TestNextIDFromNames covers the pure high-water computation over a name list.
// This is an L1 test: no FS, no store, just the function.
func TestNextIDFromNames(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		names  []string
		want   string
	}{
		{
			name:   "empty list allocates first",
			prefix: "agt",
			names:  nil,
			want:   "agt-0001",
		},
		{
			name:   "single entry",
			prefix: "agt",
			names:  []string{"agt-0001.md"},
			want:   "agt-0002",
		},
		{
			name:   "high water mark across gap",
			prefix: "agt",
			names:  []string{"agt-0001.md", "agt-0005.md", "agt-0003.md"},
			want:   "agt-0006",
		},
		{
			name:   "ignores different prefix",
			prefix: "agt",
			names:  []string{"agt-0002.md", "other-0099.md"},
			want:   "agt-0003",
		},
		{
			name:   "ignores malformed names (no number)",
			prefix: "agt",
			names:  []string{"agt-0001.md", "agt-badnum.md", "agt-.md"},
			want:   "agt-0002",
		},
		{
			name:   "ignores directories and hidden files",
			prefix: "agt",
			names:  []string{"agt-0001.md", "agt-0002", ".lock"},
			want:   "agt-0002",
		},
		{
			name:   "names without extension skipped",
			prefix: "agt",
			names:  []string{"agt-0010"},
			want:   "agt-0001",
		},
		{
			name:   "different prefix",
			prefix: "xyz",
			names:  []string{"xyz-0007.md", "xyz-0002.md"},
			want:   "xyz-0008",
		},
		{
			name:   "handles closed/ subdir entries if names include them",
			prefix: "agt",
			// store.go only passes the top-level .tasks/ dir entries; closed/
			// sub-entries would not appear but the function should still be robust.
			names: []string{"agt-0003.md", "agt-0001.md"},
			want:  "agt-0004",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nextIDFromNames(tc.prefix, tc.names)
			if got != tc.want {
				t.Errorf("nextIDFromNames(%q, %v) = %q; want %q", tc.prefix, tc.names, got, tc.want)
			}
		})
	}
}

// TestParseIDNum exercises the low-level numeric parser.
func TestParseIDNum(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		input  string
		wantN  int
		wantOK bool
	}{
		{"valid", "agt", "agt-0042.md", 42, true},
		{"leading zeros", "agt", "agt-0001.md", 1, true},
		{"wrong prefix", "agt", "xyz-0001.md", 0, false},
		{"no extension", "agt", "agt-0001", 0, false},
		{"non-numeric suffix", "agt", "agt-abc.md", 0, false},
		{"empty suffix after dash", "agt", "agt-.md", 0, false},
		{"hidden file", "agt", ".lock", 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n, ok := parseIDNum(tc.prefix, tc.input)
			if ok != tc.wantOK || n != tc.wantN {
				t.Errorf("parseIDNum(%q, %q) = (%d, %v); want (%d, %v)",
					tc.prefix, tc.input, n, ok, tc.wantN, tc.wantOK)
			}
		})
	}
}
