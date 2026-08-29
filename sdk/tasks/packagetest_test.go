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
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// Package fixtures for hook tests. A hook is only ever configured through a
// package now (HOOK-SPEC §3.6), so a test that wants a gate writes one the way a
// user does — a directory with a manifest — rather than assigning a struct field
// the format no longer has.

// writePackage writes a package directory holding hooks and returns its path.
// An entry with no id gets one, so a test that does not care about ids stays
// short; the effective id is then "pkg:<name>:h<index>".
func writePackage(t *testing.T, fs vfs.FS, dir, name string, hooks []Hook) string {
	t.Helper()
	pkgDir := filepath.Join(dir, packagesSubdir, name)
	if err := fs.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir package %s: %v", pkgDir, err)
	}
	m := packageManifest{Version: 1, Hooks: make([]Hook, 0, len(hooks))}
	for i, h := range hooks {
		if h.ID == "" {
			h.ID = "h" + itoa(i)
		}
		m.Hooks = append(m.Hooks, h)
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := fs.WriteAtomic(filepath.Join(pkgDir, PackageManifestName), data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	// A hook that names a script inside the package gets one. loadPackage checks
	// that such a program is there, so a fixture declaring a script it never
	// wrote describes a package that does not load — which is not what a test
	// about hook ordering or argv means to say.
	for _, h := range m.Hooks {
		if len(h.Run) == 0 || isAbsAnyPlatform(h.Run[0]) || !strings.ContainsRune(h.Run[0], '/') {
			continue
		}
		script := filepath.Join(pkgDir, filepath.FromSlash(h.Run[0]))
		if err := fs.MkdirAll(filepath.Dir(script), 0o755); err != nil {
			t.Fatalf("mkdir hook dir: %v", err)
		}
		if err := fs.WriteAtomic(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write hook script %s: %v", script, err)
		}
	}
	return pkgDir
}

// storePackage writes a package inside the store's own directory and points the
// store's `use:` list at it — the shape a repository ships, where the package
// travels with the store.
func storePackage(t *testing.T, s *Store, name string, hooks []Hook) {
	t.Helper()
	writePackage(t, s.fs, s.dir, name, hooks)
	s.cfg.Use = append(s.cfg.Use, PackageRef{Path: filepath.Join(packagesSubdir, name)})
	s.hookBuilt = false
	s.hookSet, s.hookErr = nil, nil
}

// useRef adds a `use:` entry to the store config without writing any package, so
// a test can exercise an entry that does not resolve.
func useRef(s *Store, ref PackageRef) {
	s.cfg.Use = append(s.cfg.Use, ref)
	s.hookBuilt = false
	s.hookSet, s.hookErr = nil, nil
}
