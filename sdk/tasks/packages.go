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
	// which the old `hooks:` block never invited. Rejecting it in the decoder
	// would fail *every* command, reads included, and take both documented
	// recovery paths (`list`, `package list`) with it. It is carried instead and
	// reported where every other bad entry is: at the write (HOOK-SPEC §3.4).
	malformed string
}

// UnmarshalYAML accepts a mapping, and records anything else rather than failing
// the decode, so a hand-edited config file stays readable (§3.4).
func (r *PackageRef) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		r.malformed = node.Value
		if r.malformed == "" {
			r.malformed = "(not a mapping)"
		}
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
	if r.malformed != "" {
		return r.malformed, nil
	}
	type plain struct {
		Name string `yaml:"name,omitempty"`
		Path string `yaml:"path,omitempty"`
	}
	return plain{Name: r.Name, Path: r.Path}, nil
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
	Version int          `yaml:"version"`
	Hooks   []Hook       `yaml:"hooks"`
	Guide   []GuideEntry `yaml:"guide"`
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
}

// packageGuide is one guide fragment read out of a manifest: the effective topic
// id, and the fragment's path already resolved against the package directory.
type packageGuide struct {
	id   string
	path string
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
	out := make([]packageGuide, 0, len(m.Guide))
	seen := make(map[string]bool, len(m.Guide))
	for i, g := range m.Guide {
		declared := strings.TrimSpace(g.ID)
		if declared == "" {
			return nil, fmt.Errorf("package %s: guide #%d: id is required (a guide entry has no positional default id)", name, i)
		}
		if strings.Contains(declared, packageIDSep) {
			return nil, fmt.Errorf("package %s: guide %q: id must not contain %q (it separates the parts of the effective topic %q)",
				name, declared, packageIDSep, packageGuideID(name, "<id>"))
		}
		if seen[declared] {
			return nil, fmt.Errorf("package %s: guide id %q is declared twice", name, declared)
		}
		seen[declared] = true

		path, err := resolveGuideFile(g.File, dir)
		if err != nil {
			return nil, fmt.Errorf("package %s: guide %q: %w", name, declared, err)
		}
		out = append(out, packageGuide{id: packageGuideID(name, declared), path: path})
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

	p := strings.TrimSpace(ref.Path)
	if !filepath.IsAbs(p) {
		if strings.TrimSpace(configDir) == "" {
			return "", "", fmt.Errorf("use entry: relative path %q has no directory to resolve against", ref.Path)
		}
		p = filepath.Join(configDir, p)
	}
	p = filepath.Clean(p)
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

	// A relative path must stay inside the directory holding the config file.
	// The promise a path entry makes is that the package travels with the file
	// (HOOK-SPEC §3.5) — an entry reaching outside breaks for every colleague the
	// moment the file is committed, and for the author on `store move --central`.
	if !filepath.IsAbs(path) {
		clean := filepath.Clean(path)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("use entry: path %q leaves the directory holding this config file; a relative package path must stay inside it (use an absolute path for one that lives elsewhere)", ref.Path)
		}
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
// make a bad entry already on disk refuse the write that removes it. It checks
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

func containsRef(refs []PackageRef, ref PackageRef) bool {
	for _, c := range refs {
		if c.Name == ref.Name && c.Path == ref.Path {
			return true
		}
	}
	return false
}
