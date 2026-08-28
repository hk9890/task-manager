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

// packageload.go — the imperative shell for hook packages: reading a package
// directory through the disk seam, and merging the two `use:` lists into the one
// chain a mutation runs (HOOK-SPEC §3.5).
//
// The format itself is pure and lives in packages.go. This file only reads.
package tasks

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// Package status values, as reported by `taskmgr package list` (CLI-SPEC §2.3).
// They mirror the registry's vocabulary (CONFIG-SPEC §3) so a listing and a
// mutation never disagree about an entry.
const (
	// PackageOK — the directory is there and holds a manifest that parses.
	PackageOK = "ok"
	// PackageMissing — nothing at the resolved path.
	PackageMissing = "missing"
	// PackageBroken — the directory is there but is not a finished package:
	// no manifest, or one that does not load.
	PackageBroken = "broken"
)

// PackageInfo is one entry of a config file's `use:` list together with what it
// resolves to on this machine. It is what `taskmgr package list` prints, so a
// caller can see the entries that will not load before a mutation fails on them.
type PackageInfo struct {
	// Name is the package name: the `name:` given, or the base name of `path:`.
	Name string
	// Path is the directory the entry resolves to on this machine.
	Path string
	// Scope is "global" or "store" — which config file declared the entry.
	Scope string
	// Status is PackageOK, PackageMissing or PackageBroken.
	Status string
	// Detail explains a status that is not ok.
	Detail string
	// Hooks is the number of hooks the package contributes; 0 unless ok.
	Hooks int
	// Shadowed reports an entry whose name was already taken by an earlier one,
	// so it contributes nothing (HOOK-SPEC §3.5).
	Shadowed bool
}

// HookInfo is one hook of the effective chain, in the order it runs. It is what
// `taskmgr hook list` prints — the authoritative answer to what gates a store,
// which neither config file can give on its own.
type HookInfo struct {
	// ID is the effective id, "pkg:<package>:<hook>" — the id a denial reason
	// reports.
	ID      string
	Event   string
	When    string
	Run     []string
	Package string
	// Scope is "global" or "store": which config file's `use:` list brought the
	// package in, and therefore which file to edit.
	Scope string
}

// Config-file scopes, used in PackageInfo and HookInfo.
const (
	scopeGlobal = "global"
	scopeStore  = "store"
)

// loadPackage reads one package directory and returns the hooks it contributes.
// A directory that is not there, or that holds no usable manifest, is an error:
// a `use:` entry names a package the configuration depends on, so a mutation
// fails closed rather than running with a gate silently absent (HOOK-SPEC §1
// principle 4).
func loadPackage(fs vfs.FS, dir, name string) ([]packageHook, error) {
	data, err := fs.ReadFile(filepath.Join(dir, PackageManifestName))
	if err != nil {
		if vfs.IsNotExist(err) {
			if _, serr := fs.Stat(dir); serr != nil && vfs.IsNotExist(serr) {
				return nil, fmt.Errorf("package %q is not installed: nothing at %s", name, dir)
			}
			return nil, fmt.Errorf("package %q at %s has no %s", name, dir, PackageManifestName)
		}
		return nil, fmt.Errorf("package %q: read %s: %w", name, PackageManifestName, err)
	}
	m, err := parsePackageManifest(data, dir)
	if err != nil {
		return nil, err
	}
	return hooksFromManifest(m, name, dir)
}

// collectUse resolves one config file's `use:` list into the hooks it
// contributes, in list order, and reports what each entry resolved to.
//
// seen carries the package names already taken by an earlier list, so the same
// package named in both files contributes once, from the first file that named
// it (HOOK-SPEC §3.5). Erroring on the duplicate instead would let one person's
// machine-wide package break every colleague's repository.
//
// The []PackageInfo is complete whether or not an error is returned, so
// `package list` can report every entry while a mutation stops at the first one
// that will not load.
func collectUse(fs vfs.FS, refs []PackageRef, home, configDir, scope string, seen map[string]bool) ([]packageHook, []PackageInfo, error) {
	var (
		hooks []packageHook
		infos []PackageInfo
		first error
	)
	for _, ref := range refs {
		dir, name, err := packageDir(ref, home, configDir)
		if err != nil {
			infos = append(infos, PackageInfo{Name: ref.Name, Path: ref.Path, Scope: scope, Status: PackageBroken, Detail: err.Error()})
			if first == nil {
				first = err
			}
			continue
		}
		info := PackageInfo{Name: name, Path: dir, Scope: scope}
		if seen[name] {
			info.Status = PackageOK
			info.Shadowed = true
			info.Detail = "already provided by an earlier use entry"
			infos = append(infos, info)
			continue
		}
		seen[name] = true

		phs, err := loadPackage(fs, dir, name)
		if err != nil {
			info.Status = PackageBroken
			if _, serr := fs.Stat(dir); serr != nil && vfs.IsNotExist(serr) {
				info.Status = PackageMissing
			}
			info.Detail = err.Error()
			infos = append(infos, info)
			if first == nil {
				first = err
			}
			continue
		}
		info.Status = PackageOK
		info.Hooks = len(phs)
		infos = append(infos, info)
		hooks = append(hooks, phs...)
	}
	return hooks, infos, first
}

