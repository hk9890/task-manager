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

// content.go — the disk half of body overflow: reading and writing the content
// sidecar at content/<id>. The rule itself (when to split, when to re-join, what
// the two pieces contain) is pure and lives in overflow.go.
//
// Imperative shell: this file may import internal/vfs (ARCHITECTURE-SPEC §5).
package tasks

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// contentDir returns the absolute path to the content/ subdirectory.
func (s *Store) contentDir() string {
	return filepath.Join(s.dir, contentDirName)
}

// contentPath returns the sidecar path for an issue's body.
//
// The path is derived entirely from the ID — there is no stored path, and no
// extension. The ID is not user-supplied in the sense that matters: it comes
// from an allocation or from a frontmatter field that Unmarshal has already
// checked against the issue-ID grammar (validIssueID), which admits no path
// separator and no dot. Traversal is closed off there, at the parse boundary,
// because that is the only place an ID can enter the store. The absent extension
// is the accepted cost: the bytes could be HTML, markdown or a log, and the
// store does not record which.
func (s *Store) contentPath(id string) string {
	return filepath.Join(s.contentDir(), id)
}

// stagedContentPath returns the path new sidecar bytes are written to before
// they replace an existing body — see writeFiles. The leading dot puts it
// outside the issue-ID grammar, so it can never collide with a real sidecar.
func (s *Store) stagedContentPath(id string) string {
	return filepath.Join(s.contentDir(), "."+id+".incoming")
}

// previousLayout reports whether the issue currently on disk keeps its body in
// the sidecar. It reads the .md from whichever partition holds it; an issue that
// does not exist yet (a create) is not external.
//
// The previous layout is read from disk rather than carried on the in-memory
// Issue on purpose. Issue.bodyExternal is a strict mirror of the stored state —
// true only alongside an empty Description — so an issue whose body has been
// resolved cannot also report where that body came from. Reading it back costs
// one ReadFile of a file that is bounded at MaxInlineBody by construction, on a
// path that is already fsync-bound.
func (s *Store) previousLayout(id string) (bool, error) {
	path, err := s.issueFilePath(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) || vfs.IsNotExist(err) {
			return false, nil // creating: nothing on disk yet
		}
		return false, err
	}
	data, err := s.fs.ReadFile(path)
	if err != nil {
		if vfs.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	prev, err := Unmarshal(data)
	if err != nil {
		// A malformed file is reported by the read paths; for the layout decision
		// treat it as inline, so the write that follows is self-correcting.
		return false, nil //nolint:nilerr // deliberate: layout falls back to inline
	}
	return prev.bodyExternal, nil
}

