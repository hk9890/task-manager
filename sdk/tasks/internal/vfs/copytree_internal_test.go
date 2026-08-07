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

//go:build integration

// Internal L3 tests for the copy walker behind MoveTree's cross-device
// fallback. They call copyTree directly so the walker is exercised on every
// machine, not only where two filesystems happen to be reachable — it is the
// one part of a promote that can lose data if it under-copies silently.
package vfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyTree_CopiesEverything(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(filepath.Join(src, "closed"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(src, "comments", "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	files := map[string]string{
		"config.yaml":            "prefix: prj\n",
		"prj-aaa111.md":          "hot task\n",
		"closed/prj-bbb222.md":   "closed task\n",
		"comments/prj-aaa.yaml":  "- body: one\n",
		"comments/nested/x.yaml": "- body: deep\n",
		".lock":                  "",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(src, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	dst := filepath.Join(base, "dst")
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil {
			t.Errorf("missing %s at destination: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}

	// Nothing extra, nothing missing: compare the two trees entry for entry.
	if a, b := walkRel(t, src), walkRel(t, dst); strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Errorf("trees differ:\n src: %v\n dst: %v", a, b)
	}
	// The source is untouched — copyTree copies, MoveTree does the removing.
	if _, err := os.Stat(filepath.Join(src, "config.yaml")); err != nil {
		t.Errorf("source must be intact after copyTree: %v", err)
	}
}

func TestCopyTree_PreservesPermissions(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	dst := filepath.Join(base, "dst")
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	fi, err := os.Stat(filepath.Join(dst, "secret"))
	if err != nil {
		t.Fatalf("stat copied file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 600", perm)
	}
	di, err := os.Stat(filepath.Join(dst, "sub"))
	if err != nil {
		t.Fatalf("stat copied dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir perm = %o, want 700", perm)
	}
}

// TestCopyTree_RejectsSymlink pins the strict-walker contract: an entry that is
// neither a directory nor a regular file is an error, never a silent omission.
// MoveTree relies on this — it removes the source only after a clean return.
func TestCopyTree_RejectsSymlink(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "real"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink("real", filepath.Join(src, "link")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	err := copyTree(src, filepath.Join(base, "dst"))
	if err == nil {
		t.Fatal("copyTree must refuse a symlink rather than skip it")
	}
	if !strings.Contains(err.Error(), "unsupported file type") {
		t.Errorf("err = %v, want an unsupported-file-type error", err)
	}
}

func TestCopyTree_RefusesExistingDestination(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	dst := filepath.Join(base, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := copyTree(src, dst); err == nil {
		t.Fatal("copyTree onto an existing directory should fail")
	}
}

// walkRel returns every path under root, relative to it, in walk order.
func walkRel(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}
