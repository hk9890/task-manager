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

// centralPaths resolves the home and the central root through the seams
// (CONFIG-SPEC §1–§2) without reading the registry.
func centralPaths(fs vfs.FS, e env.Environment) (home, croot string, err error) {
	home, err = taskmgrHome(e)
	if err != nil {
		return "", "", err
	}
	gcfg, err := loadGlobalConfig(fs, home)
	if err != nil {
		return "", "", err
	}
	return home, centralRoot(gcfg, home), nil
}

// loadCentral resolves the home and central root and loads (and validates) the
// registry — the shared prelude for the central paths of Resolve and Stores.
func loadCentral(fs vfs.FS, e env.Environment) (home, croot string, entries []registryEntry, err error) {
	home, croot, err = centralPaths(fs, e)
	if err != nil {
		return "", "", nil, err
	}
	entries, err = loadRegistry(fs, croot, home)
	if err != nil {
		return "", "", nil, err
	}
	return home, croot, entries, nil
}

// saveRegistry serializes entries to <croot>/mapping.yaml atomically. Callers
// must already hold the central-root lock (CONFIG-SPEC §3/§5).
func saveRegistry(fs vfs.FS, croot string, entries []registryEntry) error {
	out, err := yaml.Marshal(registryFile{Version: 1, Stores: entries})
	if err != nil {
		return err
	}
	return fs.WriteAtomic(filepath.Join(croot, registryFileName), out, 0o644)
}

// lockCentral prepares the central root and takes its advisory lock, so registry
// writes serialize across processes (CONFIG-SPEC §3).
func lockCentral(fs vfs.FS, croot string) (func() error, error) {
	if err := fs.MkdirAll(croot, 0o755); err != nil {
		return nil, err
	}
	return fs.Lock(filepath.Join(croot, centralLockName))
}

// storeComplete reports whether dir is a finished store: a directory holding a
// config.yaml. A registry entry pointing at anything else is treated like one
// whose folder is missing — skipped by resolution (CONFIG-SPEC §3/§4). That
// covers a folder left behind by a failed promote, and narrows (without
// closing) the window in which a store still being copied could be opened: a
// copy that has already written config.yaml but not the issue files still looks
// complete.
func storeComplete(fs vfs.FS, dir string) bool {
	if fi, err := fs.Stat(dir); err != nil || !fi.IsDir() {
		return false
	}
	_, err := fs.Stat(filepath.Join(dir, ConfigFileName))
	return err == nil
}

