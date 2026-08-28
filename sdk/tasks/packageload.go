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
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// ErrPackageMissing reports a `use:` entry whose package directory is not there,
// as opposed to one that is there but unusable. Callers match it with errors.Is
// to tell "install this" apart from "repair this".
var ErrPackageMissing = errors.New("package not installed")

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
				return nil, fmt.Errorf("package %q is not installed: nothing at %s: %w", name, dir, ErrPackageMissing)
			}
			return nil, fmt.Errorf("package %q at %s has no %s", name, dir, PackageManifestName)
		}
		return nil, fmt.Errorf("package %q: read %s: %w", name, PackageManifestName, err)
	}
	m, err := parsePackageManifest(data, dir)
	if err != nil {
		return nil, err
	}
	hooks, err := hooksFromManifest(m, name, dir)
	if err != nil {
		return nil, err
	}
	// Compile every hook here, so "loaded" means "will run". Deferring the event,
	// run and when checks to buildHookSet made `package list` and `hook list`
	// report a package that wedges every mutation as ok — the two commands a
	// reader reaches for precisely when writes have stopped.
	for _, ph := range hooks {
		if _, err := compileHook(ph.hook, ph.id); err != nil {
			return nil, err
		}
	}
	return hooks, nil
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
func collectUse(fs vfs.FS, refs []PackageRef, home, configDir, scope string, seen map[string]string) ([]packageHook, []PackageInfo, error) {
	var (
		hooks []packageHook
		infos []PackageInfo
		first error
	)
	for _, ref := range refs {
		dir, name, err := packageDir(ref, home, configDir)
		if err != nil {
			// Name the entry the way the file spells it, so the row points at a
			// line the reader can find — including an entry that is not a
			// mapping at all and therefore has neither name nor path.
			label := ref.Name
			if label == "" {
				label = ref.Path
			}
			if label == "" {
				label = ref.malformed
			}
			infos = append(infos, PackageInfo{Name: label, Path: ref.Path, Scope: scope, Status: PackageBroken, Detail: err.Error()})
			if first == nil {
				first = err
			}
			continue
		}
		info := PackageInfo{Name: name, Path: dir, Scope: scope}

		// Identity is the resolved directory, not the name. Keying on the name
		// let a per-user package silently disable a *different* store package
		// that merely shared a directory name — the exact inversion of §3.5
		// rule 5, which says a store cannot suppress an inherited package.
		if at, dup := seen[dir]; dup {
			info.Status = PackageOK
			info.Shadowed = true
			info.Detail = "the same directory is already used by an earlier entry"
			_ = at
			infos = append(infos, info)
			continue
		}

		phs, err := loadPackage(fs, dir, name)
		if err != nil {
			info.Status = PackageBroken
			if errors.Is(err, ErrPackageMissing) {
				info.Status = PackageMissing
			}
			info.Detail = err.Error()
			infos = append(infos, info)
			if first == nil {
				first = err
			}
			// Not marked seen: an entry that did not load provides nothing, so a
			// later entry for the same package must still get its chance rather
			// than being reported "already provided" by one that failed.
			continue
		}

		// Two different directories contributing the same package name would
		// mint the same effective ids, so a denial could not say which one
		// refused. That is a configuration error, not a silent winner.
		if other, clash := seen[name]; clash {
			err := fmt.Errorf("package name %q is claimed by two different directories, %s and %s: effective hook ids would collide", name, other, dir)
			info.Status = PackageBroken
			info.Detail = err.Error()
			infos = append(infos, info)
			if first == nil {
				first = err
			}
			continue
		}
		seen[dir], seen[name] = dir, dir

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
func packageChain(fs vfs.FS, e env.Environment, storeDir string, global GlobalConfig, cfg Config) ([]packageHook, []PackageInfo, error) {
	// A home that cannot be located is not an error in itself — there is then
	// nothing machine-wide to inherit. It only becomes one for an entry that
	// needs it, which packageDir reports per entry rather than by substituting a
	// directory that resolves somewhere unintended.
	home, _ := taskmgrHome(e)

	seen := make(map[string]string)
	var (
		hooks []packageHook
		infos []PackageInfo
	)

	gh, gi, gerr := collectUse(fs, global.Use, home, home, scopeGlobal, seen)
	hooks, infos = append(hooks, gh...), append(infos, gi...)

	// Both lists are always walked. Returning at the first failing per-user entry
	// dropped every store row from the listing — in exactly the state where a
	// reader needs them, since that listing is the documented way out.
	sh, si, serr := collectUse(fs, cfg.Use, home, storeDir, scopeStore, seen)
	hooks, infos = append(hooks, sh...), append(infos, si...)

	if gerr != nil {
		return hooks, infos, gerr
	}
	return hooks, infos, serr
}

// hookInfos turns a resolved chain into the reportable form, tagging each hook
// with the config file whose `use:` list brought its package in.
func hookInfos(hooks []packageHook, infos []PackageInfo) []HookInfo {
	scopeOf := make(map[string]string, len(infos))
	for _, in := range infos {
		if !in.Shadowed && in.Status == PackageOK {
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
	return out
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
	_, infos, _ := collectUse(fs, cfg.Use, home, home, scopeGlobal, make(map[string]string))
	return infos, nil
}

// inspectRef resolves one `use:` entry and reports what it is, without writing
// anything. It is what lets a command check a package *before* it adds the entry
// that depends on it.
func inspectRef(fs vfs.FS, ref PackageRef, home, configDir, scope string) PackageInfo {
	_, infos, _ := collectUse(fs, []PackageRef{ref}, home, configDir, scope, make(map[string]string))
	if len(infos) == 1 {
		return infos[0]
	}
	return PackageInfo{Name: ref.Name, Path: ref.Path, Scope: scope, Status: PackageBroken}
}
