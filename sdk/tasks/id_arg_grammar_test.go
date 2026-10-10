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
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// An ID argument is joined onto a store directory. "../<id>" resolved to the
// hot file through closed/, so Close took an open issue for a closed one,
// answered a successful no-op and left it open; "closed/<id>" resolved to the
// closed file through the hot directory, so a closed issue was edited in place.

// idArgOps is every public operation that takes an issue ID and reaches a path.
var idArgOps = map[string]func(s *Store, id, other string) error{
	"Get":           func(s *Store, id, _ string) error { _, err := s.Get(id); return err },
	"Detail":        func(s *Store, id, _ string) error { _, err := s.Detail(id); return err },
	"Details":       func(s *Store, id, other string) error { _, err := s.Details(other, id); return err },
	"Comments":      func(s *Store, id, _ string) error { _, err := s.Comments(id); return err },
	"Close":         func(s *Store, id, _ string) error { _, err := s.Close(id, "done"); return err },
	"Reopen":        func(s *Store, id, _ string) error { _, err := s.Reopen(id); return err },
	"AddDep":        func(s *Store, id, other string) error { return s.AddDep(id, other) },
	"RemoveDep":     func(s *Store, id, other string) error { return s.RemoveDep(id, other) },
	"AddRelated":    func(s *Store, id, other string) error { return s.AddRelated(id, other) },
	"RemoveRelated": func(s *Store, id, other string) error { return s.RemoveRelated(id, other) },
	"Update": func(s *Store, id, _ string) error {
		title := "renamed"
		_, err := s.Update(id, UpdateInput{Title: &title})
		return err
	},
	"AddComment": func(s *Store, id, _ string) error {
		_, err := s.AddComment(id, Actor{Name: "ann"}, "hello")
		return err
	},
	"EditComment": func(s *Store, id, _ string) error {
		_, err := s.EditComment(id, "c1", Actor{Name: "ann"}, "hello")
		return err
	},
	"DeleteComment": func(s *Store, id, _ string) error { return s.DeleteComment(id, "c1", Actor{Name: "ann"}) },
}

func TestIDArgument_OutsideTheIDGrammar_IsNotFound(t *testing.T) {
	for name, op := range idArgOps {
		t.Run(name, func(t *testing.T) {
			s, fs := newMemStore(t)
			open, err := unwrap(s.Create(CreateInput{Title: "open"}))
			if err != nil {
				t.Fatal(err)
			}
			other, err := unwrap(s.Create(CreateInput{Title: "other"}))
			if err != nil {
				t.Fatal(err)
			}
			done, err := unwrap(s.Create(CreateInput{Title: "done"}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Close(done.ID, "done"); err != nil {
				t.Fatal(err)
			}
			paths := []string{s.filePath(open.ID), s.closedFilePath(done.ID)}
			before := readFiles(t, fs, paths)

			for _, id := range []string{
				"../" + open.ID, "nope/../" + open.ID, "./" + open.ID,
				closedDirName + "/" + done.ID, "",
			} {
				if err := op(s, id, other.ID); !errors.Is(err, ErrNotFound) {
					t.Errorf("%q: got %v, want ErrNotFound", id, err)
				}
			}

			for i, after := range readFiles(t, fs, paths) {
				if !bytes.Equal(before[i], after) {
					t.Errorf("%s was rewritten:\n%s", filepath.Base(paths[i]), after)
				}
			}
			for _, id := range []string{open.ID, done.ID} {
				if comments, err := s.Comments(id); err != nil || len(comments) != 0 {
					t.Errorf("%s has %d comments (err %v), want none", id, len(comments), err)
				}
			}
		})
	}
}

// The plain ID still reaches the issue through the operations the check guards.
func TestIDArgument_PlainID_StillResolves(t *testing.T) {
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "open"}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Get(iss.ID); err != nil {
		t.Errorf("Get: %v", err)
	}
	title := "renamed"
	if _, err := s.Update(iss.ID, UpdateInput{Title: &title}); err != nil {
		t.Errorf("Update: %v", err)
	}
	if _, err := s.AddComment(iss.ID, Actor{Name: "ann"}, "hello"); err != nil {
		t.Errorf("AddComment: %v", err)
	}
	if _, err := s.Close(iss.ID, "done"); err != nil {
		t.Errorf("Close: %v", err)
	}
	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Status.IsClosed() || got.Title != title {
		t.Errorf("got status %q, title %q; want closed, %q", got.Status, got.Title, title)
	}
}

func readFiles(t *testing.T, fs vfs.FS, paths []string) [][]byte {
	t.Helper()
	out := make([][]byte, len(paths))
	for i, p := range paths {
		data, err := fs.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		out[i] = data
	}
	return out
}

// Every public method reaches getUnresolved first, so the guards behind it are
// asserted directly.
func TestIssueFilePath_IDOutsideTheIDGrammar_IsNotFound(t *testing.T) {
	s, _ := newMemStore(t)
	iss, err := unwrap(s.Create(CreateInput{Title: "open"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../" + iss.ID, "nope/../" + iss.ID, "./" + iss.ID, "closed/" + iss.ID, ""} {
		if _, err := s.issueFilePath(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("issueFilePath(%q): got %v, want ErrNotFound", id, err)
		}
		if _, err := s.isInClosed(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("isInClosed(%q): got %v, want ErrNotFound", id, err)
		}
	}
}
