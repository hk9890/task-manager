package vfs

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// fault is a single registered error injection: when the named op is called
// with a path matching pathGlob, it returns err instead of executing.
// A fault is consumed on first match (fires exactly once).
type fault struct {
	op       string
	pathGlob string
	err      error
}

// memFileInfo implements os.FileInfo for in-memory files and directories.
type memFileInfo struct {
	name  string
	size  int64
	isDir bool
}

func (fi *memFileInfo) Name() string       { return filepath.Base(fi.name) }
func (fi *memFileInfo) Size() int64        { return fi.size }
func (fi *memFileInfo) Mode() os.FileMode  { return 0o644 }
func (fi *memFileInfo) ModTime() time.Time { return time.Time{} }
func (fi *memFileInfo) IsDir() bool        { return fi.isDir }
func (fi *memFileInfo) Sys() any           { return nil }

// memDirEntry implements os.DirEntry for ReadDir results. It holds (does not
// embed) a memFileInfo so it does not accidentally promote memFileInfo's
// os.FileInfo methods.
type memDirEntry struct{ fi memFileInfo }

func (e *memDirEntry) Name() string               { return e.fi.Name() }
func (e *memDirEntry) IsDir() bool                { return e.fi.IsDir() }
func (e *memDirEntry) Type() os.FileMode          { return 0 }
func (e *memDirEntry) Info() (os.FileInfo, error) { return &e.fi, nil }

// Mem is an in-memory implementation of FS. It is safe for concurrent use.
//
// WriteAtomic and Append are plain map writes (fsync is a no-op). Lock is an
// in-process mutex — it does not prove cross-process flock behaviour (that is
// L3's job). ReadDir sees all prior writes within the same Mem.
//
// Use FailOn to inject errors: the first fault whose op+pathGlob matches a
// call returns the registered error and is consumed (fires exactly once). Call
// FailOn multiple times to inject multiple failures on the same call site.
type Mem struct {
	mu    sync.Mutex
	files map[string][]byte // full path → content
	dirs  map[string]bool   // full paths of known directories

	// faults is a FIFO queue of registered faults. The first matching entry
	// for a given (op, path) pair is consumed and returned.
	faults []fault

	// locks maps lock paths to their in-process mutex. Access is guarded by mu.
	locks map[string]*sync.Mutex
}

// NewMem returns a new empty in-memory FS.
func NewMem() *Mem {
	return &Mem{
		files: make(map[string][]byte),
		dirs:  make(map[string]bool),
		locks: make(map[string]*sync.Mutex),
	}
}

// FailOn registers a fault: the next call to op whose path matches pathGlob
// will return err instead of executing. The fault is consumed after it fires.
// pathGlob follows filepath.Match semantics. Multiple calls to FailOn stack
// (FIFO): each registers one additional failure.
func (m *Mem) FailOn(op, pathGlob string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.faults = append(m.faults, fault{op: op, pathGlob: pathGlob, err: err})
}

// checkFault looks for a matching fault for (op, path), consumes it if found,
// and returns its error. Returns nil if no fault matches. Must be called with
// m.mu held.
func (m *Mem) checkFault(op, path string) error {
	for i, f := range m.faults {
		if f.op != op {
			continue
		}
		matched, err := filepath.Match(f.pathGlob, path)
		if err != nil {
			// Invalid glob — skip it.
			continue
		}
		if matched {
			// Consume this fault (remove from slice).
			m.faults = append(m.faults[:i], m.faults[i+1:]...)
			return f.err
		}
	}
	return nil
}

// ensureDir records the directory path and all its ancestors as known
// directories. Must be called with m.mu held.
func (m *Mem) ensureDir(dir string) {
	for d := dir; d != "/" && d != "."; d = filepath.Dir(d) {
		if m.dirs[d] {
			break
		}
		m.dirs[d] = true
	}
	m.dirs["/"] = true
}

// requireParentDir returns an os.ErrNotExist-wrapped error when the parent
// directory of name does not exist. what labels the directory in the message
// (e.g. "parent directory" or "destination parent directory"). Must be called
// with m.mu held.
func (m *Mem) requireParentDir(name, what string) error {
	parent := filepath.Dir(name)
	if !m.dirs[parent] {
		return fmt.Errorf("%w: %s does not exist: %s", os.ErrNotExist, what, parent)
	}
	return nil
}

