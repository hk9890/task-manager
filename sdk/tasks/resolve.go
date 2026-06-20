package tasks

import (
	"path/filepath"
	"regexp"
	"strings"
)

// This file is pure core (CONFIG-SPEC §4 matching): it carries the resolution
// types and the lexical path helpers used to decide which store a working
// directory maps to. It performs no filesystem or environment access — the I/O
// (reading the global config and registry, walk-up, symlink canonicalization)
// lives in the imperative shell (config.go, registry.go) and feeds canonical
// strings to these helpers.

// ResolveOptions configures Resolve / Stores (SDK-SPEC §1).
type ResolveOptions struct {
	// WorkDir is the resolution origin: where local walk-up starts and the path
	// matched against the central registry. Empty means the process working
	// directory.
	WorkDir string
	// StorePath is an explicit store-path override (--store-path / TASKMGR_DIR):
	// the store at this path is opened directly, with no walk-up or registry.
	StorePath string
	// StoreName is an explicit central store-name override (--store-name): the
	// registered store with this name is opened. Mutually exclusive with StorePath.
	StoreName string
}

// ResolveKind reports how Resolve chose a store (SDK-SPEC §1).
type ResolveKind int

const (
	// ResolvedLocal: a local .tasks found by walking up from WorkDir.
	ResolvedLocal ResolveKind = iota
	// ResolvedCentral: matched the central registry by project path.
	ResolvedCentral
	// ResolvedOverridePath: an explicit StorePath / TASKMGR_DIR override.
	ResolvedOverridePath
	// ResolvedOverrideName: an explicit StoreName override.
	ResolvedOverrideName
)

// String returns the stable, agent-facing token for the kind, matching the
// CLI `where` JSON contract (CLI-SPEC §6). It never returns "none"; the absence
// of a store is signalled by ErrNoStore, which the CLI renders as "none".
func (k ResolveKind) String() string {
	switch k {
	case ResolvedLocal:
		return "local"
	case ResolvedCentral:
		return "central"
	case ResolvedOverridePath:
		return "override_path"
	case ResolvedOverrideName:
		return "override_name"
	default:
		return "unknown"
	}
}

// ResolveInfo describes the store Resolve chose and why (SDK-SPEC §1).
type ResolveInfo struct {
	Kind        ResolveKind // how the store was chosen
	StorePath   string      // the resolved store directory
	ProjectPath string      // the project the store tracks (the store's parent for a local store)
}

// StoreEntry is one central-registry entry (SDK-SPEC §1, returned by Stores).
type StoreEntry struct {
	Path      string // the project path the entry maps (canonicalized)
	Store     string // the registry name == subfolder under <central_root>/stores
	StorePath string // the resolved store directory, <central_root>/stores/<Store>
}

// storeNameRe is the store-name grammar (CONFIG-SPEC §3): a single path
// segment, leading alphanumeric (which excludes separators, ".", "..", and
// hidden names), 1–64 characters.
var storeNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// validStoreName reports whether name satisfies the store-name grammar.
func validStoreName(name string) bool { return storeNameRe.MatchString(name) }

// nonAlnumRe matches characters not allowed in a derived ID prefix.
var nonAlnumRe = regexp.MustCompile(`[^a-z0-9]`)

// derivePrefix turns a project path's base name into a valid ID prefix
// (CONFIG-SPEC §5): lowercased, non-alphanumerics stripped, leading digits
// removed, truncated to 8, falling back to "task". Mirrors the CLI's local-init
// derivation so central and local stores derive identically.
func derivePrefix(path string) string {
	base := strings.ToLower(filepath.Base(path))
	base = nonAlnumRe.ReplaceAllString(base, "")
	base = strings.TrimLeft(base, "0123456789")
	if base == "" {
		return "task"
	}
	if len(base) > 8 {
		base = base[:8]
	}
	return base
}

// expandHome replaces a leading "~" (alone or "~/...") with home. Other paths
// are returned unchanged. Pure: no environment access.
func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

// lexCanon lexically canonicalizes path (CONFIG-SPEC §4, the FS-free part):
// expand a leading ~, make absolute by joining a relative path onto base, then
// Clean. Symlink resolution (the existence-dependent part) is applied by the
// shell via the vfs seam.
func lexCanon(path, home, base string) string {
	p := expandHome(path, home)
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}

// isAncestorOrEqual reports whether ancestor is an ancestor of, or equal to,
// descendant, comparing on path-segment boundaries (so "/a/project" is not an
// ancestor of "/a/projectX"). Both inputs must already be clean absolute paths.
func isAncestorOrEqual(ancestor, descendant string) bool {
	if ancestor == descendant {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(ancestor, sep) {
		ancestor += sep
	}
	return strings.HasPrefix(descendant, ancestor)
}

// longestAncestorIndex returns the index of the entry in canonPaths that is the
// longest ancestor-or-equal of canonW (the most specific project), or -1 if
// none match (CONFIG-SPEC §4 step 3).
func longestAncestorIndex(canonW string, canonPaths []string) int {
	best, bestLen := -1, -1
	for i, p := range canonPaths {
		if isAncestorOrEqual(p, canonW) && len(p) > bestLen {
			best, bestLen = i, len(p)
		}
	}
	return best
}
