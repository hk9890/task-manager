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

// packages.go — the hook-package format (HOOK-SPEC §3.6), as pure core.
//
// A package is a directory holding a manifest and the scripts its hooks run. A
// config file does not declare hooks; it lists the packages it uses, and the
// hooks come from those (CONFIG-SPEC §2, TASK-STORAGE-SPEC §4.2).
//
// Everything here maps values to values: manifest bytes to hooks, a reference
// plus two directories to a package directory. Reading a directory is the shell's
// job and lives in packageload.go.
package tasks

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// PackageManifestName is the file that makes a directory a hook package.
	PackageManifestName = "taskmgr-package.yaml"

	// packagesSubdir holds machine-wide packages under the taskmgr home, so a
	// `use: - name:` reference resolves to <home>/packages/<name>.
	packagesSubdir = "packages"

	// packageSchemaVersion is the manifest schema this build reads (§3.6).
	packageSchemaVersion = 1

	// packageIDSep separates the three parts of an effective hook id,
	// "pkg:<package>:<hook>". A declared hook id may not contain it (§3.6), so
	// the three parts can always be told apart in a denial reason.
	packageIDSep = ":"

	// MaxGuideFragmentBytes caps one guide fragment (§3.7). The text is written
	// verbatim into an agent's instructions, so an uncapped fragment lets one
	// package spend the caller's whole context. A fragment over the cap is
	// truncated at the last line break under it and marked, never dropped: a
	// truncated section still teaches, and a silently absent one does not.
	MaxGuideFragmentBytes = 8 << 10

	// MaxGuideOverviewBytes caps a package's overview fragment — eight times
	// tighter than a topic, because this text lands in *every* caller's context
	// rather than in the ones that asked for the subject. The cap is the design:
	// an overview fragment has room to say what this store expects and which
	// topic states it, and no room to state it here.
	MaxGuideOverviewBytes = 1 << 10

	// GuideOverviewID is the reserved fragment id the `overview:` manifest key
	// declares. A `guide:` entry may not claim it, so "pkg:<package>:overview"
	// always names the fragment that reaches the overview.
	GuideOverviewID = "overview"
)

// PackageRef is one entry of a config file's `use:` list: the package a
// configuration takes its hooks from. Exactly one of Name and Path is set.
//
// Name resolves to <home>/packages/<name> and is therefore machine-independent
// — a store's config can carry it into git and every machine finds its own copy.
// Path resolves against the directory holding the config file that declares it,
// so a package committed inside a store travels with the store.
//
// Which of the two is meant is always stated, never inferred from the string
// (CONFIG-SPEC's rule for naming a store, applied to naming a package).
type PackageRef struct {
	Name string `yaml:"name,omitempty"`
	Path string `yaml:"path,omitempty"`

	// malformed holds an entry that is not a mapping at all — `- doc-policy`
	// rather than `- name: doc-policy`, which is the obvious thing to write and
	// which the old `hooks:` block never invited — or an empty `-` with nothing
	// under it. Rejecting either in the decoder would fail *every* command, reads
	// included, and take both documented recovery paths (`list`, `package list`)
	// with it. It is carried instead and reported where every other bad entry is:
	// at the write (HOOK-SPEC §3.4).
	malformed string

	// raw is the entry's original node, kept for a malformed entry only. An entry
	// this build cannot model is written back from it verbatim, so a write that
	// touches an unrelated entry rewrites nothing here — not the text, not the
	// comment beside it.
	raw *yaml.Node
}

// emptyUseEntry labels a `- ` with nothing under it. yaml.v3 drops a null
// sequence element before any Unmarshaler runs, so such an entry never reaches
// UnmarshalYAML: decodeUse recognises it from the node instead.
const emptyUseEntry = "(empty entry)"

// UnmarshalYAML accepts a mapping, and records anything else rather than failing
// the decode, so a hand-edited config file stays readable (§3.4).
func (r *PackageRef) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		r.malformed = node.Value
		if r.malformed == "" {
			r.malformed = "(not a mapping)"
		}
		r.raw = node
		return nil
	}
	type plain struct {
		Name string `yaml:"name,omitempty"`
		Path string `yaml:"path,omitempty"`
	}
	var v plain
	if err := node.Decode(&v); err != nil {
		return err
	}
	r.Name, r.Path = v.Name, v.Path
	return nil
}

