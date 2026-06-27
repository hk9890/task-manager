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

package tasks_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImportBoundary_OnlyVfsImportsOS verifies that no non-test Go file
// outside the three seams — sdk/tasks/internal/vfs (disk),
// sdk/tasks/internal/exec (hook processes), and sdk/tasks/internal/env (user
// environment, for store resolution) — imports the "os" or "syscall" packages.
// This is the grep-guard for the single-writer / seam rule: os and syscall are
// concentrated in those three seams only.
func TestImportBoundary_OnlyVfsImportsOS(t *testing.T) {
	// Locate the sdk/tasks root by walking up from the test binary's working
	// directory. We search for the directory that contains store.go.
	sdkTasksDir, err := findSDKTasksDir()
	if err != nil {
		t.Skipf("cannot locate sdk/tasks dir: %v", err)
	}

	forbidden := []string{"os", "syscall"}

	err = filepath.WalkDir(sdkTasksDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Only inspect .go files; skip test files; skip the two seams that are
		// permitted os/syscall (vfs = disk, exec = hook processes) and the
		// storetest package (test-only support package; may use os like vfs).
		if d.IsDir() {
			if d.Name() == "vfs" || d.Name() == "exec" || d.Name() == "env" || d.Name() == "storetest" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			// imp.Path.Value is a quoted string like `"os"`.
			pkg := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				if pkg == bad || strings.HasPrefix(pkg, bad+"/") {
					t.Errorf("non-vfs file %s imports forbidden package %q", path, pkg)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
}

// TestImportBoundary_PureCoreNoVfs verifies that pure-core files in the tasks
// package (all non-test .go files except the imperative-shell files store.go
// and comments.go) do not import internal/vfs. These files must remain pure:
// they take in-memory inputs and return values/errors, enabling L1 unit tests
// without any filesystem dependency.
//
// Pure-core files: ids.go, model.go, frontmatter.go, validate.go, ready.go,
// doc.go, and any future file that is not explicitly the imperative shell.
func TestImportBoundary_PureCoreNoVfs(t *testing.T) {
	sdkTasksDir, err := findSDKTasksDir()
	if err != nil {
		t.Skipf("cannot locate sdk/tasks dir: %v", err)
	}

	// imperativeShell lists the files that are allowed to import vfs because
	// they form the imperative shell that connects pure logic to the disk seam.
	imperativeShell := map[string]bool{
		"store.go":    true,
		"comments.go": true,
		"config.go":   true, // global config loader (env/vfs seams)
		"registry.go": true, // central registry + Resolve/Stores/InitCentral
	}

	const vfsPkg = "github.com/hk9890/task-manager/sdk/tasks/internal/vfs"

	entries, err := os.ReadDir(sdkTasksDir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", sdkTasksDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if imperativeShell[name] {
			continue // shell files may import vfs
		}

		path := filepath.Join(sdkTasksDir, name)
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("parse %s: %v", path, err)
			continue
		}
		for _, imp := range f.Imports {
			pkg := strings.Trim(imp.Path.Value, `"`)
			if pkg == vfsPkg || strings.HasPrefix(pkg, vfsPkg+"/") {
				t.Errorf("pure-core file %s must not import vfs (got %q)", name, pkg)
			}
		}
	}
}

// findSDKTasksDir walks up from the current working directory to find the
// sdk/tasks directory (identified by the presence of store.go).
func findSDKTasksDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// The test runs inside sdk/tasks (or a subdirectory).
	// Walk up to find the directory containing store.go.
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "store.go")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", os.ErrNotExist
}
