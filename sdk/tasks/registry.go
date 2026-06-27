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
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// This file is the imperative shell for store resolution (CONFIG-SPEC §4–§5):
// the central registry (mapping.yaml), the public Resolve / Stores / InitCentral
// entry points, and the canonicalization that bridges the env/vfs seams to the
// pure matching helpers in resolve.go.

// registryFile is the on-disk shape of mapping.yaml (CONFIG-SPEC §3).
type registryFile struct {
	Version int             `yaml:"version"`
	Stores  []registryEntry `yaml:"stores"`
}

type registryEntry struct {
	Path  string `yaml:"path"`
	Store string `yaml:"store"`
}

// loadRegistry reads <croot>/mapping.yaml and validates it (CONFIG-SPEC §3): a
// missing file is an empty registry (not an error); a corrupt file, a malformed
// entry, an invalid store name, or a duplicate canonical path / store name is an
// error. home is used to expand a leading ~ in entry paths for the uniqueness
// check (matching is lexical here; symlink canonicalization happens at use).
func loadRegistry(fs vfs.FS, croot, home string) ([]registryEntry, error) {
	data, err := fs.ReadFile(filepath.Join(croot, registryFileName))
	if err != nil {
		if vfs.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read registry: %w", err)
	}
	var rf registryFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	seenPath := make(map[string]bool, len(rf.Stores))
	seenStore := make(map[string]bool, len(rf.Stores))
	for _, e := range rf.Stores {
		if e.Path == "" || e.Store == "" {
			return nil, fmt.Errorf("registry: entry missing path or store")
		}
		if !validStoreName(e.Store) {
			return nil, fmt.Errorf("registry: invalid store name %q", e.Store)
		}
		cp := lexCanon(e.Path, home, croot)
		if seenPath[cp] {
			return nil, fmt.Errorf("registry: duplicate path %q", e.Path)
		}
		if seenStore[e.Store] {
			return nil, fmt.Errorf("registry: duplicate store name %q", e.Store)
		}
		seenPath[cp] = true
		seenStore[e.Store] = true
	}
	return rf.Stores, nil
}

// loadCentral resolves the home and central root and loads (and validates) the
// registry — the shared prelude for the central paths of Resolve and Stores.
func loadCentral(fs vfs.FS, e env.Environment) (home, croot string, entries []registryEntry, err error) {
	home, err = taskmgrHome(e)
	if err != nil {
		return "", "", nil, err
	}
	gcfg, err := loadGlobalConfig(fs, home)
	if err != nil {
		return "", "", nil, err
	}
	croot = centralRoot(gcfg, home)
	entries, err = loadRegistry(fs, croot, home)
	if err != nil {
		return "", "", nil, err
	}
	return home, croot, entries, nil
}

// Resolve maps the working directory (and any override in opts) to a single open
// store, reporting how it was chosen (CONFIG-SPEC §4). It is the one entry point
// front ends call; the CLI is a thin wrapper. Returns ErrNoStore when nothing
// resolves.
func Resolve(opts ResolveOptions, sopts ...Option) (*Store, ResolveInfo, error) {
	return resolveWith(opts, vfs.NewOS(), env.NewOS(), sopts)
}

// resolveWith is Resolve with injectable seams, for hermetic tests.
func resolveWith(opts ResolveOptions, fs vfs.FS, e env.Environment, sopts []Option) (*Store, ResolveInfo, error) {
	// 1. Explicit override.
	storePath := opts.StorePath
	if storePath == "" {
		storePath = e.Getenv(envTaskmgrDir)
	}
	if storePath != "" && opts.StoreName != "" {
		return nil, ResolveInfo{}, ErrAmbiguousOverride
	}
	if storePath != "" {
		dir, err := absViaSeam(storePath, fs)
		if err != nil {
			return nil, ResolveInfo{}, err
		}
		s, err := openData(filepath.Dir(dir), dir, fs, sopts)
		if err != nil {
			return nil, ResolveInfo{}, err
		}
		return s, ResolveInfo{Kind: ResolvedOverridePath, StorePath: dir, ProjectPath: s.root}, nil
	}

	// store-name override: open the named central store via the registry.
	if opts.StoreName != "" {
		home, croot, entries, err := loadCentral(fs, e)
		if err != nil {
			return nil, ResolveInfo{}, err
		}
		for _, en := range entries {
			if en.Store != opts.StoreName {
				continue
			}
			dir := filepath.Join(croot, storesSubdir, en.Store)
			project := canonicalize(fs, en.Path, home, croot)
			s, err := openData(project, dir, fs, sopts)
			if err != nil {
				return nil, ResolveInfo{}, err
			}
			return s, ResolveInfo{Kind: ResolvedOverrideName, StorePath: dir, ProjectPath: project}, nil
		}
		return nil, ResolveInfo{}, ErrStoreNotRegistered
	}

	// 2. Local walk-up (the common path — touches no global config).
	start, err := resolutionOrigin(opts.WorkDir, fs)
	if err != nil {
		return nil, ResolveInfo{}, err
	}
	if root, dir, found, err := findLocalStore(fs, start); err != nil {
		return nil, ResolveInfo{}, err
	} else if found {
		s, err := openData(root, dir, fs, sopts)
		if err != nil {
			return nil, ResolveInfo{}, err
		}
		return s, ResolveInfo{Kind: ResolvedLocal, StorePath: dir, ProjectPath: root}, nil
	}

	// 3. Central fallback (only now do we read the global config + registry).
	home, croot, entries, err := loadCentral(fs, e)
	if err != nil {
		return nil, ResolveInfo{}, err
	}
	canonW := canonicalize(fs, start, home, start)
	var canonPaths []string
	var kept []registryEntry
	for _, en := range entries {
		dir := filepath.Join(croot, storesSubdir, en.Store)
		if fi, statErr := fs.Stat(dir); statErr != nil || !fi.IsDir() {
			continue // dangling: store subfolder missing — skip (CONFIG-SPEC §3)
		}
		canonPaths = append(canonPaths, canonicalize(fs, en.Path, home, croot))
		kept = append(kept, en)
	}
	idx := longestAncestorIndex(canonW, canonPaths)
	if idx < 0 {
		return nil, ResolveInfo{}, ErrNoStore
	}
	en := kept[idx]
	dir := filepath.Join(croot, storesSubdir, en.Store)
	project := canonPaths[idx]
	s, err := openData(project, dir, fs, sopts)
	if err != nil {
		return nil, ResolveInfo{}, err
	}
	return s, ResolveInfo{Kind: ResolvedCentral, StorePath: dir, ProjectPath: project}, nil
}