// MarshalYAML writes a carried-through malformed entry back exactly as it was
// read, so a write that touches an unrelated entry does not silently rewrite it.
func (r PackageRef) MarshalYAML() (any, error) {
	if r.raw != nil {
		return r.raw, nil
	}
	if r.malformed != "" {
		return r.malformed, nil
	}
	type plain struct {
		Name string `yaml:"name,omitempty"`
		Path string `yaml:"path,omitempty"`
	}
	return plain{Name: r.Name, Path: r.Path}, nil
}

// The two package keys of a config file. `use:` is the list this build reads;
// `hooks:` is the withdrawn inline block it replaced (§3, "Why a package and not
// an inline block").
const (
	useKey    = "use"
	hooksKey  = "hooks"
	hooksHelp = "the `hooks:` key was withdrawn: a configuration no longer declares hooks, it lists the packages it takes them from. " +
		"Move each entry into a package directory (HOOK-SPEC §3.6), add it with `taskmgr package add`, then delete the `hooks:` block from this file"
)

// configDefect is something wrong with a config file's own package configuration
// — a key, as opposed to one `use:` entry.
//
// It is carried rather than returned as a decode error for the reason §3.4 gives
// for every other package fault: a config file is read on every command, so
// failing the decode stops reads too, and with them `list`, `where` and
// `package list` — the commands a reader reaches for precisely when writes have
// stopped. A defect instead fails every *mutation* closed and prints as a row of
// `taskmgr package list`.
type configDefect struct {
	// key is the config key at fault, spelled as it appears in the file.
	key string
	// detail says what is wrong and what to do about it.
	detail string
}

// decodeConfigPackages pulls the package configuration out of a config-file
// mapping and returns the mapping with `use:` removed, so the caller can decode
// the remaining keys through their struct tags.
//
// `use:` is modelled by hand rather than by a struct tag because yaml.v3 handles
// two hand-edit shapes in ways a config file cannot afford:
//
//   - a value that is not a sequence (`use: doc-policy`) fails the decode of the
//     whole document, which takes every command in the store down with it;
//   - a null element (a bare `-`) is dropped before any Unmarshaler runs, so it
//     is absent from the model and the next unrelated write deletes it from the
//     file.
//
// Both are carried instead: the first as a defect, the second as a malformed
// entry that is reported at the write and written back verbatim.
func decodeConfigPackages(node *yaml.Node) (rest *yaml.Node, use []PackageRef, defects []configDefect) {
	if node.Kind != yaml.MappingNode {
		return node, nil, nil
	}
	out := *node
	out.Content = nil
	var useNode *yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Value == useKey {
			useNode = value
			continue
		}
		if key.Value == hooksKey {
			defects = append(defects, configDefect{key: hooksKey, detail: hooksHelp})
		}
		out.Content = append(out.Content, key, value)
	}
	use, defect := decodeUse(useNode)
	if defect != nil {
		defects = append(defects, *defect)
	}
	return &out, use, defects
}

// decodeUse models one `use:` value node. A value that is not a sequence is not
// a list of entries and cannot be modelled at all, so it is reported as a defect
// rather than as a list of one bad entry: the fault is the key's, and the fix is
// to write the value as a list.
func decodeUse(node *yaml.Node) ([]PackageRef, *configDefect) {
	if node == nil || node.Tag == nullTag {
		return nil, nil // absent, or `use:` with nothing under it — an empty list
	}
	if node.Kind != yaml.SequenceNode {
		return nil, &configDefect{
			key: useKey,
			detail: fmt.Sprintf("the `use:` key must hold a list of package entries, not a single value; write it as `use:` followed by `  - name: %s`",
				firstNonEmpty(node.Value, "doc-policy")),
		}
	}
	refs := make([]PackageRef, 0, len(node.Content))
	for _, el := range node.Content {
		var r PackageRef
		if el.Tag == nullTag {
			r.malformed, r.raw = emptyUseEntry, el
		} else if err := el.Decode(&r); err != nil {
			r.malformed, r.raw = strings.TrimSpace(err.Error()), el
		}
		refs = append(refs, r)
	}
	return refs, nil
}

// nullTag is yaml.v3's resolved tag for an empty value.
const nullTag = "!!null"