// packageChain resolves both `use:` lists into the effective hook chain: the
// per-user config's packages first, then the store's, each in list order and
// each package's hooks in manifest order (HOOK-SPEC §3.5).
//
// A home that cannot be located is not an error — there is then simply nothing
// machine-wide to inherit, exactly as before packages existed.
func (s *Store) packageChain(global GlobalConfig, cfg Config) ([]packageHook, []PackageInfo, error) {
	home, err := taskmgrHome(s.env)
	if err != nil {
		home = ""
	}
	seen := make(map[string]bool)

	var (
		hooks []packageHook
		infos []PackageInfo
	)
	if home != "" {
		gh, gi, gerr := collectUse(s.fs, global.Use, home, home, scopeGlobal, seen)
		hooks, infos = append(hooks, gh...), append(infos, gi...)
		if gerr != nil {
			return hooks, infos, gerr
		}
	}
	sh, si, serr := collectUse(s.fs, cfg.Use, home, s.dir, scopeStore, seen)
	hooks, infos = append(hooks, sh...), append(infos, si...)
	return hooks, infos, serr
}

// Packages reports every `use:` entry that applies to this store — the per-user
// config's first, then the store's — with what each one resolves to on this
// machine (CONFIG-SPEC §2, TASK-STORAGE-SPEC §4.2).
//
// It never fails on an entry that will not load: an unusable package is reported
// as its status, because the whole point of the listing is to show the entries a
// mutation would refuse.
func (s *Store) Packages() ([]PackageInfo, error) {
	global, err := s.globalConfig()
	if err != nil {
		return nil, err
	}
	_, infos, _ := s.packageChain(global, s.Config())
	return infos, nil
}

// HookChain returns the effective hook chain for this store, in the order the
// hooks run (HOOK-SPEC §3.5). It is the reading of the two config files plus the
// manifests they name, which is what makes the order inspectable rather than
// merely specified.
func (s *Store) HookChain() ([]HookInfo, error) {
	global, err := s.globalConfig()
	if err != nil {
		return nil, err
	}
	cfg := s.Config()
	hooks, infos, err := s.packageChain(global, cfg)
	if err != nil {
		return nil, err
	}
	scopeOf := make(map[string]string, len(infos))
	for _, in := range infos {
		if !in.Shadowed {
			scopeOf[in.Name] = in.Scope
		}
	}
	out := make([]HookInfo, 0, len(hooks))
	for _, ph := range hooks {
		pkg := packageOfID(ph.id)
		out = append(out, HookInfo{
			ID:      ph.id,
			Event:   ph.hook.Event,
			When:    ph.hook.When,
			Run:     append([]string(nil), ph.hook.Run...),
			Package: pkg,
			Scope:   scopeOf[pkg],
		})
	}
	return out, nil
}

// packageOfID pulls the package name out of an effective id "pkg:<package>:<hook>".
func packageOfID(id string) string {
	parts := strings.SplitN(id, packageIDSep, 3)
	if len(parts) != 3 {
		return ""
	}
	return parts[1]
}

// GlobalPackages reports the `use:` entries of the per-user config with what each
// resolves to (CONFIG-SPEC §2). It needs no store, so it answers in a directory
// where nothing resolves.
func GlobalPackages() ([]PackageInfo, error) {
	return globalPackages(vfs.NewOS(), env.NewOS())
}

func globalPackages(fs vfs.FS, e env.Environment) ([]PackageInfo, error) {
	home, err := taskmgrHome(e)
	if err != nil {
		return nil, err
	}
	cfg, err := loadGlobalConfig(fs, home)
	if err != nil {
		return nil, err
	}
	_, infos, _ := collectUse(fs, cfg.Use, home, home, scopeGlobal, make(map[string]bool))
	return infos, nil
}