// moveStoreDir moves a store directory from src to dst while holding the store's
// own write lock, so a concurrent mutation in that store cannot be split across
// the two locations.
func moveStoreDir(fs vfs.FS, src, dst string) error {
	unlock, err := fs.Lock(filepath.Join(src, lockFileName))
	if err != nil {
		return err
	}
	moveErr := fs.MoveTree(src, dst)
	_ = unlock()
	if moveErr != nil {
		return fmt.Errorf("move store to %s: %w", dst, moveErr)
	}
	return nil
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
	// 1. Explicit override: open the named central store via the registry.
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
			if !storeComplete(fs, dir) {
				// Named explicitly, so say what is wrong rather than reporting
				// it as unregistered: the entry is there, the store is not.
				return nil, ResolveInfo{}, fmt.Errorf("central store %q is not a finished store at %s (no %s) — a move may have failed part-way",
					en.Store, dir, ConfigFileName)
			}
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
		if !storeComplete(fs, dir) {
			continue // dangling or half-built — skip (CONFIG-SPEC §3)
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
	home, croot, err := centralPaths(fs, e)
	if err != nil {
		return nil, err
	}
	project := canonicalize(fs, projectPath, home, croot)

	// Serialize registry writes under the central-root lock (CONFIG-SPEC §3/§5).
	unlock, err := lockCentral(fs, croot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unlock() }()

	entries, err := loadRegistry(fs, croot, home)
	if err != nil {
		return nil, err
	}
	if err := checkFree(entries, name, projectPath, project, home, croot); err != nil {
		return nil, err
	}

	if strings.TrimSpace(prefix) == "" {
		prefix = derivePrefix(project)
	}
	dir := filepath.Join(croot, storesSubdir, name)
	s, err := initData(project, dir, prefix, fs, opts)
	if err != nil {
		return nil, err
	}

	if err := saveRegistry(fs, croot, append(entries, registryEntry{Path: project, Store: name})); err != nil {
		return nil, err
	}
	return s, nil
}

// checkFree reports whether a new entry (name, project) can be added: both the
// store name and the project path must be unused (CONFIG-SPEC §3).
//
// The path is matched on two keys — the caller's raw input and the
// symlink-resolved form — because registry entries are only ever compared
// lexically. Matching on the resolved form alone would let a project registered
// under a path that has since become a symlink be registered a second time, and
// the resulting duplicate would be invisible to loadRegistry's lexical dedup.
func checkFree(entries []registryEntry, name, rawProject, project, home, croot string) error {
	keys := map[string]bool{
		lexCanon(rawProject, home, croot): true,
		lexCanon(project, home, croot):    true,
	}
	for _, en := range entries {
		if en.Store == name {
			return fmt.Errorf("%w: central store %q", ErrStoreExists, name)
		}
		if keys[lexCanon(en.Path, home, croot)] {
			return fmt.Errorf("a central store is already registered for %q", project)
		}
	}
	return nil
}

// MoveToCentral promotes the local store at projectPath into the central root:
// it registers name for projectPath and moves <projectPath>/.tasks to
// <central_root>/stores/<name>, returning the opened central store
// (CONFIG-SPEC §5).
//
// The registry entry is written *before* the files move, so a promote that dies
// between the two leaves the local store in place and still winning resolution;
// the reverse order would leave the store unreachable. When the move returns an
// error the entry is rolled back, so the command can simply be run again. A hard
// kill gets no such chance: it leaves the entry, which resolution ignores as
// dangling (CONFIG-SPEC §3).
//
// The files are not rolled back: a partial cross-filesystem copy stays at the
// destination for inspection, where resolution skips it for having no config.
func MoveToCentral(projectPath, name string, opts ...Option) (*Store, error) {
	return moveToCentralWith(projectPath, name, vfs.NewOS(), env.NewOS(), opts)
}

func moveToCentralWith(projectPath, name string, fs vfs.FS, e env.Environment, opts []Option) (*Store, error) {
	if !validStoreName(name) {
		return nil, fmt.Errorf("invalid store name %q: must match the store-name grammar (CONFIG-SPEC §3)", name)
	}
	home, croot, err := centralPaths(fs, e)
	if err != nil {
		return nil, err
	}
	project := canonicalize(fs, projectPath, home, croot)
	src := filepath.Join(project, DataDirName)
	if fi, err := fs.Stat(src); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrNoStore, src)
	}

	unlock, err := lockCentral(fs, croot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unlock() }()

	entries, err := loadRegistry(fs, croot, home)
	if err != nil {
		return nil, err
	}
	if err := checkFree(entries, name, projectPath, project, home, croot); err != nil {
		return nil, err
	}
	dst := filepath.Join(croot, storesSubdir, name)
	if _, err := fs.Stat(dst); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrStoreExists, dst)
	}
	if err := fs.MkdirAll(filepath.Join(croot, storesSubdir), 0o755); err != nil {
		return nil, err
	}

	if err := saveRegistry(fs, croot, append(entries, registryEntry{Path: project, Store: name})); err != nil {
		return nil, err
	}
	if err := moveStoreDir(fs, src, dst); err != nil {
		// Undo the entry: it now points at files that are not there, and
		// leaving it would block every retry — under this name (taken) and
		// under any other (the project path is taken). Rolling back covers an
		// ordinary failure; a crash between the two writes still leaves the
		// entry, which resolution ignores as dangling (CONFIG-SPEC §3/§5).
		if rbErr := saveRegistry(fs, croot, entries); rbErr != nil {
			return nil, fmt.Errorf("%w (the registry entry %q could not be rolled back: %v — remove it from %s by hand)",
				err, name, rbErr, filepath.Join(croot, registryFileName))
		}
		return nil, err
	}
	return openData(project, dst, fs, opts)
}