// hasDefect reports whether one config key is among a file's package defects.
func hasDefect(defects []configDefect, key string) bool {
	for _, d := range defects {
		if d.key == key {
			return true
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// packageManifest is taskmgr-package.yaml. Unknown keys are ignored for
// forward-compatibility, as in a config file.
//
// There is deliberately no `name` key: the directory name is the package name,
// so a copy that lands in a differently-named directory cannot disagree with its
// own manifest. And no `hook_timeout`: a package that could raise the limit could
// extend how long the store lock is held, for every project on the machine
// (HOOK-SPEC §8).
type packageManifest struct {
	Version  int          `yaml:"version"`
	Hooks    []Hook       `yaml:"hooks"`
	Guide    []GuideEntry `yaml:"guide"`
	Overview string       `yaml:"overview"`
}

// GuideEntry is one guide fragment a package contributes (HOOK-SPEC §3.7): a
// Markdown file whose text `taskmgr guide` appends to its own, so the package
// that enforces a convention with a hook can also state it in prose.
//
// A gate teaches by refusing: the agent files, is denied, reads the reason and
// retries. That loop works, and it costs a round trip per rule. A fragment
// states the rule before the first attempt, from the same directory and the same
// version as the hook that enforces it, so the two cannot drift apart.
type GuideEntry struct {
	// ID is the fragment's label within its package. Required, must not contain
	// ':', and unique within the manifest — the same rules a hook id follows,
	// for the same reason: the effective topic "pkg:<package>:<id>" is what a
	// caller names to select it, so it must keep meaning the same fragment when
	// the package is replaced.
	ID string `yaml:"id,omitempty"`
	// File is the fragment, as a path inside the package directory.
	File string `yaml:"file"`
	// Into optionally names a built-in guide topic that this fragment prints
	// inside, after that topic's own text. It is how a package adds its rules to
	// a job the tool already defines, rather than making the caller fetch a
	// second topic it had no reason to know exists. Empty leaves the fragment
	// reachable only by its own effective topic id.
	//
	// Whether the named topic exists is deliberately **not** decided here. A
	// manifest is parsed on the write path, so a package naming a topic this
	// binary has since renamed would become broken — and a broken package runs
	// no hooks, which would turn a documentation mismatch into refused writes.
	// The guide resolves the target when it prints, and reports one it cannot
	// place (HOOK-SPEC §3.7).
	Into string `yaml:"into,omitempty"`
}

// packageGuide is one guide fragment read out of a manifest: the effective topic
// id, and the fragment's path already resolved against the package directory.
type packageGuide struct {
	id   string
	path string
	// overview marks the fragment the `overview:` key declared. It reaches the
	// guide's overview rather than waiting to be asked for, and is capped at
	// MaxGuideOverviewBytes instead of MaxGuideFragmentBytes.
	overview bool
	// into is the built-in topic the fragment prints inside, empty when the entry
	// declared none. It is carried verbatim: this package cannot know which
	// topics the binary defines.
	into string
}

// guideFromManifest turns a parsed manifest into the guide fragments it
// contributes, each id validated and each path resolved against dir.
//
// Only the entries are checked here; the files themselves are read when a caller
// asks for the guide (packageload.go). A manifest is parsed on the write path,
// where a fragment's *content* is nothing a mutation depends on — reading one
// there would be I/O no write needs, and would make an unreadable document able
// to stop a write.
func guideFromManifest(m packageManifest, name, dir string) ([]packageGuide, error) {
	out := make([]packageGuide, 0, len(m.Guide)+1)
	seen := make(map[string]bool, len(m.Guide)+1)

	// The overview fragment comes first, so a reader of the whole set meets what
	// the store expects before the sections that expand on it.
	if ov := strings.TrimSpace(m.Overview); ov != "" {
		path, err := resolveGuideFile(ov, dir)
		if err != nil {
			return nil, fmt.Errorf("package %s: overview: %w", name, err)
		}
		seen[GuideOverviewID] = true
		out = append(out, packageGuide{id: packageGuideID(name, GuideOverviewID), path: path, overview: true})
	}

	for i, g := range m.Guide {
		declared := strings.TrimSpace(g.ID)
		if declared == "" {
			return nil, fmt.Errorf("package %s: guide #%d: id is required (a guide entry has no positional default id)", name, i)
		}
		if declared == GuideOverviewID {
			return nil, fmt.Errorf("package %s: guide id %q is reserved for the overview: key, which declares the fragment printed in the guide's overview", name, GuideOverviewID)
		}
		if strings.Contains(declared, packageIDSep) {
			return nil, fmt.Errorf("package %s: guide %q: id must not contain %q (it separates the parts of the effective topic %q)",
				name, declared, packageIDSep, packageGuideID(name, "<id>"))
		}
		if seen[declared] {
			return nil, fmt.Errorf("package %s: guide id %q is declared twice", name, declared)
		}
		seen[declared] = true

		// Only the shape of `into` is checked here — a ':' would make it read as
		// an effective topic id, which it is not. The target itself is resolved
		// by the guide, for the reason GuideEntry.Into states.
		into := strings.TrimSpace(g.Into)
		if strings.Contains(into, packageIDSep) {
			return nil, fmt.Errorf("package %s: guide %q: into %q must not contain %q (it names one built-in topic, not an effective topic id)",
				name, declared, into, packageIDSep)
		}

		path, err := resolveGuideFile(g.File, dir)
		if err != nil {
			return nil, fmt.Errorf("package %s: guide %q: %w", name, declared, err)
		}
		out = append(out, packageGuide{id: packageGuideID(name, declared), path: path, into: into})
	}
	return out, nil
}

// resolveGuideFile resolves a fragment's `file` against the package directory.
//
// Unlike a hook's argv[0], a fragment path has no PATH-lookup meaning and no
// reason to name anything outside the package: it is a document the package
// ships. So an absolute path is refused rather than honoured — it is
// machine-specific, which is the one thing the package format exists to avoid —
// and so is a relative path that climbs out of the directory.
func resolveGuideFile(file, dir string) (string, error) {
	f := strings.TrimSpace(file)
	if f == "" {
		return "", fmt.Errorf("file is required")
	}
	if isAbsAnyPlatform(f) {
		return "", fmt.Errorf("file %q is an absolute path; a guide fragment is a file inside the package", file)
	}
	clean := filepath.Clean(filepath.FromSlash(f))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file %q leaves the package directory", file)
	}
	return filepath.Join(dir, clean), nil
}

// packageGuideID composes the effective topic id a caller names to select one
// package's fragment. It is the hook id's shape — "pkg:<package>:<id>" — so a
// denial reason and a guide topic spell the same package the same way.
func packageGuideID(pkg, id string) string {
	return "pkg" + packageIDSep + pkg + packageIDSep + id
}

// packageHook is one hook read out of a package: the entry with its argv already
// resolved against the package directory, and the effective id it runs under.
type packageHook struct {
	id   string
	hook Hook
}

// validPackageName reports whether name is a legal package name. It is the
// store-name grammar (CONFIG-SPEC §3) — one path segment, leading alphanumeric,
// 1–64 characters — because the constraint is the same one: the value is used as
// a directory name under a shared parent, so it must not reach a sibling
// directory or hide itself.
func validPackageName(name string) bool { return validStoreName(name) }

// packageDir resolves one `use:` entry to the directory the package lives in,
// and to the name it is known by.
//
// home is the taskmgr home (for a Name reference) and configDir is the directory
// holding the config file the entry was read from (for a Path reference): the
// store's data directory for a store config, the home for the per-user one. A
// package referenced by path from a store config therefore sits inside the store
// and survives `store move --central`, which moves the store whole.
func packageDir(ref PackageRef, home, configDir string) (dir, name string, err error) {
	if err := refShape(ref); err != nil {
		return "", "", err
	}
	if n := strings.TrimSpace(ref.Name); n != "" {
		// A name is only ever <home>/packages/<name>. With no locatable home
		// there is no such directory, and joining an empty home would yield the
		// *relative* "packages/<name>" — which resolves against whatever
		// directory the process happens to be in, so taskmgr would load and run
		// hooks from a path any local process can plant. That is a hard error,
		// not a fallback.
		if strings.TrimSpace(home) == "" {
			return "", "", fmt.Errorf("use entry: package %q is named by name, but no taskmgr home could be located (set $TASKMGR_HOME or $HOME)", n)
		}
		return filepath.Join(home, packagesSubdir, n), n, nil
	}

	// refShape has already refused an absolute path, so every path that reaches
	// here resolves against the directory holding the file that declared it.
	p := strings.TrimSpace(ref.Path)
	if strings.TrimSpace(configDir) == "" {
		return "", "", fmt.Errorf("use entry: path %q has no directory to resolve against", ref.Path)
	}
	p = filepath.Clean(filepath.Join(configDir, p))
	return p, filepath.Base(p), nil
}

// refShape checks a `use:` entry on its own, without resolving it against any
// directory: exactly one of name and path, a legal name, and a relative path
// that stays inside the file's own directory.
//
// It is what a write checks before the entry lands (checkUseChange), so it must
// not depend on where the file happens to be — a store config travels in git and
// is legitimately written on a machine where the package is not installed.
func refShape(ref PackageRef) error {
	if ref.malformed == emptyUseEntry {
		return fmt.Errorf("use entry: an entry is empty; each entry is a mapping with either name or path, e.g. `- name: doc-policy`")
	}
	if ref.malformed != "" {
		return fmt.Errorf("use entry %q: each entry is a mapping with either name or path, e.g. `- name: %s`", ref.malformed, ref.malformed)
	}
	name := strings.TrimSpace(ref.Name)
	path := strings.TrimSpace(ref.Path)
	switch {
	case name != "" && path != "":
		return fmt.Errorf("use entry: set either name or path, not both (name %q, path %q)", ref.Name, ref.Path)
	case name == "" && path == "":
		return fmt.Errorf("use entry: one of name or path is required")
	case name != "":
		if !validPackageName(name) {
			return fmt.Errorf("use entry: invalid package name %q: one path segment, starting with a letter or digit, at most 64 characters", ref.Name)
		}
		return nil
	}

	// A path entry must stay inside the directory holding the config file. The
	// promise it makes is that the package travels with the file (HOOK-SPEC
	// §3.5) — an entry reaching outside breaks for every colleague the moment the
	// file is committed, and for the author on `store move --central`.
	//
	// An absolute path is refused for the same reason a guide fragment's is
	// (resolveGuideFile): it is machine-specific, which is the one thing the
	// package format exists to avoid. It also read as the sanctioned way out —
	// the message here used to recommend it — and a store config carrying
	// `/home/me/pkg` resolves to nothing on every clone, which is `missing`,
	// which fails every mutation in that repository. A package that lives
	// elsewhere is named by `name:` and installed under the taskmgr home.
	//
	// isAbsAnyPlatform, not filepath.IsAbs: a config file is written once and
	// read on every platform, so "/opt/pkg" has to be refused on Windows too.
	if isAbsAnyPlatform(path) {
		return fmt.Errorf("use entry: path %q is an absolute path; a package path is relative to the directory holding this config file, so that the package travels with it (use `name:` for a package installed under the taskmgr home)", ref.Path)
	}
	clean := filepath.Clean(path)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("use entry: path %q leaves the directory holding this config file; a package path must stay inside it (use `name:` for a package installed under the taskmgr home)", ref.Path)
	}
	if base := filepath.Base(filepath.Clean(path)); !validPackageName(base) {
		return fmt.Errorf("use entry: path %q ends in %q, which is not a usable package name: the directory name is the package name", ref.Path, base)
	}
	return nil
}

// parsePackageManifest decodes a manifest. dir names the package in the error,
// because the caller reached it through a reference and not by typing the path.
func parsePackageManifest(data []byte, dir string) (packageManifest, error) {
	var m packageManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return packageManifest{}, fmt.Errorf("package %s: parse %s: %w", dir, PackageManifestName, err)
	}
	return m, nil
}

