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

// storeState classifies what a registry entry's store subfolder actually is.
// The three cases are handled differently and must not be collapsed: a folder
// that is gone is a dangling entry, which resolution skips (CONFIG-SPEC §3),
// while a folder that is there but unusable is a broken store, which resolution
// reports. Treating the second as the first hides a real store behind "no
// .tasks directory found".
type storeState int

const (
	storeMissing  storeState = iota // no directory at all: a dangling entry
	storePartial                    // a directory, but no config.yaml
	storeFinished                   // a usable store
)

// storeStateOf classifies dir. Only a not-exist Stat means storeMissing: any
// other failure — a permission that stops the walk, an I/O error — says nothing
// about whether the store is there, and answering "gone" for it reports intact
// stores as dangling and sends the reader to advice that deletes the entry.
func storeStateOf(fs vfs.FS, dir string) (storeState, error) {
	fi, err := fs.Stat(dir)
	if err != nil {
		if vfs.IsNotExist(err) {
			return storeMissing, nil
		}
		return storeMissing, fmt.Errorf("read central store %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return storeMissing, nil
	}
	if _, err := fs.Stat(filepath.Join(dir, ConfigFileName)); err != nil {
		if vfs.IsNotExist(err) {
			return storePartial, nil
		}
		return storeMissing, fmt.Errorf("read central store %s: %w", dir, err)
	}
	return storeFinished, nil
}

// healthOf maps the internal classification onto the public one Stores reports,
// so a listing and a resolution can never disagree about an entry.
func healthOf(fs vfs.FS, dir string) (StoreHealth, error) {
	st, err := storeStateOf(fs, dir)
	if err != nil {
		return StoreOK, err
	}
	switch st {
	case storeMissing:
		return StoreDangling, nil
	case storePartial:
		return StoreBroken, nil
	default:
		return StoreOK, nil
	}
}

// storeComplete reports whether dir is a finished store: a directory holding a
// config.yaml.
func storeComplete(fs vfs.FS, dir string) (bool, error) {
	st, err := storeStateOf(fs, dir)
	return st == storeFinished, err
}

// errPartialStore is the shared diagnostic for a registry entry whose folder is
// present but unusable. It names the folder and what is missing, so the fix is
// obvious; the alternative — reporting no store at all — sends the user to
// `taskmgr init`, which creates a second, empty store and splits the project's
// issues across the two.
func errPartialStore(name, dir string) error {
	return fmt.Errorf("central store %q is not a finished store at %s (no %s) — a move may have failed part-way",
		name, dir, ConfigFileName)
}

// errDanglingStore is the diagnostic for a registry entry whose folder is gone
// altogether. It is a different fault from a partial store and takes a different
// repair, so it must not borrow errPartialStore's message: that one sends the
// reader to `ls` and a hand-written config.yaml inside a directory that does not
// exist, and both commands fail on the spot.
func errDanglingStore(name, dir string) error {
	return fmt.Errorf("central store %q is registered but its directory is gone (%s) — restore it, or drop the entry from %s",
		name, dir, registryFileName)
}

// stagingPrefix names the directory a store is assembled in before it is
// published under its real name. It is not a legal store name (validStoreName
// rejects the leading dot), so it can never be resolved as one or collide with
// a real store.
const stagingPrefix = ".incoming-"

// moveStoreDir moves a store directory from src to dst while holding the store's
// own write lock, so a concurrent mutation in that store cannot be split across
// the two locations.
//
// The tree is moved to a staging directory beside dst and renamed into place
// only once it is whole. Moving straight to dst publishes it a piece at a time
// on the cross-filesystem path: the registry entry is already live by then, so
// the moment config.yaml lands another process resolves the half-copied folder
// as a finished store, takes ITS lock — a different file from the one held here,
// which is why locking the source serializes nothing at the destination — and
// writes into it, whereupon the copy walks into its own EEXIST. The final rename
// is within one directory and atomic, so dst never exists half-built.
//
// A failed attempt therefore leaves its partial copy at the staging path, not at
// dst where it would block every retry with ErrStoreExists. The next attempt
// clears it: getting here means src exists and is locked, and the central-root
// lock serializes attempts, so anything staged is a leftover and never the only
// copy of the data.
func moveStoreDir(fs vfs.FS, src, dst string) error {
	unlock, err := fs.Lock(filepath.Join(src, lockFileName))
	if err != nil {
		return err
	}
	staging := filepath.Join(filepath.Dir(dst), stagingPrefix+filepath.Base(dst))
	moveErr := fs.RemoveAll(staging)
	if moveErr == nil {
		moveErr = fs.MoveTree(src, staging)
	}
	if moveErr == nil {
		moveErr = fs.MoveTree(staging, dst)
	}
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
	// 0. TASKMGR_DIR used to point resolution at a store directory outright. The
	// override is gone, along with the --store-path flag it mirrored. The flag
	// now fails as unknown, but an environment variable has no such backstop:
	// left unread it silently pins nothing, and a CI job or direnv profile that
	// exports it writes every issue into whatever store the walk-up happens to
	// find. Refuse instead of misfiling the work.
	if dir := strings.TrimSpace(e.Getenv(envTaskmgrDir)); dir != "" {
		return nil, ResolveInfo{}, fmt.Errorf("%s is set (%s) but is no longer supported — run from inside the project, or register the store centrally with 'taskmgr store move --central --to <name>' and select it with --store-name",
			envTaskmgrDir, dir)
	}

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
			st, err := storeStateOf(fs, dir)
			if err != nil {
				return nil, ResolveInfo{}, err
			}
			// Named explicitly, so say what is wrong rather than reporting it as
			// unregistered: the entry is there, the store is not. Which of the two
			// faults it is decides the repair, so they get separate messages.
			switch st {
			case storeMissing:
				return nil, ResolveInfo{}, errDanglingStore(en.Store, dir)
			case storePartial:
				return nil, ResolveInfo{}, errPartialStore(en.Store, dir)
			}
			project := canonicalize(fs, en.Path, home, croot)
			s, err := openData(project, dir, fs, sopts)
			if err != nil {
				return nil, ResolveInfo{}, err
			}
			s.name = en.Store
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
		st, err := storeStateOf(fs, dir)
		if err != nil {
			return nil, ResolveInfo{}, err
		}
		if st == storeMissing {
			continue // dangling: the folder is gone — skip (CONFIG-SPEC §3)
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
	// The entry that owns this directory is the one that answers for it, so a
	// folder that is present but unusable is reported rather than skipped. A
	// store whose config.yaml went missing is still the project's store, with
	// every issue file intact; skipping it hands the project a shorter ancestor's
	// store or ErrNoStore, and the advice that comes with the latter creates a
	// second, empty store beside the real one.
	if done, err := storeComplete(fs, dir); err != nil {
		return nil, ResolveInfo{}, err
	} else if !done {
		return nil, ResolveInfo{}, errPartialStore(en.Store, dir)
	}
	project := canonPaths[idx]
	s, err := openData(project, dir, fs, sopts)
	if err != nil {
		return nil, ResolveInfo{}, err
	}
	s.name = en.Store
	return s, ResolveInfo{Kind: ResolvedCentral, StorePath: dir, ProjectPath: project}, nil
}

// Stores returns the central registry entries (CONFIG-SPEC §4, SDK-SPEC §1). It
// does not resolve against a working directory; it reads through the seams and
// never writes. A missing registry yields an empty slice; a corrupt one an error.
//
// It deliberately takes no ResolveOptions: the registry is global, so there is
// nothing for a working directory or a store-name override to select. It used to
// accept one and discard it, which made --dir and --store-name look as if they
// filtered `taskmgr store list` when they never did.
func Stores() ([]StoreEntry, error) {
	return storesWith(vfs.NewOS(), env.NewOS())
}

func storesWith(fs vfs.FS, e env.Environment) ([]StoreEntry, error) {
	home, croot, entries, err := loadCentral(fs, e)
	if err != nil {
		return nil, err
	}
	out := make([]StoreEntry, 0, len(entries))
	for _, en := range entries {
		dir := filepath.Join(croot, storesSubdir, en.Store)
		health, err := healthOf(fs, dir)
		if err != nil {
			return nil, err
		}
		out = append(out, StoreEntry{
			Path:      canonicalize(fs, en.Path, home, croot),
			Store:     en.Store,
			StorePath: dir,
			Health:    health,
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
		prefix = DerivePrefix(project)
	}
	dir := filepath.Join(croot, storesSubdir, name)
	s, err := initData(project, dir, prefix, fs, opts)
	if err != nil {
		return nil, err
	}
	s.name = name

	if err := saveRegistry(fs, croot, append(entries, registryEntry{Path: project, Store: name})); err != nil {
		return nil, err
	}
	return s, nil
}

// projectKeys returns the lexical keys a project path must be matched on: the
// caller's raw input and the symlink-resolved form.
//
// Two keys are needed because registry entries are only ever compared lexically.
// Matching on the resolved form alone would let a project registered under a
// path that has since become a symlink be registered a second time, and the
// resulting duplicate would be invisible to loadRegistry's lexical dedup —
// leaving two entries for one project, with resolution picking whichever it
// scanned into first. Every registry writer must use this, not just the ones
// that create entries.
func projectKeys(rawProject, project, home, croot string) map[string]bool {
	return map[string]bool{
		lexCanon(rawProject, home, croot): true,
		lexCanon(project, home, croot):    true,
	}
}

// checkFree reports whether a new entry (name, project) can be added: both the
// store name and the project path must be unused (CONFIG-SPEC §3).
func checkFree(entries []registryEntry, name, rawProject, project, home, croot string) error {
	keys := projectKeys(rawProject, project, home, croot)
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
// staging path beside the destination for inspection (see moveStoreDir), which
// is neither a legal store name nor in the way of a retry.
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
	s, err := openData(project, dst, fs, opts)
	if err != nil {
		return nil, err
	}
	s.name = name
	return s, nil
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
	// The folder must be a finished store, not merely a directory: resolution
	// requires config.yaml, so a bare Stat here would accept a folder left behind
	// by a half-done promote and report success on an entry the very next command
	// skips as dangling.
	dir := filepath.Join(croot, storesSubdir, name)
	if done, err := storeComplete(fs, dir); err != nil {
		return "", err
	} else if !done {
		return "", fmt.Errorf("%w: %s is not a finished store (no %s) — relinking it would write an entry that resolution skips",
			ErrNoStore, dir, ConfigFileName)
	}
	keys := projectKeys(projectPath, project, home, croot)
	for i, en := range entries {
		if i != idx && keys[lexCanon(en.Path, home, croot)] {
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