// writeFiles performs the two-file write for iss: the sidecar (if any), then the
// .md via writeMD, then the removal of a stale sidecar (if any).
//
// The ordering is the crash contract (TASK-STORAGE-SPEC §4.6). The .md's
// body_external flag decides which file is authoritative, so the .md is written
// last on a split and first on a join. Either way a crash between the two leaves
// the .md and the body it names in agreement, and any sidecar nothing points at
// is inert garbage rather than a silent override of the .md.
//
// A body that is ALREADY external is never overwritten in place. The new bytes
// are staged beside it and renamed over it only once the .md has landed, because
// the obvious order — overwrite the sidecar, then write the .md — commits the
// new body even when the .md write fails. The caller is told the mutation failed
// (Update returns the error, gateWrite logs io_error and fires no post-hooks)
// while the next read already returns the new body under the old frontmatter.
// Staging keeps the previous body readable until the mutation is real.
//
// It is still NOT atomic across the pair: a crash between the .md and the rename
// leaves the new frontmatter beside the previous body. That is accepted — the
// alternative is a journal — and it never loses a body, and never reports a
// write as failed after committing it.
//
// The caller holds the store lock.
//
// Every issue write funnels through here — the hot path (writeIssue), the close
// move, and the reopen move — so this is where validation runs. Putting it at
// the funnel rather than in each caller is what makes "the engine never writes
// an issue it would refuse to re-validate" true by construction: Close/Reopen
// and the four edge mutations reach the FS through this function without passing
// validateAndIndex. The gated paths validate twice; the check is in-memory and
// the write is already the expensive half.
//
// The check is validateWrite, not validateFields: a violation already on disk
// must not freeze the issue it is on, or an issue with a hand-edited `creator`
// could never be closed again (mutation.go states the rule).
func (s *Store) writeFiles(iss *Issue, writeMD func(md []byte) error) error {
	if err := s.validateWrite(iss); err != nil {
		return err
	}
	prevExternal, err := s.previousLayout(iss.ID)
	if err != nil {
		return err
	}
	md, sidecar, dropSidecar, err := renderForWrite(iss, prevExternal)
	if err != nil {
		return err
	}

	// staged is set only when there is a previous body to protect. On a split
	// there is none: the .md that will name the sidecar has not landed yet, so
	// the bytes go straight to the final path and a crash leaves a sidecar
	// nothing points at.
	staged := ""
	if sidecar != nil {
		if err := s.fs.MkdirAll(s.contentDir(), 0o755); err != nil {
			return fmt.Errorf("create content dir: %w", err)
		}
		target := s.contentPath(iss.ID)
		if prevExternal {
			staged = s.stagedContentPath(iss.ID)
			target = staged
		}
		if err := s.fs.WriteAtomic(target, sidecar, 0o644); err != nil {
			return fmt.Errorf("write content sidecar %s: %w", iss.ID, err)
		}
	}

	if err := writeMD(md); err != nil {
		if staged != "" {
			// Nothing is committed: the staged body is garbage, and the body the
			// .md on disk still names is untouched.
			_ = s.fs.Remove(staged)
		}
		return err
	}

	if staged != "" {
		if err := s.fs.Rename(staged, s.contentPath(iss.ID)); err != nil {
			return fmt.Errorf("commit content sidecar %s: %w", iss.ID, err)
		}
	}

	if dropSidecar {
		// The .md is committed and says the body is inline, so the mutation has
		// landed. Reporting a failure here would report a write that succeeded as
		// failed — the caller would see an error, no post-hooks, and the new body
		// on the next read. The leftover file is inert: nothing points at it, and
		// the next overflow overwrites it.
		_ = s.fs.Remove(s.contentPath(iss.ID))
	}
	return nil
}

// resolveBody fills in iss.Description from the content sidecar when the body
// lives there, and clears the flag so the result is safe to hand to Marshal.
// An issue whose body is inline is returned untouched.
func (s *Store) resolveBody(iss *Issue) error {
	if !iss.bodyExternal {
		return nil
	}
	data, err := s.fs.ReadFile(s.contentPath(iss.ID))
	if err != nil {
		if vfs.IsNotExist(err) {
			return fmt.Errorf("content sidecar missing for %s: %w", iss.ID, err)
		}
		return err
	}
	iss.Description = strings.TrimSpace(string(data))
	iss.bodyExternal = false
	return nil
}

// resolvedCopy returns a copy of iss with its body filled in from the sidecar,
// leaving the original untouched. Used by the query path, which needs the body
// to evaluate a text predicate but must not hand a populated Description back to
// a caller of a bulk read (SDK-SPEC §4).
func (s *Store) resolvedCopy(iss *Issue) (*Issue, error) {
	dup := *iss
	if err := s.resolveBody(&dup); err != nil {
		return nil, err
	}
	return &dup, nil
}

// ResolveBody fills in an issue's Description when its body is stored in the
// content sidecar, and is a no-op otherwise.
//
// The bulk read paths (All, Query, List, Ready, Blocked, Find) deliberately do
// NOT read sidecars: an overflowed issue comes back with an empty Description so
// that listing a thousand issues can never materialize gigabytes. This is how a
// caller asks for the body of one of them, without the second lookup that going
// back through Get would cost. Get and Detail already resolve on their own.
//
// Safe to call on any issue, resolved or not. After it returns, iss is safe to
// pass to Marshal.
func (s *Store) ResolveBody(iss *Issue) error {
	if iss == nil {
		return nil
	}
	return s.resolveBody(iss)
}
