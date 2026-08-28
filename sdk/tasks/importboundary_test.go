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
	"go/ast"
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
		// Only inspect .go files; skip test files; skip the three seams that are
		// permitted os/syscall (vfs = disk, exec = hook processes, env = user
		// environment). internal/storetest is deliberately NOT skipped: it is
		// test-only support, which would have excused an os import, but its
		// fixtures reach disk through the vfs seam like everything else, so it
		// is held to the rule instead of sitting outside it unwatched.
		if d.IsDir() {
			if d.Name() == "vfs" || d.Name() == "exec" || d.Name() == "env" {
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

// TestImportBoundary_PureCoreNoSeams verifies that pure-core files in the tasks
// package stay pure: they take in-memory inputs and return values/errors, so
// they can be unit-tested at L1 with no filesystem at all.
//
// A file breaks that rule two ways, and checking only the first is what let
// ready.go read disk on every call while this test passed it:
//
//  1. It imports one of the three seams — internal/vfs (disk),
//     internal/env (user environment) or internal/exec (hook processes).
//  2. It declares a method on *Store. A method needs no import to reach disk —
//     it goes through the s.fs field, which no import list reveals — and it
//     cannot be called without constructing a store, which is the property that
//     actually makes L1 testing impossible.
//
// All three seams are checked, each against its own exemption list. The guard
// once compared imports against the vfs path alone, so a pure-core file could
// import internal/env and read the process environment with every gate green —
// the boundary had a third of a guard and read as if it had a whole one.
//
// The lists are per-rule as well as per-seam: a file that hangs methods off
// *Store still has its imports checked unless it is also on that seam's list.
// Pure-core files — ids.go, model.go, frontmatter.go, validate.go, ready.go,
// criteria.go, doc.go, and any future file on no list — are held to all of them.
func TestImportBoundary_PureCoreNoSeams(t *testing.T) {
	sdkTasksDir, err := findSDKTasksDir()
	if err != nil {
		t.Skipf("cannot locate sdk/tasks dir: %v", err)
	}

	// One exemption list per seam, plus one for the *Store-method rule, because
	// they exempt different files. A single shared list short-circuits every check
	// at once: adding a file so it may declare a *Store method would silently stop
	// checking its imports too, and the seam guards would then cover neither it nor
	// the shell files that never needed an import exemption in the first place.
	//
	// Keep every list minimal: each entry is a file the guard stops checking, so
	// adding one to make a build pass is how the boundary erodes. A file belongs on
	// a list only if it genuinely cannot do its job over plain values.
	mayImportVFS := map[string]bool{
		"store.go":    true,
		"comments.go": true,
		"config.go":   true, // global config loader (env/vfs seams)
		"registry.go": true, // central registry + Resolve/Stores/InitCentral
		"content.go":  true, // body-overflow sidecar I/O (rule itself is in overflow.go)

		// packageload.go reads a hook package's manifest; the format itself is
		// pure and lives in packages.go.
		"packageload.go": true,
	}
	mayImportEnv := map[string]bool{
		"store.go":       true, // carries the env seam its resolution used
		"config.go":      true, // the per-user home (CONFIG-SPEC)
		"registry.go":    true, // central registry resolution
		"packageload.go": true, // global package directory under the user home
	}
	mayImportExec := map[string]bool{
		"store.go":   true, // carries the runner hooks are spawned with
		"hookrun.go": true, // spawns the hook process
		"log.go":     true, // records a hook process failure
	}
	mayDeclareStoreMethods := map[string]bool{
		"store.go":    true,
		"comments.go": true,
		"config.go":   true,
		"registry.go": true,
		"content.go":  true,
		"list.go":     true, // Ready/Blocked/Detail/List/Find over the rules in ready.go
		"mutation.go": true, // the gated write path
		"import.go":   true, // bulk import
		"hookrun.go":  true, // hook execution against the exec seam
		"log.go":      true, // observability records emitted from the write path
	}

	const seamRoot = "github.com/hk9890/task-manager/sdk/tasks/internal/"
	seams := []struct {
		name   string
		pkg    string
		exempt map[string]bool
	}{
		{"vfs (disk)", seamRoot + "vfs", mayImportVFS},
		{"env (user environment)", seamRoot + "env", mayImportEnv},
		{"exec (hook processes)", seamRoot + "exec", mayImportExec},
	}

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
		path := filepath.Join(sdkTasksDir, name)
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("parse %s: %v", path, err)
			continue
		}
		for _, seam := range seams {
			if seam.exempt[name] {
				continue
			}
			for _, imp := range f.Imports {
				pkg := strings.Trim(imp.Path.Value, `"`)
				if pkg == seam.pkg || strings.HasPrefix(pkg, seam.pkg+"/") {
					t.Errorf("pure-core file %s must not import the %s seam (got %q)", name, seam.name, pkg)
				}
			}
		}
		if mayDeclareStoreMethods[name] {
			continue
		}

		// The receiver check needs the declarations, so re-parse in full.
		full, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Errorf("parse %s: %v", path, err)
			continue
		}
		for _, decl := range full.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			if receiverTypeName(fn.Recv.List[0].Type) == "Store" {
				t.Errorf("pure-core file %s declares %s on *Store: a method reaches the disk seam "+
					"through s.fs without importing it, and cannot be called without a store. "+
					"Move it to an imperative-shell file, or make it a function over values.",
					name, fn.Name.Name)
			}
		}
	}
}

// receiverTypeName returns the bare type name of a method receiver, unwrapping
// a pointer and any generic instantiation.
func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return ""
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
