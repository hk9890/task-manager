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
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

const (
	// DataDirName is the per-project directory that holds all issue files.
	DataDirName = ".tasks"
	// ConfigFileName is the project config inside the data directory.
	ConfigFileName = "config.yaml"
	// FileExt is the extension of an issue file.
	FileExt = ".md"

	lockFileName  = ".lock"
	closedDirName = "closed"
)

// Errors returned by the store. Callers should test with errors.Is.
var (
	ErrNotFound      = errors.New("issue not found")
	ErrAlreadyExists = errors.New("issue already exists")
	ErrNoStore       = errors.New("no .tasks directory found")
	ErrStoreExists   = errors.New(".tasks directory already exists")
	// ErrStoreNotRegistered is returned by Resolve when an explicit StoreName
	// override names a store with no registry entry (CONFIG-SPEC §4).
	ErrStoreNotRegistered = errors.New("no central store registered under that name")
	// ErrAmbiguousOverride is returned by Resolve when both a store-path and a
	// store-name override are supplied (CONFIG-SPEC §4); they are mutually exclusive.
	ErrAmbiguousOverride = errors.New("store-path and store-name overrides are mutually exclusive")
	// ErrImmutable is returned when a caller attempts an in-place write to a
	// closed issue (which lives in closed/ and is immutable per TASK-STORAGE-SPEC §5).
	// Reopen is the only permitted mutation for a closed issue; comment appends
	// are also allowed (those go through the sidecar, not the task .md).
	ErrImmutable = errors.New("issue is closed and immutable")
)

var prefixRe = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// errNotFound wraps ErrNotFound with the offending ID.
func errNotFound(id string) error {
	return fmt.Errorf("%w: %s", ErrNotFound, id)
}

// defaultNow is the production clock: UTC truncated to whole seconds, so
// timestamps stay readable and produce minimal git diffs.
func defaultNow() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}

// Config is the persisted per-project configuration.
type Config struct {
	// Prefix is prepended to allocated issue IDs, e.g. "agt" -> "agt-0001".
	Prefix string `yaml:"prefix"`

	// HookTimeout is the global per-hook wall-clock limit as a Go duration
	// string ("2s", "5m"); empty means the 2s default, "0" disables it. It is
	// parsed and validated lazily on the first write (HOOK-SPEC §3.1/§3.4), not
	// on read, so a malformed value never breaks queries.
	HookTimeout string `yaml:"hook_timeout,omitempty"`

	// Hooks are the lifecycle-gate hooks run at issue transitions (HOOK-SPEC §3).
	// Like HookTimeout they are validated lazily on the first write; unknown keys
	// within an entry are ignored for forward-compatibility.
	Hooks []Hook `yaml:"hooks,omitempty"`
}

// Store is the single gateway to a project's issue files. Every read and write
// goes through it, so it is the one place that enforces the on-disk format,
// validation, and locking. Nothing else should touch the files directly.
type Store struct {
	root string // project root (the parent of the data dir)
	dir  string // absolute path to the data directory (.tasks)
	cfg  Config
	fs   vfs.FS // disk seam; always vfs.NewOS() in production

	// runner is the process seam used to execute hooks (HOOK-SPEC). It is
	// exec.NewOS() in production; tests inject an exec.Fake to exercise hook
	// logic without spawning real processes. Never nil after construction.
	runner exec.Runner

	// mu serializes writes within a single process. It is acquired by withLock
	// before the advisory flock, so concurrent goroutines in one embedder never
	// interleave their mutations even when flock would allow it (flock is per-
	// process on Linux/macOS). Reads remain lock-free (SDK-SPEC §1/§7,
	// ARCHITECTURE-SPEC §6/§7, TASK-STORAGE-SPEC §7).
	mu sync.Mutex

	// now returns the current time; overridable in tests.
	now func() time.Time

	// logger receives structured observability records (MONITORING.md). It is a
	// discard logger unless the caller supplies one via WithLogger; the SDK never
	// reads the environment itself (the os import stays in the seams), so the CLI
	// configures level/destination from TASKMGR_LOG and injects the logger here.
	logger *slog.Logger

	// hookOnce guards the lazy compile of the hook configuration. Built on the
	// first write (via hooks()); never on a read, so a malformed hooks block
	// fails mutations closed (HOOK-SPEC §3.4) without affecting queries.
	hookOnce sync.Once
	hookSet  *hookSet
	hookErr  error
}

// hooks returns the compiled, validated hook configuration, building it once on
// first use. A configuration error (unknown event, empty run, unparseable when
// or hook_timeout) is returned here and, because this is called only from the
// write path, fails the mutation closed while leaving reads unaffected
// (HOOK-SPEC §3.4).
func (s *Store) hooks() (*hookSet, error) {
	s.hookOnce.Do(func() {
		s.hookSet, s.hookErr = buildHookSet(s.cfg)
	})
	return s.hookSet, s.hookErr
}

// openWithFS is an unexported test hook that constructs a Store rooted at
// root using the provided FS implementation. It does NOT read or create the
// config — the caller must set s.cfg directly (or call readConfig through s.fs
// after construction). This hook exists so tests can inject vfs.Mem or other
// FS implementations without going through Init/Open.
func openWithFS(root string, fs vfs.FS) *Store {
	absRoot, _ := filepath.Abs(root)
	return &Store{
		root:   absRoot,
		dir:    filepath.Join(absRoot, DataDirName),
		fs:     fs,
		runner: exec.NewOS(),
		logger: discardLogger,
		now:    defaultNow,
	}
}