// hooksFromManifest turns a parsed manifest into the hooks it contributes: each
// entry validated, its effective id composed, and its argv resolved against dir.
//
// A hook here must declare an id. There is no positional default: a package is
// replaced wholesale when it is updated, and a defaulted "<event>#<index>" would
// silently renumber onto a different hook when an entry is added above it, so
// the id in a denial reason would stop naming the same gate.
func hooksFromManifest(m packageManifest, name, dir string) ([]packageHook, error) {
	// An unset version is 1 (§3.6). A version this build does not know is a hard
	// error rather than a best-effort read: the manifest describes gates, so
	// running a later format under v1 rules is the one outcome the fail-closed
	// treatment everywhere else exists to prevent.
	if v := m.Version; v != 0 && v != packageSchemaVersion {
		return nil, fmt.Errorf("package %s: manifest version %d is not supported by this taskmgr (expected %d)", name, v, packageSchemaVersion)
	}
	out := make([]packageHook, 0, len(m.Hooks))
	seen := make(map[string]bool, len(m.Hooks))
	for i, h := range m.Hooks {
		declared := strings.TrimSpace(h.ID)
		if declared == "" {
			return nil, fmt.Errorf("package %s: hook #%d: id is required (a package hook has no positional default id)", name, i)
		}
		if strings.Contains(declared, packageIDSep) {
			return nil, fmt.Errorf("package %s: hook %q: id must not contain %q (it separates the parts of the effective id %q)",
				name, declared, packageIDSep, packageHookID(name, "<id>"))
		}
		if seen[declared] {
			return nil, fmt.Errorf("package %s: hook id %q is declared twice", name, declared)
		}
		seen[declared] = true

		h.ID = declared
		h.Run = resolveHookArgv(h.Run, dir)
		out = append(out, packageHook{id: packageHookID(name, declared), hook: h})
	}
	return out, nil
}

