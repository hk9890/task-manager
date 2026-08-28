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

	// packageIDSep separates the three parts of an effective hook id,
	// "pkg:<package>:<hook>". A declared hook id may not contain it (§3.6), so
	// the three parts can always be told apart in a denial reason.
	packageIDSep = ":"
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
	Version int    `yaml:"version"`
	Hooks   []Hook `yaml:"hooks"`
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
	hasName := strings.TrimSpace(ref.Name) != ""
	hasPath := strings.TrimSpace(ref.Path) != ""
	switch {
	case hasName && hasPath:
		return "", "", fmt.Errorf("use entry: set either name or path, not both (name %q, path %q)", ref.Name, ref.Path)
	case !hasName && !hasPath:
		return "", "", fmt.Errorf("use entry: one of name or path is required")
	case hasName:
		n := strings.TrimSpace(ref.Name)
		if !validPackageName(n) {
			return "", "", fmt.Errorf("use entry: invalid package name %q: one path segment, starting with a letter or digit, at most 64 characters", ref.Name)
		}
		return filepath.Join(home, packagesSubdir, n), n, nil
	default:
		p := strings.TrimSpace(ref.Path)
		if !filepath.IsAbs(p) {
			p = filepath.Join(configDir, p)
		}
		p = filepath.Clean(p)
		n := filepath.Base(p)
		if !validPackageName(n) {
			return "", "", fmt.Errorf("use entry: path %q ends in %q, which is not a usable package name: the directory name is the package name", ref.Path, n)
		}
		return p, n, nil
	}
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
	if a0 != "" && !filepath.IsAbs(a0) && strings.ContainsRune(a0, filepath.Separator) {
		out[0] = filepath.Join(dir, a0)
	}
	return out
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
		if _, _, err := packageDir(ref, "/", "/"); err != nil {
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