// InitWithVFS creates an initialised store at root using the provided FS seam
// with the given ID prefix. It is intended for test helpers that need to
// supply a custom FS (e.g. vfs.Mem) without going through the OS-backed Init.
// The FS must already have root visible (MkdirAll is called internally).
// Only packages under sdk/tasks/internal can call this because vfs.FS is
// itself internal; outside callers use Init or Open.
func InitWithVFS(root, prefix string, fs vfs.FS) (*Store, error) {
	prefix = strings.TrimSpace(prefix)
	if !prefixRe.MatchString(prefix) {
		return nil, fmt.Errorf("invalid prefix %q: must match %s", prefix, prefixRe.String())
	}
	dir := filepath.Join(root, DataDirName)
	if err := fs.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	cfg := Config{Prefix: prefix}
	s := &Store{root: root, dir: dir, cfg: cfg, fs: fs, runner: exec.NewOS(), logger: discardLogger, now: defaultNow}
	if err := s.writeConfig(cfg); err != nil {
		return nil, err
	}
	return s, nil
}

// Init creates a new data directory under root with the given ID prefix and
// returns an open Store. It fails if a data directory already exists. Options
// (e.g. WithLogger) configure the store.
func Init(root, prefix string, opts ...Option) (*Store, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return initData(absRoot, filepath.Join(absRoot, DataDirName), prefix, vfs.NewOS(), opts)
}

// initData creates a store whose data directory is dir, tracking project root,
// with the given ID prefix, using fs. It fails (ErrStoreExists) if dir already
// exists. It is the shared creation path for Init (local: dir = root/.tasks) and
// InitCentral (central: dir = <central_root>/stores/<name>, root = project path).
func initData(root, dir, prefix string, fs vfs.FS, opts []Option) (*Store, error) {
	prefix = strings.TrimSpace(prefix)
	if !prefixRe.MatchString(prefix) {
		return nil, fmt.Errorf("invalid prefix %q: must match %s", prefix, prefixRe.String())
	}
	if _, err := fs.Stat(dir); err == nil {
		return nil, ErrStoreExists
	}
	if err := fs.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	cfg := Config{Prefix: prefix}
	s := &Store{root: root, dir: dir, cfg: cfg, fs: fs, runner: exec.NewOS(), logger: discardLogger, now: defaultNow}
	s.applyOptions(opts)
	if err := s.writeConfig(cfg); err != nil {
		return nil, err
	}
	return s, nil
}

// Open locates the data directory by walking up from start (or the current
// working directory if start is empty) and loads its config. It performs only
// local discovery — no global config or central registry; consumers that want
// the central fallback call Resolve (CONFIG-SPEC §4). Options configure the store.
func Open(start string, opts ...Option) (*Store, error) {
	fs := vfs.NewOS()
	if start == "" {
		wd, err := fs.Getwd()
		if err != nil {
			return nil, err
		}
		start = wd
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return nil, err
	}
	root, dir, found, err := findLocalStore(fs, abs)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNoStore
	}
	return openData(root, dir, fs, opts)
}

// findLocalStore walks up from start (which must be absolute) looking for a
// DataDirName directory. On success it returns the project root (the parent of
// the data dir) and the data dir itself. found is false when the filesystem root
// is reached without a match.
func findLocalStore(fs vfs.FS, start string) (root, dir string, found bool, err error) {
	abs := start
	for {
		d := filepath.Join(abs, DataDirName)
		if fi, statErr := fs.Stat(d); statErr == nil && fi.IsDir() {
			return abs, d, true, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", "", false, nil
		}
		abs = parent
	}
}

// openData opens an existing store whose data directory is dir, tracking project
// root, using fs, and loads its config. root is reported by Root() and used as
// the hook working directory — for a central store that is the tracked project
// path, not the parent of dir. It is the shared open path for Open (local) and
// Resolve (central / override).
func openData(root, dir string, fs vfs.FS, opts []Option) (*Store, error) {
	s := &Store{root: root, dir: dir, fs: fs, runner: exec.NewOS(), logger: discardLogger, now: defaultNow}
	s.applyOptions(opts)
	cfg, err := s.readConfig()
	if err != nil {
		return nil, err
	}
	s.cfg = cfg
	return s, nil
}

// Root returns the project root directory.
func (s *Store) Root() string { return s.root }

// Dir returns the absolute path to the data directory.
func (s *Store) Dir() string { return s.dir }

// Prefix returns the configured ID prefix.
func (s *Store) Prefix() string { return s.cfg.Prefix }

// SetNow overrides the store's clock with fn. Intended for test helpers that
// need deterministic timestamps across the store-creation boundary (e.g.
// internal/storetest). Production code uses defaultNow.
func (s *Store) SetNow(fn func() time.Time) { s.now = fn }

func (s *Store) writeConfig(cfg Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.fs.WriteAtomic(filepath.Join(s.dir, ConfigFileName), data, 0o644)
}

func (s *Store) readConfig() (Config, error) {
	data, err := s.fs.ReadFile(filepath.Join(s.dir, ConfigFileName))
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Prefix == "" {
		return Config{}, fmt.Errorf("config %s has no prefix", ConfigFileName)
	}
	return cfg, nil
}

func (s *Store) filePath(id string) string {
	return filepath.Join(s.dir, id+FileExt)
}

// closedFilePath returns the path to the .md file for id in the closed/ partition.
func (s *Store) closedFilePath(id string) string {
	return filepath.Join(s.closedDir(), id+FileExt)
}

// closedDir returns the absolute path to the closed/ subdirectory.
func (s *Store) closedDir() string {
	return filepath.Join(s.dir, closedDirName)
}

// isInClosed reports whether the issue with the given id lives in the closed/
// partition (i.e. its .md is not in the hot directory).
func (s *Store) isInClosed(id string) (bool, error) {
	path, err := s.issueFilePath(id)
	return path == s.closedFilePath(id), err
}