// packageHookID composes the effective id a denial reason reports and
// `taskmgr hook list` prints.
func packageHookID(pkg, hook string) string {
	return "pkg" + packageIDSep + pkg + packageIDSep + hook
}

// resolveHookArgv rewrites a relative argv[0] to sit inside the package
// directory, so a package can name the script it ships without knowing where it
// was installed. The working directory of the hook process is unchanged — it is
// still the project root (HOOK-SPEC §3.2) — and only argv[0] is touched; every
// other argument is passed through verbatim.
//
// A first element with no path separator is left alone: that is a PATH lookup,
// and rewriting it would turn the documented ["sh", "-c", …] idiom into a search
// for a shell inside the package. The rule is exactly execve's own.
func resolveHookArgv(run []string, dir string) []string {
	if len(run) == 0 {
		return run
	}
	out := append([]string(nil), run...)
	a0 := out[0]
	// '/' as well as the platform separator: a manifest is written once and read
	// on every platform, and the documented form is "./hooks/x.sh". Testing only
	// filepath.Separator would leave every such package broken on Windows, which
	// is a release target — the exact failure the rule exists to prevent.
	if a0 != "" && !isAbsAnyPlatform(a0) && (strings.ContainsRune(a0, '/') || strings.ContainsRune(a0, filepath.Separator)) {
		out[0] = filepath.Join(dir, a0)
	}
	return out
}

