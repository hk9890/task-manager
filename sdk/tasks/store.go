package tasks

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/hk9890/agent-tasks/sdk/tasks/internal/vfs"
)

const (
	// DataDirName is the per-project directory that holds all issue files.
	DataDirName = ".tasks"
	// ConfigFileName is the project config inside the data directory.
	ConfigFileName = "config.yaml"
	// FileExt is the extension of an issue file.
	FileExt = ".md"

	lockFileName = ".lock"
)

// Errors returned by the store. Callers should test with errors.Is.
var (
	ErrNotFound      = errors.New("issue not found")
	ErrAlreadyExists = errors.New("issue already exists")
	ErrNoStore       = errors.New("no .tasks directory found")
	ErrStoreExists   = errors.New(".tasks directory already exists")
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
}

// Store is the single gateway to a project's issue files. Every read and write
// goes through it, so it is the one place that enforces the on-disk format,
// validation, and locking. Nothing else should touch the files directly.
type Store struct {
	root string // project root (the parent of the data dir)
	dir  string // absolute path to the data directory (.tasks)
	cfg  Config
	fs   vfs.FS // disk seam; always vfs.NewOS() in production

	// now returns the current time; overridable in tests.
	now func() time.Time
}

// openWithFS is an unexported test hook that constructs a Store rooted at
// root using the provided FS implementation. It does NOT read or create the
// config — the caller must set s.cfg directly (or call readConfig through s.fs
// after construction). This hook exists so tests can inject vfs.Mem or other
// FS implementations without going through Init/Open.
func openWithFS(root string, fs vfs.FS) *Store {
	absRoot, _ := filepath.Abs(root)
	return &Store{
		root: absRoot,
		dir:  filepath.Join(absRoot, DataDirName),
		fs:   fs,
		now:  defaultNow,
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
	s := &Store{root: root, dir: dir, cfg: cfg, fs: fs, now: defaultNow}
	if err := s.writeConfig(cfg); err != nil {
		return nil, err
	}
	return s, nil
}

// Init creates a new data directory under root with the given ID prefix and
// returns an open Store. It fails if a data directory already exists.
func Init(root, prefix string) (*Store, error) {
	prefix = strings.TrimSpace(prefix)
	if !prefixRe.MatchString(prefix) {
		return nil, fmt.Errorf("invalid prefix %q: must match %s", prefix, prefixRe.String())
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	fs := vfs.NewOS()
	dir := filepath.Join(absRoot, DataDirName)
	if _, err := fs.Stat(dir); err == nil {
		return nil, ErrStoreExists
	}
	if err := fs.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	cfg := Config{Prefix: prefix}
	s := &Store{root: absRoot, dir: dir, cfg: cfg, fs: fs, now: defaultNow}
	if err := s.writeConfig(cfg); err != nil {
		return nil, err
	}
	return s, nil
}

// Open locates the data directory by walking up from start (or the current
// working directory if start is empty) and loads its config.
func Open(start string) (*Store, error) {
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
	for {
		dir := filepath.Join(abs, DataDirName)
		if fi, err := fs.Stat(dir); err == nil && fi.IsDir() {
			s := &Store{root: abs, dir: dir, fs: fs, now: defaultNow}
			cfg, err := s.readConfig()
			if err != nil {
				return nil, err
			}
			s.cfg = cfg
			return s, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return nil, ErrNoStore
		}
		abs = parent
	}
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

// Get loads a single issue by ID.
func (s *Store) Get(id string) (*Issue, error) {
	data, err := s.fs.ReadFile(s.filePath(id))
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

// All loads every issue in the store. Order is by ID for determinism.
func (s *Store) All() ([]*Issue, error) {
	entries, err := s.fs.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var issues []*Issue
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, FileExt) {
			continue
		}
		data, err := s.fs.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			return nil, err
		}
		iss, err := Unmarshal(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		issues = append(issues, iss)
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })
	return issues, nil
}

// index loads all issues into a map keyed by ID.
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

// nextID allocates the next sequential ID by scanning existing files for the
// highest number and adding one. There is no counter file, so the only way two
// IDs collide is concurrent creation on different git branches.
//
// The computation is delegated to the pure nextIDFromNames function in ids.go;
// this method is responsible only for reading the directory via the vfs seam.
func (s *Store) nextID() (string, error) {
	entries, err := s.fs.ReadDir(s.dir)
	if err != nil {
		return "", err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return nextIDFromNames(s.cfg.Prefix, names), nil
}

// writeIssue atomically writes an issue to disk via the FS seam. The
// caller must hold the store lock.
func (s *Store) writeIssue(iss *Issue) error {
	data, err := Marshal(iss)
	if err != nil {
		return err
	}
	return s.fs.WriteAtomic(s.filePath(iss.ID), data, 0o644)
}

// withLock runs fn while holding an exclusive advisory lock on the store, so
// concurrent atctl processes cannot interleave writes.
func (s *Store) withLock(fn func() error) error {
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
	Title       string
	Description string
	Type        Type
	Priority    *int
	Assignee    string
	Labels      []string
	Parent      string
	BlockedBy   []string
	Related     []string
}

// Create validates and writes a new issue, allocating its ID.
func (s *Store) Create(in CreateInput) (*Issue, error) {
	var created *Issue
	err := s.withLock(func() error {
		id, err := s.nextID()
		if err != nil {
			return err
		}
		now := s.now()
		iss := &Issue{
			ID:          id,
			Title:       strings.TrimSpace(in.Title),
			Status:      StatusOpen,
			Type:        in.Type,
			Priority:    PriorityDefault,
			Assignee:    in.Assignee,
			Labels:      dedupe(in.Labels),
			Parent:      in.Parent,
			BlockedBy:   dedupe(in.BlockedBy),
			Related:     dedupe(in.Related),
			Created:     now,
			Updated:     now,
			Description: in.Description,
		}
		if iss.Type == "" {
			iss.Type = TypeTask
		}
		if in.Priority != nil {
			iss.Priority = *in.Priority
		}
		if err := validateFields(iss); err != nil {
			return err
		}
		if err := s.checkRefs(iss); err != nil {
			return err
		}
		if err := s.writeIssue(iss); err != nil {
			return err
		}
		created = iss
		return nil
	})
	return created, err
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
func (s *Store) Update(id string, in UpdateInput) (*Issue, error) {
	var updated *Issue
	err := s.withLock(func() error {
		iss, err := s.Get(id)
		if err != nil {
			return err
		}
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
		if in.Status != nil {
			s.applyStatus(iss, *in.Status, "")
		}
		iss.Updated = s.now()

		if err := validateFields(iss); err != nil {
			return err
		}
		if err := s.checkRefs(iss); err != nil {
			return err
		}
		if err := s.writeIssue(iss); err != nil {
			return err
		}
		updated = iss
		return nil
	})
	return updated, err
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

// Close marks an issue closed with an optional reason. It is idempotent:
// closing an already-closed issue updates the reason but does not error.
func (s *Store) Close(id, reason string) (*Issue, error) {
	var closed *Issue
	err := s.withLock(func() error {
		iss, err := s.Get(id)
		if err != nil {
			return err
		}
		s.applyStatus(iss, StatusClosed, reason)
		iss.Updated = s.now()
		if err := s.writeIssue(iss); err != nil {
			return err
		}
		closed = iss
		return nil
	})
	return closed, err
}

// AddComment appends an immutable comment to an issue.
func (s *Store) AddComment(id, author, body string) (*Issue, error) {
	var out *Issue
	err := s.withLock(func() error {
		iss, err := s.Get(id)
		if err != nil {
			return err
		}
		iss.Comments = append(iss.Comments, Comment{
			Author:  author,
			Created: s.now(),
			Body:    strings.TrimSpace(body),
		})
		iss.Updated = s.now()
		if err := s.writeIssue(iss); err != nil {
			return err
		}
		out = iss
		return nil
	})
	return out, err
}

// AddDep records that dependent is blocked by blocker.
func (s *Store) AddDep(dependent, blocker string) error {
	return s.withLock(func() error {
		iss, err := s.Get(dependent)
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
		iss, err := s.Get(dependent)
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

// checkRefs verifies that every referenced ID exists and that adding the
// issue's blockers does not create a dependency cycle. Caller holds the lock.
func (s *Store) checkRefs(iss *Issue) error {
	idx, _, err := s.index()
	if err != nil {
		return err
	}
	idx[iss.ID] = iss // include the (possibly new) issue itself

	if iss.Parent != "" {
		if _, ok := idx[iss.Parent]; !ok {
			return invalid("parent", "referenced issue %q does not exist", iss.Parent)
		}
	}
	for _, id := range iss.BlockedBy {
		if _, ok := idx[id]; !ok {
			return invalid("blocked_by", "referenced issue %q does not exist", id)
		}
	}
	for _, id := range iss.Related {
		if _, ok := idx[id]; !ok {
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