// getMutable loads an issue and rejects in-place edits to closed issues.
// Returns ErrImmutable (wrapped with the id) when the issue lives in closed/
// (TASK-STORAGE-SPEC §5). Caller holds the lock.
func (s *Store) getMutable(id string) (*Issue, error) {
	iss, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	inClosed, err := s.isInClosed(id)
	if err != nil {
		return nil, err
	}
	if inClosed {
		return nil, fmt.Errorf("%w: %s", ErrImmutable, id)
	}
	return iss, nil
}

// Get loads a single issue by ID. It first looks in the hot directory and
// falls through to closed/ when the file is absent from the hot set.
func (s *Store) Get(id string) (*Issue, error) {
	// Try the hot directory first.
	data, err := s.fs.ReadFile(s.filePath(id))
	if err == nil {
		iss, err := Unmarshal(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", id, err)
		}
		return iss, nil
	}
	if !vfs.IsNotExist(err) {
		return nil, err
	}
	// Fall through to closed/.
	data, err = s.fs.ReadFile(s.closedFilePath(id))
	if err != nil {
		if vfs.IsNotExist(err) {
			return nil, errNotFound(id)
		}
		return nil, err
	}
	iss, err := Unmarshal(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", id, err)
	}
	return iss, nil
}

// loadIssuesFromDir scans dir for issue files, skipping subdirectories,
// dot-files, and non-.md entries, and returns the parsed issues. Parse errors
// are wrapped as "parse <errPrefix><name>". The error from ReadDir is returned
// UNWRAPPED so callers can test it with vfs.IsNotExist (allClosed relies on this).
func (s *Store) loadIssuesFromDir(dir, errPrefix string) ([]*Issue, error) {
	entries, err := s.fs.ReadDir(dir)
	if err != nil {
		return nil, err // unwrapped on purpose: allClosed() does vfs.IsNotExist on this
	}
	var issues []*Issue
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, FileExt) {
			continue
		}
		data, err := s.fs.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		iss, err := Unmarshal(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s%s: %w", errPrefix, name, err)
		}
		issues = append(issues, iss)
	}
	return issues, nil
}

// All loads every issue in the store. Order is by ID for determinism.
func (s *Store) All() ([]*Issue, error) {
	issues, err := s.loadIssuesFromDir(s.dir, "")
	if err != nil {
		return nil, err
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })
	return issues, nil
}

// index loads all hot (active) issues into a map keyed by ID.
func (s *Store) index() (map[string]*Issue, []*Issue, error) {
	all, err := s.All()
	if err != nil {
		return nil, nil, err
	}
	m := make(map[string]*Issue, len(all))
	for _, iss := range all {
		m[iss.ID] = iss
	}
	return m, all, nil
}

// allClosed loads all issues from the closed/ partition. Returns an empty
// slice if closed/ does not exist.
func (s *Store) allClosed() ([]*Issue, error) {
	issues, err := s.loadIssuesFromDir(s.closedDir(), "closed/")
	if err != nil {
		if vfs.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return issues, nil
}

// nextID allocates a fresh, collision-resistant ID: a random base36 token (see
// newIDFromNames in ids.go). There is no counter file and no high-water scan, so
// issues created in parallel on different branches no longer collide on merge.
//
// This method reads BOTH the hot directory AND the closed/ subdirectory via the
// vfs seam and passes the union to newIDFromNames, which retries against those
// existing names as defence in depth so an ID never collides within one store.
// If closed/ does not yet exist, it is treated as empty (TASK-STORAGE-SPEC §3).
func (s *Store) nextID() (string, error) {
	hotEntries, err := s.fs.ReadDir(s.dir)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(hotEntries))
	for _, e := range hotEntries {
		names = append(names, e.Name())
	}

	// Also scan closed/ for the high-water mark. Absent dir → treat as empty.
	closedEntries, err := s.fs.ReadDir(s.closedDir())
	if err != nil && !vfs.IsNotExist(err) {
		return "", fmt.Errorf("scan closed dir: %w", err)
	}
	for _, e := range closedEntries {
		names = append(names, e.Name())
	}

	return newIDFromNames(s.cfg.Prefix, names), nil
}

// validateNewID checks a caller-supplied issue ID (CreateInput.ID): it must
// match the ID grammar, carry the store prefix, and not already exist in any
// partition. Auto-allocated IDs skip this — they are well-formed by
// construction and unique by allocation.
func (s *Store) validateNewID(id string) error {
	if len(id) > maxIDLen || !idRe.MatchString(id) {
		return invalid("id", "%q is not a valid issue ID", id)
	}
	if !strings.HasPrefix(id, s.cfg.Prefix+"-") {
		return invalid("id", "%q does not carry the store prefix %q", id, s.cfg.Prefix)
	}
	if _, err := s.Get(id); err == nil {
		return fmt.Errorf("%w: %s", ErrAlreadyExists, id)
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// resolveID returns the issue ID to use: a freshly allocated one when raw is
// empty (after trimming), or raw itself once validated as a usable new ID.
// Caller holds the lock.
func (s *Store) resolveID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return s.nextID()
	}
	if err := s.validateNewID(id); err != nil {
		return "", err
	}
	return id, nil
}

// buildIssue assembles an *Issue from the shared fields common to Create and
// Import, applying the identical defaulting both callers need: Title/Creator
// trimmed, Type defaulted to TypeTask when empty, Priority defaulted to
// PriorityDefault unless overridden, and Labels/BlockedBy/Related deduped. The
// status and timestamps are passed in by the caller; the closed end-state
// (Closed/CloseReason) is left to the caller (Import) since Create never sets it.
func buildIssue(id string, in issueFields, status Status, created, updated time.Time) *Issue {
	iss := &Issue{
		ID:          id,
		Title:       strings.TrimSpace(in.Title),
		Status:      status,
		Type:        in.Type,
		Priority:    PriorityDefault,
		Assignee:    in.Assignee,
		Creator:     strings.TrimSpace(in.Creator),
		Labels:      dedupe(in.Labels),
		Parent:      in.Parent,
		BlockedBy:   dedupe(in.BlockedBy),
		Related:     dedupe(in.Related),
		Created:     created,
		Updated:     updated,
		Description: in.Description,
	}
	if iss.Type == "" {
		iss.Type = TypeTask
	}
	if in.Priority != nil {
		iss.Priority = *in.Priority
	}
	return iss
}