// isAbsAnyPlatform reports whether a0 is absolute under this platform's rule or
// as a POSIX path. A manifest written on Linux carries "/usr/bin/gate", which
// filepath.IsAbs rejects on Windows — joining it into the package directory
// would be worse than leaving it alone.
func isAbsAnyPlatform(a0 string) bool {
	return filepath.IsAbs(a0) || strings.HasPrefix(a0, "/")
}

// clonePackageRefs copies a `use:` list so a caller editing the result cannot
// reach the one a Store is already running with.
func clonePackageRefs(refs []PackageRef) []PackageRef {
	if refs == nil {
		return nil
	}
	return append([]PackageRef(nil), refs...)
}

// checkUseChange validates the entries a write introduces, so a reference that
// could never load is refused at the command that added it rather than at the
// next unrelated mutation (HOOK-SPEC §3.4).
//
// Only the delta is checked, and deliberately: validating the whole list would
// make a bad entry already on disk refuse the write that removes it — which is
// `taskmgr package rm`, the one way back out of a configuration that has stopped
// every mutation. It checks
// the shape of a reference, not that the package is there — a store's config
// travels in git, so an entry is legitimately unresolvable on the machine that
// writes it and only has to resolve on the machine that runs a mutation.
func checkUseChange(cur, next []PackageRef) error {
	for _, ref := range next {
		if containsRef(cur, ref) {
			continue
		}
		if err := refShape(ref); err != nil {
			return err
		}
	}
	return nil
}

// containsRef reports whether ref is already one of refs. The malformed text is
// part of the comparison as well as name and path: two entries a hand edit left
// unmodellable both have an empty name and an empty path, so comparing only
// those would call any one of them "already present" and let a write introduce a
// second one unchecked. Nothing can introduce one today — malformed is set by
// the decoder alone — which is exactly why the guard is cheap to keep honest.
func containsRef(refs []PackageRef, ref PackageRef) bool {
	for _, c := range refs {
		if c.Name == ref.Name && c.Path == ref.Path && c.malformed == ref.malformed {
			return true
		}
	}
	return false
}