// Stores returns the central registry entries (CONFIG-SPEC §4, SDK-SPEC §1). It
// does not resolve against a working directory; it reads through the seams and
// never writes. A missing registry yields an empty slice; a corrupt one an error.
func Stores(opts ResolveOptions) ([]StoreEntry, error) {
	return storesWith(vfs.NewOS(), env.NewOS())
}

func storesWith(fs vfs.FS, e env.Environment) ([]StoreEntry, error) {
	home, croot, entries, err := loadCentral(fs, e)
	if err != nil {
		return nil, err
	}
	out := make([]StoreEntry, 0, len(entries))
	for _, en := range entries {
		out = append(out, StoreEntry{
			Path:      canonicalize(fs, en.Path, home, croot),
			Store:     en.Store,
			StorePath: filepath.Join(croot, storesSubdir, en.Store),
		})
	}
	return out, nil
}

// InitCentral creates a central store at <central_root>/stores/<name> and
// registers it for projectPath, in one locked operation (CONFIG-SPEC §5).
func InitCentral(projectPath, name, prefix string, opts ...Option) (*Store, error) {
	return initCentralWith(projectPath, name, prefix, vfs.NewOS(), env.NewOS(), opts)
}

func initCentralWith(projectPath, name, prefix string, fs vfs.FS, e env.Environment, opts []Option) (*Store, error) {
	if !validStoreName(name) {
		return nil, fmt.Errorf("invalid store name %q: must match the store-name grammar (CONFIG-SPEC §3)", name)
	}
	home, err := taskmgrHome(e)
	if err != nil {
		return nil, err
	}
	gcfg, err := loadGlobalConfig(fs, home)
	if err != nil {
		return nil, err
	}
	croot := centralRoot(gcfg, home)
	project := canonicalize(fs, projectPath, home, croot)

	// Serialize registry writes under the central-root lock (CONFIG-SPEC §3/§5).
	if err := fs.MkdirAll(croot, 0o755); err != nil {
		return nil, err
	}
	unlock, err := fs.Lock(filepath.Join(croot, centralLockName))
	if err != nil {
		return nil, err
	}
	defer func() { _ = unlock() }()

	entries, err := loadRegistry(fs, croot, home)
	if err != nil {
		return nil, err
	}
	projKey := lexCanon(projectPath, home, croot)
	for _, en := range entries {
		if en.Store == name {
			return nil, ErrStoreExists
		}
		if lexCanon(en.Path, home, croot) == projKey {
			return nil, fmt.Errorf("a central store is already registered for %q", project)
		}
	}

	if strings.TrimSpace(prefix) == "" {
		prefix = derivePrefix(project)
	}
	dir := filepath.Join(croot, storesSubdir, name)
	s, err := initData(project, dir, prefix, fs, opts)
	if err != nil {
		return nil, err
	}

	// Append the entry and write the registry atomically.
	rf := registryFile{Version: 1}
	rf.Stores = append(rf.Stores, entries...)
	rf.Stores = append(rf.Stores, registryEntry{Path: project, Store: name})
	out, err := yaml.Marshal(rf)
	if err != nil {
		return nil, err
	}
	if err := fs.WriteAtomic(filepath.Join(croot, registryFileName), out, 0o644); err != nil {
		return nil, err
	}
	return s, nil
}

// resolutionOrigin returns the absolute, cleaned resolution origin W: workDir if
// given (made absolute against the cwd when relative), else the cwd.
func resolutionOrigin(workDir string, fs vfs.FS) (string, error) {
	if workDir == "" {
		return absViaSeam(".", fs)
	}
	return absViaSeam(workDir, fs)
}

// absViaSeam makes p absolute and clean, using the vfs seam for the cwd (rather
// than os.Getwd) so resolution stays hermetically testable.
func absViaSeam(p string, fs vfs.FS) (string, error) {
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	wd, err := fs.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(wd, p)), nil
}

// canonicalize applies the full path canonicalization of CONFIG-SPEC §4: the
// lexical part (expand ~, make absolute against base, clean) followed by symlink
// resolution via the seam where the path exists, falling back to the lexical
// form otherwise.
func canonicalize(fs vfs.FS, raw, home, base string) string {
	p := lexCanon(raw, home, base)
	if resolved, err := fs.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}