// issueFields carries the issue payload shared by CreateInput and ImportInput
// so buildIssue can assemble the common end-state from either caller.
type issueFields struct {
	Title       string
	Description string
	Type        Type
	Priority    *int
	Assignee    string
	Creator     string
	Labels      []string
	Parent      string
	BlockedBy   []string
	Related     []string
}

// writeIssue atomically writes an issue to disk via the FS seam. The
// caller must hold the store lock.
//
// Defense-in-depth (TASK-STORAGE-SPEC §5): if the issue's id already exists
// in closed/, writing to the hot dir would either duplicate it across both
// partitions or silently resurrect it. We detect this here as a belt-and-
// braces guard; callers are expected to check isInClosed before reaching this
// point, but this prevents any future caller from bypassing that check.
func (s *Store) writeIssue(iss *Issue) error {
	inClosed, err := s.isInClosed(iss.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if inClosed {
		return fmt.Errorf("%w: %s", ErrImmutable, iss.ID)
	}
	data, err := Marshal(iss)
	if err != nil {
		return err
	}
	return s.fs.WriteAtomic(s.filePath(iss.ID), data, 0o644)
}

// withLock runs fn while holding both the in-process mutex and the advisory
// flock, so writers serialize across goroutines within one process AND across
// concurrent taskmgr processes on the same host.
//
// Lock order (must be consistent everywhere to avoid deadlock):
//  1. s.mu  — in-process mutex (acquired first)
//  2. flock — cross-process advisory lock (acquired second)
//
// All public mutation methods call withLock exactly once at their outermost
// level. Internal helpers (writeIssue, closeMove, reopenLocked, checkRefs,
// migrateInlineComments) run inside the fn closure — they must NOT call
// withLock themselves, or the non-reentrant Mutex will deadlock.
// Reads (Get, All, Comments, …) are intentionally lock-free.
func (s *Store) withLock(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	unlock, err := s.fs.Lock(filepath.Join(s.dir, lockFileName))
	if err != nil {
		return err
	}
	defer unlock() //nolint:errcheck
	return fn()
}

// CreateInput describes a new issue. Zero values fall back to sensible
// defaults (TypeTask, PriorityDefault, StatusOpen).
type CreateInput struct {
	// ID, when non-empty, is used verbatim instead of allocating a fresh one.
	// It must carry the store prefix, match the ID grammar, and not already
	// exist. This is an escape hatch for import/migration (and test fixtures)
	// that need to preserve known IDs; normal callers leave it empty and let
	// the store allocate a collision-resistant ID.
	ID          string
	Title       string
	Description string
	Type        Type
	Priority    *int
	Assignee    string
	Creator     string
	Labels      []string
	Parent      string
	BlockedBy   []string
	Related     []string
}

// Create validates and writes a new issue, allocating its ID. It runs the
// create lifecycle hooks (HOOK-SPEC §4): a pre-create gate can refuse the write
// (returning *HookDeniedError, nothing written); post-create hooks notify after
// it commits. Hints and post-hook warnings are returned in the MutationResult.
func (s *Store) Create(in CreateInput) (*MutationResult, error) {
	hs, err := s.hooks()
	if err != nil {
		return nil, err
	}
	var created *Issue
	var preHints []string
	err = s.withLock(func() error {
		id, err := s.resolveID(in.ID)
		if err != nil {
			return err
		}
		now := s.now()
		iss := buildIssue(id, issueFields{
			Title:       in.Title,
			Description: in.Description,
			Type:        in.Type,
			Priority:    in.Priority,
			Assignee:    in.Assignee,
			Creator:     in.Creator,
			Labels:      in.Labels,
			Parent:      in.Parent,
			BlockedBy:   in.BlockedBy,
			Related:     in.Related,
		}, StatusOpen, now, now)
		idx, err := s.validateAndIndex(iss)
		if err != nil {
			return err
		}
		preHints, err = s.gateWrite(hs, transCreate, nil, iss, idx, func() error { return s.writeIssue(iss) })
		if err != nil {
			return err
		}
		created = iss
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.postFinish(hs, true, transCreate, nil, created, preHints), nil
}

// UpdateInput holds partial changes. Nil pointer fields are left unchanged.
type UpdateInput struct {
	Title        *string
	Description  *string
	Status       *Status
	Type         *Type
	Priority     *int
	Assignee     *string
	Parent       *string
	SetLabels    []string // replace the label set wholesale
	AddLabels    []string
	RemoveLabels []string
	ClearLabels  bool
}

// Update applies a partial update to an issue.
//
// Status routing (TASK-STORAGE-SPEC §5 / CLI-SPEC §4):
//   - Setting Status to StatusClosed routes through Close (moves .md to closed/).
//   - Setting Status to a non-closed value on a closed issue routes through Reopen
//     (moves .md back to hot dir), then applies the remaining field changes.
func (s *Store) Update(id string, in UpdateInput) (*MutationResult, error) {
	hs, err := s.hooks()
	if err != nil {
		return nil, err
	}

	// Detect early lifecycle-routing cases that must run under a single lock.
	// We acquire the lock once and handle all cases inside.
	var old, newIss *Issue
	var trans transition
	var preHints []string
	var fired bool
	err = s.withLock(func() error {
		iss, err := s.Get(id)
		if err != nil {
			return err
		}

		// Determine whether this Update requests a lifecycle transition.
		requestsClose := in.Status != nil && (*in.Status).IsClosed() && !iss.Status.IsClosed()
		requestsReopen := in.Status != nil && !(*in.Status).IsClosed() && iss.Status.IsClosed()
		isCurrentlyClosed := iss.Status.IsClosed()

		if requestsClose {
			// Route through the close flow: apply non-status field changes, then close.
			old = cloneIssue(iss)
			applyNonStatusFields(iss, in)
			idx, err := s.validateAndIndex(iss)
			if err != nil {
				return err
			}
			// Apply close fields and gate. Closed and Updated share one instant.
			s.applyStatus(iss, StatusClosed, "")
			iss.Updated = iss.Closed
			trans = classify(old, iss)
			preHints, err = s.gateWrite(hs, trans, old, iss, idx, func() error { return s.closeMove(iss) })
			if err != nil {
				return err
			}
			newIss, fired = iss, true
			return nil
		}

		if requestsReopen {
			// Route through the reopen flow: materialize the reopened end-state,
			// gate it, then write it back to the hot dir in one move.
			old = cloneIssue(iss)
			iss.Status = StatusOpen
			iss.Closed = time.Time{}
			iss.CloseReason = ""
			// Override with the requested non-closed status so that e.g.
			// Update(..., {Status: in_progress}) lands on in_progress, not open
			// (SDK-SPEC §4: "the issue ends in the requested status").
			iss.Status = *in.Status
			applyNonStatusFields(iss, in)
			iss.Updated = s.now()
			idx, err := s.validateAndIndex(iss)
			if err != nil {
				return err
			}
			trans = classify(old, iss)
			preHints, err = s.gateWrite(hs, trans, old, iss, idx, func() error { return s.reopenWrite(iss) })
			if err != nil {
				return err
			}
			newIss, fired = iss, true
			return nil
		}

		// Ordinary update: issue must be in the hot dir (mutable).
		if isCurrentlyClosed {
			return fmt.Errorf("%w: %s", ErrImmutable, id)
		}

		old = cloneIssue(iss) // snapshot before applying changes
		applyNonStatusFields(iss, in)
		if in.Status != nil {
			s.applyStatus(iss, *in.Status, "")
		}
		iss.Updated = s.now()

		// No-op: if nothing would change on disk (ignoring the stamped Updated),
		// write nothing and run no hooks. The engine detects this for every front
		// end, not as a CLI-level short-circuit (HOOK-SPEC §2.1).
		if issuesEqualIgnoringUpdated(old, iss) {
			newIss, fired = old, false
			return nil
		}

		idx, err := s.validateAndIndex(iss)
		if err != nil {
			return err
		}
		trans = classify(old, iss)
		preHints, err = s.gateWrite(hs, trans, old, iss, idx, func() error { return s.writeIssue(iss) })
		if err != nil {
			return err
		}
		newIss, fired = iss, true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.postFinish(hs, fired, trans, old, newIss, preHints), nil
}

// applyNonStatusFields applies all UpdateInput fields except Status to iss.
func applyNonStatusFields(iss *Issue, in UpdateInput) {
	if in.Title != nil {
		iss.Title = strings.TrimSpace(*in.Title)
	}
	if in.Description != nil {
		iss.Description = *in.Description
	}
	if in.Type != nil {
		iss.Type = *in.Type
	}
	if in.Priority != nil {
		iss.Priority = *in.Priority
	}
	if in.Assignee != nil {
		iss.Assignee = *in.Assignee
	}
	if in.Parent != nil {
		iss.Parent = *in.Parent
	}
	applyLabels(iss, in)
}

// applyStatus sets status and keeps the closed timestamp/reason consistent.
func (s *Store) applyStatus(iss *Issue, status Status, reason string) {
	prev := iss.Status
	iss.Status = status
	switch {
	case status.IsClosed() && !prev.IsClosed():
		iss.Closed = s.now()
		if reason != "" {
			iss.CloseReason = reason
		}
	case !status.IsClosed() && prev.IsClosed():
		iss.Closed = time.Time{}
		iss.CloseReason = ""
	case status.IsClosed() && reason != "":
		iss.CloseReason = reason
	}
}

func applyLabels(iss *Issue, in UpdateInput) {
	switch {
	case in.ClearLabels:
		iss.Labels = nil
		return
	case in.SetLabels != nil:
		iss.Labels = dedupe(in.SetLabels)
	}
	if len(in.AddLabels) > 0 {
		iss.Labels = dedupe(append(iss.Labels, in.AddLabels...))
	}
	if len(in.RemoveLabels) > 0 {
		remove := make(map[string]struct{}, len(in.RemoveLabels))
		for _, l := range in.RemoveLabels {
			remove[l] = struct{}{}
		}
		kept := iss.Labels[:0]
		for _, l := range iss.Labels {
			if _, drop := remove[l]; !drop {
				kept = append(kept, l)
			}
		}
		iss.Labels = kept
	}
}

// Close stamps the issue closed and moves its .md to closed/.
//
// Idempotent (CLI-SPEC §"taskmgr close", SDK-SPEC §4): if the issue is already
// closed, Close returns a successful no-op — the existing closed issue is
// returned and no file write occurs. The supplied reason is silently ignored
// on a re-close; the original close_reason from the first Close call is
// preserved. This keeps the "closed issues are immutable in place"
// (TASK-STORAGE-SPEC §5) invariant intact — no hot-dir write is attempted.
//
// To change the close_reason of an already-closed issue, Reopen it first,
// then Close again with the new reason.
//
// Use Reopen to restore mutability, or AddComment to append a post-close note.
func (s *Store) Close(id, reason string) (*MutationResult, error) {
	hs, err := s.hooks()
	if err != nil {
		return nil, err
	}
	var old, newIss *Issue
	var preHints []string
	var fired bool
	err = s.withLock(func() error {
		iss, err := s.Get(id)
		if err != nil {
			return err
		}
		// If the issue is already closed, return a successful no-op.
		// No in-place write to closed/ is attempted, preserving the immutability
		// invariant (TASK-STORAGE-SPEC §5); no hooks fire (HOOK-SPEC §2.1).
		inClosed, err := s.isInClosed(id)
		if err != nil {
			return err
		}
		if inClosed {
			newIss, fired = iss, false
			return nil
		}
		old = cloneIssue(iss)
		s.applyStatus(iss, StatusClosed, reason)
		iss.Updated = s.now()
		preHints, err = s.gateWrite(hs, transClose, old, iss, nil, func() error { return s.closeMove(iss) })
		if err != nil {
			return err
		}
		newIss, fired = iss, true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.postFinish(hs, fired, transClose, old, newIss, preHints), nil
}

// closeMove writes the issue to closed/ and removes its hot-dir file.
// It must be called while holding the store lock.
//
// Sequence (no-torn-state guarantee):
//  1. MkdirAll closed/ (idempotent).
//  2. WriteAtomic to closed/<id>.md — if this fails, the hot-dir file is
//     untouched and the caller sees an error with the issue still open.
//  3. vfs.Rename hot/<id>.md → closed/<id>.md — atomic rename over the
//     already-written closed file. This is the git rename that preserves
//     file history. If it fails, closed/ has the new content and hot/ has
//     the old; Get falls through to closed/ and returns the closed version —
//     a recoverable inconsistency that resolves on the next successful close.
//
// In practice (real osFS), WriteAtomic + Rename together behave like a
// single atomic move because WriteAtomic internally uses temp+rename within
// closed/, and the final Rename from hot to closed is the git history anchor.
func (s *Store) closeMove(iss *Issue) error {
	// Step 1: ensure closed/ exists.
	if err := s.fs.MkdirAll(s.closedDir(), 0o755); err != nil {
		return fmt.Errorf("create closed dir: %w", err)
	}
	// Step 2: write the closed-state content directly to closed/<id>.md.
	data, err := Marshal(iss)
	if err != nil {
		return err
	}
	closedPath := s.closedFilePath(iss.ID)
	if err := s.fs.WriteAtomic(closedPath, data, 0o644); err != nil {
		return err
	}
	// Step 3: rename the hot-dir file over the closed-dir file (git rename).
	// This is a rename of the original hot file → closed, which git tracks as a
	// rename. WriteAtomic in step 2 wrote the updated content; the rename in
	// step 3 replaces it with the original-path-named file. To preserve both the
	// updated content AND the git rename, we write the updated content to the
	// hot-dir file first and then rename it over the closed/ file.
	hotPath := s.filePath(iss.ID)
	if err := s.fs.WriteAtomic(hotPath, data, 0o644); err != nil {
		// Hot-dir write failed; closed/ already has the new content. Return the
		// error; Get will fall through to closed/ and find the closed issue.
		return err
	}
	return s.fs.Rename(hotPath, closedPath)
}

// Reopen moves a closed issue back to the active set, clears its closed
// timestamp/reason, sets status open, and bumps updated. It runs the reopen
// lifecycle hooks (HOOK-SPEC §4): a pre-reopen gate can refuse; post-reopen
// hooks notify after the move commits.
func (s *Store) Reopen(id string) (*MutationResult, error) {
	hs, err := s.hooks()
	if err != nil {
		return nil, err
	}
	var old, newIss *Issue
	var preHints []string
	var fired bool
	err = s.withLock(func() error {
		iss, err := s.Get(id)
		if err != nil {
			return err
		}
		// Must currently be closed.
		inClosed, err := s.isInClosed(id)
		if err != nil {
			return err
		}
		if !inClosed {
			// Not in closed/ — nothing to reopen. Idempotent no-op: return the
			// issue unchanged, no write, no hooks.
			newIss, fired = iss, false
			return nil
		}
		old = cloneIssue(iss)
		iss.Status = StatusOpen
		iss.Closed = time.Time{}
		iss.CloseReason = ""
		iss.Updated = s.now()
		preHints, err = s.gateWrite(hs, transReopen, old, iss, nil, func() error { return s.reopenWrite(iss) })
		if err != nil {
			return err
		}
		newIss, fired = iss, true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.postFinish(hs, fired, transReopen, old, newIss, preHints), nil
}

// reopenWrite moves a reopened issue's .md from closed/ back to the hot dir,
// writing the supplied (already-materialized and validated) end-state. The
// caller holds the store lock. It writes the content to the closed-dir path
// atomically and then renames it to the hot dir, preserving git rename history.
func (s *Store) reopenWrite(iss *Issue) error {
	src := s.closedFilePath(iss.ID)
	dst := s.filePath(iss.ID)
	data, err := Marshal(iss)
	if err != nil {
		return err
	}
	if err := s.fs.WriteAtomic(src, data, 0o644); err != nil {
		return err
	}
	return s.fs.Rename(src, dst)
}

// Comments returns the resolved effective comment log for an issue: each
// replaces-chain collapsed to its newest document, tombstoned comments omitted.
// The on-disk stream keeps full history; this returns the current view.
// All() / Ready() / List() never read sidecars; Comments() loads it lazily.
func (s *Store) Comments(id string) ([]Comment, error) {
	// Verify the issue exists first.
	if _, err := s.Get(id); err != nil {
		return nil, err
	}
	stream, err := readCommentStream(s.fs, s.commentsPath(id))
	if err != nil {
		return nil, err
	}
	return resolveComments(stream), nil
}

// migrateInlineComments checks whether the issue .md at issueFilePath still
// contains old-style inline comments in its frontmatter. If it does, it
// appends them to the sidecar and rewrites the issue .md without the
// comments field. This is a one-time migration run on first sidecar touch.
// The caller must hold the store lock.
//
// issueFilePath is the actual on-disk path to the .md file (hot or closed/).
func (s *Store) migrateInlineComments(issueFilePath string) error {
	data, err := s.fs.ReadFile(issueFilePath)
	if err != nil {
		return err
	}
	iss, legacy, err := unmarshalWithLegacy(data)
	if err != nil {
		return err
	}
	if len(legacy) == 0 {
		return nil // nothing to migrate
	}

	// Append legacy comments to the sidecar, in order.
	sidecarPath := s.commentsPath(iss.ID)
	for _, lc := range legacy {
		created, tsErr := parseTimestamp(lc.Created)
		if tsErr != nil {
			// Use a fallback time if the timestamp is unparseable.
			created = s.now()
		}
		c := Comment{
			ID:      newCommentID(),
			Author:  lc.Author,
			Created: created,
			Body:    sanitizeCommentBody(lc.Body),
		}
		if err := appendCommentDoc(s.fs, sidecarPath, c); err != nil {
			return fmt.Errorf("migrate comment to sidecar: %w", err)
		}
	}

	// Rewrite the issue .md to the same path (hot or closed/) without the
	// inline comments field (Marshal now omits it). For closed files this is
	// an internal migration-only rewrite, not a user mutation.
	migrated, err := Marshal(iss)
	if err != nil {
		return err
	}
	return s.fs.WriteAtomic(issueFilePath, migrated, 0o644)
}

// issueFilePath returns the actual on-disk path for an issue's .md file,
// checking the hot directory first and falling through to closed/.
// Returns ErrNotFound if the issue does not exist in either partition.
func (s *Store) issueFilePath(id string) (string, error) {
	hotPath := s.filePath(id)
	if _, err := s.fs.Stat(hotPath); err == nil {
		return hotPath, nil
	}
	closedPath := s.closedFilePath(id)
	if _, err := s.fs.Stat(closedPath); err == nil {
		return closedPath, nil
	}
	return "", errNotFound(id)
}

// prepareCommentMutation verifies the issue exists, migrates any legacy inline
// comments, and returns the issue plus its sidecar path (keyed on iss.ID, not
// the input id). Caller must hold the store lock.
func (s *Store) prepareCommentMutation(id string) (*Issue, string, error) {
	iss, err := s.Get(id)
	if err != nil {
		return nil, "", err
	}
	issPath, err := s.issueFilePath(id)
	if err != nil {
		return nil, "", err
	}
	if err := s.migrateInlineComments(issPath); err != nil {
		return nil, "", fmt.Errorf("migrate inline comments: %w", err)
	}
	return iss, s.commentsPath(iss.ID), nil
}

// requireReplaceTarget verifies commentID exists as an earlier comment in the
// sidecar stream at sidecarPath. Caller must hold the store lock.
func (s *Store) requireReplaceTarget(sidecarPath, commentID string) error {
	stream, err := readCommentStream(s.fs, sidecarPath)
	if err != nil {
		return err
	}
	return validateReplaces(commentID, stream)
}

// AddComment appends a new comment to the issue sidecar and returns the new
// comment (including its freshly allocated random ID). The issue .md file is
// NOT rewritten (the sidecar is append-only per TASK-STORAGE-SPEC §4.4).
// Comment appends are allowed on closed issues (TASK-STORAGE-SPEC §4.4.6).
func (s *Store) AddComment(id, author, body string) (*Comment, error) {
	var out *Comment
	err := s.withLock(func() error {
		_, sidecarPath, err := s.prepareCommentMutation(id)
		if err != nil {
			return err
		}

		body = sanitizeCommentBody(body)
		c := Comment{
			ID:      newCommentID(),
			Author:  author,
			Created: s.now(),
			Body:    body,
		}
		if err := validateCommentDoc(c); err != nil {
			return err
		}

		if err := appendCommentDoc(s.fs, sidecarPath, c); err != nil {
			return err
		}
		out = &c
		return nil
	})
	return out, err
}

// EditComment appends a revision to the issue sidecar with Replaces set to
// commentID, and returns the new effective comment. The issue .md file is NOT
// rewritten (the sidecar is append-only per TASK-STORAGE-SPEC §4.4).
func (s *Store) EditComment(id, commentID, author, body string) (*Comment, error) {
	var out *Comment
	err := s.withLock(func() error {
		_, sidecarPath, err := s.prepareCommentMutation(id)
		if err != nil {
			return err
		}

		// Validate that the target comment exists.
		if err := s.requireReplaceTarget(sidecarPath, commentID); err != nil {
			return err
		}

		body = sanitizeCommentBody(body)
		c := Comment{
			ID:       newCommentID(),
			Author:   author,
			Created:  s.now(),
			Replaces: commentID,
			Body:     body,
		}
		if err := validateCommentDoc(c); err != nil {
			return err
		}

		if err := appendCommentDoc(s.fs, sidecarPath, c); err != nil {
			return err
		}
		out = &c
		return nil
	})
	return out, err
}

// DeleteComment appends a tombstone to the issue sidecar with Replaces set to
// commentID and Deleted: true. The issue .md file is NOT rewritten.
func (s *Store) DeleteComment(id, commentID, author string) error {
	return s.withLock(func() error {
		_, sidecarPath, err := s.prepareCommentMutation(id)
		if err != nil {
			return err
		}

		if err := s.requireReplaceTarget(sidecarPath, commentID); err != nil {
			return err
		}

		c := Comment{
			ID:       newCommentID(),
			Author:   author,
			Created:  s.now(),
			Replaces: commentID,
			Deleted:  true,
		}

		return appendCommentDoc(s.fs, sidecarPath, c)
	})
}

// AddDep records that dependent is blocked by blocker.
func (s *Store) AddDep(dependent, blocker string) error {
	return s.withLock(func() error {
		iss, err := s.getMutable(dependent)
		if err != nil {
			return err
		}
		if dependent == blocker {
			return invalid("blocked_by", "issue cannot block itself")
		}
		for _, b := range iss.BlockedBy {
			if b == blocker {
				return nil // already present; idempotent
			}
		}
		iss.BlockedBy = append(iss.BlockedBy, blocker)
		iss.Updated = s.now()
		if err := s.checkRefs(iss); err != nil {
			return err
		}
		return s.writeIssue(iss)
	})
}

// RemoveDep removes a blocker from dependent.
func (s *Store) RemoveDep(dependent, blocker string) error {
	return s.withLock(func() error {
		iss, err := s.getMutable(dependent)
		if err != nil {
			return err
		}
		kept := iss.BlockedBy[:0]
		for _, b := range iss.BlockedBy {
			if b != blocker {
				kept = append(kept, b)
			}
		}
		iss.BlockedBy = kept
		iss.Updated = s.now()
		return s.writeIssue(iss)
	})
}

// AddRelated records a non-blocking "related" reference from issueID to otherID.
// Idempotent; rejects self-reference and dangling refs (no cycle check — related
// is non-blocking and legitimately symmetric). The edge is stored one-directional
// on issueID; the inverse is derived on read (Detail.RelatedRefs is the symmetric
// union), so the link surfaces from both issues.
func (s *Store) AddRelated(issueID, otherID string) error {
	return s.withLock(func() error {
		iss, err := s.getMutable(issueID)
		if err != nil {
			return err
		}
		if issueID == otherID {
			return invalid("related", "issue cannot relate to itself")
		}
		for _, r := range iss.Related {
			if r == otherID {
				return nil // already present; idempotent
			}
		}
		iss.Related = append(iss.Related, otherID)
		iss.Updated = s.now()
		if err := s.checkRefs(iss); err != nil {
			return err
		}
		return s.writeIssue(iss)
	})
}

// RemoveRelated severs the related link between issueID and otherID. Because the
// relationship is symmetric, it removes the edge from BOTH sides' stored lists so
// the link is truly gone regardless of which side recorded it. The primary side
// (issueID) must be writable (closed → ErrImmutable, mirroring RemoveDep); the
// inverse side is best-effort (skipped if otherID is absent or closed/immutable).
func (s *Store) RemoveRelated(issueID, otherID string) error {
	return s.withLock(func() error {
		removeRef := func(it *Issue, target string) bool {
			kept := it.Related[:0]
			changed := false
			for _, r := range it.Related {
				if r == target {
					changed = true
					continue
				}
				kept = append(kept, r)
			}
			it.Related = kept
			return changed
		}

		iss, err := s.getMutable(issueID)
		if err != nil {
			return err
		}
		if removeRef(iss, otherID) {
			iss.Updated = s.now()
			if err := s.writeIssue(iss); err != nil {
				return err
			}
		}

		// Inverse side: best-effort. Absent or closed → leave it (a closed issue
		// is immutable, and the active view never derives inverses from closed/).
		other, err := s.Get(otherID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil
			}
			return err
		}
		otherClosed, err := s.isInClosed(otherID)
		if err != nil {
			return err
		}
		if otherClosed {
			return nil
		}
		if removeRef(other, issueID) {
			other.Updated = s.now()
			return s.writeIssue(other)
		}
		return nil
	})
}

// checkRefs verifies that every referenced ID exists and that adding the
// issue's blockers does not create a dependency cycle. Caller holds the lock.
//
// A reference is valid if the target ID is found in the hot (active) index OR
// in the closed/ partition (checked via a cheap vfs.Stat — no parse needed).
// A reference to an ID present in neither partition is a dangling reference
// and is returned as a *ValidationError. This implements TASK-STORAGE-SPEC
// §9/§10: closed refs are valid; dangling refs are always rejected.
func (s *Store) checkRefs(iss *Issue) error {
	idx, _, err := s.index()
	if err != nil {
		return err
	}
	return s.checkRefsWith(iss, idx)
}

// checkRefsWith verifies references against a caller-supplied hot index, which
// it overlays with iss. A gated mutation builds the index once and shares it
// between reference-checking and the hook `when` row, avoiding a second
// whole-store scan under the lock (HOOK-SPEC §8).
func (s *Store) checkRefsWith(iss *Issue, idx map[string]*Issue) error {
	idx[iss.ID] = iss // include the (possibly new) issue itself

	// refExists reports whether an ID is resolvable: either in the hot index
	// or in the closed/ partition (via cheap Stat, no parse).
	refExists := func(id string) bool {
		if _, ok := idx[id]; ok {
			return true
		}
		_, statErr := s.fs.Stat(s.closedFilePath(id))
		return statErr == nil
	}

	if iss.Parent != "" {
		if !refExists(iss.Parent) {
			return invalid("parent", "referenced issue %q does not exist", iss.Parent)
		}
	}
	for _, id := range iss.BlockedBy {
		if !refExists(id) {
			return invalid("blocked_by", "referenced issue %q does not exist", id)
		}
	}
	for _, id := range iss.Related {
		if !refExists(id) {
			return invalid("related", "referenced issue %q does not exist", id)
		}
	}
	if cycle := findCycle(idx, iss.ID); cycle != "" {
		return invalid("blocked_by", "dependency cycle: %s", cycle)
	}
	return nil
}

// Labels returns the sorted set of distinct labels in use across all issues.
func (s *Store) Labels() ([]string, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	set := map[string]struct{}{}
	for _, iss := range all {
		for _, l := range iss.Labels {
			set[l] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	sort.Strings(out)
	return out, nil
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