// RenameCentral renames the central store oldName to newName: the subfolder
// under <central_root>/stores is renamed and the registry entry updated
// (CONFIG-SPEC §5). It returns the new store directory.
//
// The folder moves before the registry is rewritten; if the registry write
// fails the folder is moved back, so the untouched entry keeps finding it. Both
// steps are individually atomic, so only a hard kill can land in between — and
// it leaves the issues at the new folder with the entry still naming the old
// one, which resolution then skips as dangling. Recovering that means renaming
// the folder back by hand.
func RenameCentral(oldName, newName string) (string, error) {
	return renameCentralWith(oldName, newName, vfs.NewOS(), env.NewOS())
}

func renameCentralWith(oldName, newName string, fs vfs.FS, e env.Environment) (string, error) {
	if !validStoreName(newName) {
		return "", fmt.Errorf("invalid store name %q: must match the store-name grammar (CONFIG-SPEC §3)", newName)
	}
	home, croot, err := centralPaths(fs, e)
	if err != nil {
		return "", err
	}

	unlock, err := lockCentral(fs, croot)
	if err != nil {
		return "", err
	}
	defer func() { _ = unlock() }()

	entries, err := loadRegistry(fs, croot, home)
	if err != nil {
		return "", err
	}
	idx := -1
	for i, en := range entries {
		if en.Store == newName {
			return "", fmt.Errorf("%w: central store %q", ErrStoreExists, newName)
		}
		if en.Store == oldName {
			idx = i
		}
	}
	if idx < 0 {
		return "", fmt.Errorf("%w: %s", ErrStoreNotRegistered, oldName)
	}

	src := filepath.Join(croot, storesSubdir, oldName)
	dst := filepath.Join(croot, storesSubdir, newName)
	if fi, err := fs.Stat(src); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%w: %s", ErrNoStore, src)
	}
	if _, err := fs.Stat(dst); err == nil {
		return "", fmt.Errorf("%w: %s", ErrStoreExists, dst)
	}
	if err := moveStoreDir(fs, src, dst); err != nil {
		return "", err
	}

	entries[idx].Store = newName
	if err := saveRegistry(fs, croot, entries); err != nil {
		// Put the folder back, so the entry that still names the old one keeps
		// finding it. Both directories are under stores/, so this is a rename.
		if rbErr := fs.MoveTree(dst, src); rbErr != nil {
			return "", fmt.Errorf("%w (the store could not be moved back from %s to %s: %v — the registry still names %q)",
				err, dst, src, rbErr, oldName)
		}
		return "", err
	}
	return dst, nil
}

// RelinkCentral re-points the registry entry name at projectPath, for a project
// that moved on disk (CONFIG-SPEC §5). It touches no files and returns the
// canonical project path now recorded.
//
// It refuses when the store subfolder is missing, rather than writing an entry
// that resolution would ignore as dangling, and when projectPath does not exist,
// rather than pointing a live entry at nothing on a typo. projectPath must be
// absolute: a relative path would be resolved against the central root, not the
// caller's working directory.
func RelinkCentral(name, projectPath string) (string, error) {
	return relinkCentralWith(name, projectPath, vfs.NewOS(), env.NewOS())
}

func relinkCentralWith(name, projectPath string, fs vfs.FS, e env.Environment) (string, error) {
	home, croot, err := centralPaths(fs, e)
	if err != nil {
		return "", err
	}
	project := canonicalize(fs, projectPath, home, croot)
	// canonicalize falls back to the lexical path when the target does not
	// exist, so a typo'd path is indistinguishable from a real one unless it is
	// checked here.
	if fi, err := fs.Stat(project); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("project directory does not exist: %s", project)
	}

	unlock, err := lockCentral(fs, croot)
	if err != nil {
		return "", err
	}
	defer func() { _ = unlock() }()

	entries, err := loadRegistry(fs, croot, home)
	if err != nil {
		return "", err
	}
	idx := -1
	for i, en := range entries {
		if en.Store == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", fmt.Errorf("%w: %s", ErrStoreNotRegistered, name)
	}
	dir := filepath.Join(croot, storesSubdir, name)
	if fi, err := fs.Stat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%w: %s", ErrNoStore, dir)
	}
	projKey := lexCanon(project, home, croot)
	for i, en := range entries {
		if i != idx && lexCanon(en.Path, home, croot) == projKey {
			return "", fmt.Errorf("a central store is already registered for %q", project)
		}
	}

	entries[idx].Path = project
	if err := saveRegistry(fs, croot, entries); err != nil {
		return "", err
	}
	return project, nil
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