// ReadDir reads the named directory and returns a sorted list of entries
// (files and immediate subdirectories) whose parent is exactly dir.
func (m *Mem) ReadDir(dir string) ([]os.DirEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFault("ReadDir", dir); err != nil {
		return nil, err
	}

	// Normalise dir: ensure no trailing slash (except root).
	dir = filepath.Clean(dir)

	// Collect direct children: files and directories.
	seen := make(map[string]bool)
	var entries []os.DirEntry

	for path, data := range m.files {
		if filepath.Dir(path) == dir {
			name := filepath.Base(path)
			if !seen[name] {
				seen[name] = true
				entries = append(entries, &memDirEntry{fi: memFileInfo{name: name, size: int64(len(data)), isDir: false}})
			}
		}
	}

	for d := range m.dirs {
		if d == dir {
			continue // skip the directory itself
		}
		if filepath.Dir(d) == dir {
			name := filepath.Base(d)
			if !seen[name] {
				seen[name] = true
				entries = append(entries, &memDirEntry{fi: memFileInfo{name: name, isDir: true}})
			}
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	return entries, nil
}

// ReadFile reads and returns the contents of the named file.
func (m *Mem) ReadFile(name string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFault("ReadFile", name); err != nil {
		return nil, err
	}

	data, ok := m.files[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", os.ErrNotExist, name)
	}
	return bytes.Clone(data), nil
}

// Stat returns FileInfo for the named file or directory.
func (m *Mem) Stat(name string) (os.FileInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFault("Stat", name); err != nil {
		return nil, err
	}

	if data, ok := m.files[name]; ok {
		return &memFileInfo{name: name, size: int64(len(data))}, nil
	}
	if m.dirs[name] {
		return &memFileInfo{name: name, isDir: true}, nil
	}
	return nil, fmt.Errorf("%w: %s", os.ErrNotExist, name)
}

// WriteAtomic writes data to name, replacing any previous content. On Mem the
// write is atomic by definition — there is no torn intermediate state.
//
// The parent directory must already exist (created via MkdirAll); if it does
// not, WriteAtomic returns an os.ErrNotExist-wrapped error, matching the
// behaviour of the real osFS which calls os.CreateTemp and fails with ENOENT
// when the parent directory is absent.
func (m *Mem) WriteAtomic(name string, data []byte, perm os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFault("WriteAtomic", name); err != nil {
		return err
	}

	if err := m.requireParentDir(name, "parent directory"); err != nil {
		return err
	}

	m.files[name] = bytes.Clone(data)
	return nil
}

// Append appends data to the named file, creating it if it does not exist.
//
// The parent directory must already exist (created via MkdirAll); if it does
// not, Append returns an os.ErrNotExist-wrapped error, matching the behaviour
// of the real osFS which calls os.OpenFile with O_APPEND and fails with ENOENT
// when the parent directory is absent.
func (m *Mem) Append(name string, data []byte, perm os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFault("Append", name); err != nil {
		return err
	}

	if err := m.requireParentDir(name, "parent directory"); err != nil {
		return err
	}

	existing := m.files[name]
	combined := make([]byte, len(existing)+len(data))
	copy(combined, existing)
	copy(combined[len(existing):], data)
	m.files[name] = combined
	return nil
}

// Rename moves oldpath to newpath. If a fault is registered for "Rename" and
// the oldpath matches, the error is returned and no files are modified.
//
// Mem.Rename supports files only. Renaming a directory is not implemented
// (it would silently leave child-path keys orphaned). Callers that need to
// move a directory should use a sequence of file-level Rename calls after
// re-creating the target directory with MkdirAll. In production code the only
// Rename calls go through closeMove and openWithFS, which always rename a
// single task .md file between two directories that already exist.
//
// The destination parent directory must already exist; if it does not, Rename
// returns an os.ErrNotExist-wrapped error, matching real osFS behaviour.
func (m *Mem) Rename(oldpath, newpath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFault("Rename", oldpath); err != nil {
		return err
	}

	// Reject attempts to rename a directory.
	if m.dirs[oldpath] {
		return fmt.Errorf("vfs.Mem.Rename: renaming directories is not supported: %s", oldpath)
	}

	data, ok := m.files[oldpath]
	if !ok {
		return fmt.Errorf("%w: %s", os.ErrNotExist, oldpath)
	}

	// Require the destination parent directory to exist.
	if err := m.requireParentDir(newpath, "destination parent directory"); err != nil {
		return err
	}

	m.files[newpath] = bytes.Clone(data)
	delete(m.files, oldpath)
	return nil
}

// MkdirAll records dir and all its ancestors as known directories. On Mem this
// is a no-op for purposes of file access (files can be written anywhere), but
// it makes the directory visible in ReadDir.
func (m *Mem) MkdirAll(dir string, perm os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFault("MkdirAll", dir); err != nil {
		return err
	}

	dir = filepath.Clean(dir)
	m.ensureDir(dir)
	return nil
}

// Remove removes the named file.
func (m *Mem) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFault("Remove", name); err != nil {
		return err
	}

	if _, ok := m.files[name]; !ok {
		// Also check if it is a directory.
		if m.dirs[name] {
			delete(m.dirs, name)
			return nil
		}
		return fmt.Errorf("%w: %s", os.ErrNotExist, name)
	}
	delete(m.files, name)
	return nil
}

// Lock acquires an in-process exclusive lock on path. Multiple sequential
// callers within the same process are serialized. The lock does not interact
// with the real filesystem — cross-process locking is an L3 concern.
func (m *Mem) Lock(path string) (func() error, error) {
	m.mu.Lock()
	lmu, ok := m.locks[path]
	if !ok {
		lmu = &sync.Mutex{}
		m.locks[path] = lmu
	}
	m.mu.Unlock()

	lmu.Lock()
	unlock := func() error {
		lmu.Unlock()
		return nil
	}
	return unlock, nil
}

// Getwd returns "/" as the working directory for the in-memory filesystem.
func (m *Mem) Getwd() (string, error) {
	return "/", nil
}

// compile-time check that Mem satisfies FS.
var _ FS = (*Mem)(nil)
