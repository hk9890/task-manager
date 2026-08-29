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
	"unicode/utf8"

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
	// Guide is the number of guide fragments the package contributes; 0 unless
	// ok (HOOK-SPEC §3.7).
	Guide int
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

// loadedPackage is what one package directory contributes: the hooks that gate a
// write, and the guide fragments that describe them (HOOK-SPEC §3.6, §3.7).
type loadedPackage struct {
	hooks []packageHook
	guide []packageGuide
}

// loadPackage reads one package directory and returns what it contributes.
// A directory that is not there, or that holds no usable manifest, is an error:
// a `use:` entry names a package the configuration depends on, so a mutation
// fails closed rather than running with a gate silently absent (HOOK-SPEC §1
// principle 4).
//
// Guide entries are validated here and their files are *not* read: a fragment is
// documentation, and nothing a mutation depends on. Reading one on the write
// path would be I/O no write needs, and would let an unreadable document stop a
// write that its package's hooks were willing to allow.
func loadPackage(fs vfs.FS, dir, name string) (loadedPackage, error) {
	data, err := fs.ReadFile(filepath.Join(dir, PackageManifestName))
	if err != nil {
		if vfs.IsNotExist(err) {
			if _, serr := fs.Stat(dir); serr != nil && vfs.IsNotExist(serr) {
				return loadedPackage{}, fmt.Errorf("package %q is not installed: nothing at %s: %w", name, dir, ErrPackageMissing)
			}
			return loadedPackage{}, fmt.Errorf("package %q at %s has no %s", name, dir, PackageManifestName)
		}
		return loadedPackage{}, fmt.Errorf("package %q: read %s: %w", name, PackageManifestName, err)
	}
	m, err := parsePackageManifest(data, dir)
	if err != nil {
		return loadedPackage{}, err
	}
	hooks, err := hooksFromManifest(m, name, dir)
	if err != nil {
		return loadedPackage{}, err
	}
	// Compile every hook here, so "loaded" means "will run". Deferring the event,
	// run and when checks to buildHookSet made `package list` and `hook list`
	// report a package that wedges every mutation as ok — the two commands a
	// reader reaches for precisely when writes have stopped.
	for _, ph := range hooks {
		if _, err := compileHook(ph.hook, ph.id); err != nil {
			return loadedPackage{}, err
		}
		if err := checkHookProgram(fs, dir, ph); err != nil {
			return loadedPackage{}, err
		}
	}
	guide, err := guideFromManifest(m, name, dir)
	if err != nil {
		return loadedPackage{}, err
	}
	return loadedPackage{hooks: hooks, guide: guide}, nil
}

// checkHookProgram reports a hook whose program the package was supposed to ship
// but did not, so "loaded" reaches as far as "the file is there to run".
//
// Only a program *inside* the package directory is checked. resolveHookArgv has
// already turned a relative argv[0] carrying a separator into a concrete path in
// there, which is a file this loader can stat; a bare `sh` is a PATH lookup and
// an absolute path names something the machine owns, and neither is the
// package's to promise — stat-ing them would let a machine's PATH decide whether
// a package loads.
//
// It stops at existence. The executable bit is deliberately not checked: the
// disk seam does not carry permission bits it can state on every platform (the
// in-memory FS has none, a Windows checkout has none to keep), so a package that
// is present but not executable still fails at the transition, with the exec
// seam's own message.
func checkHookProgram(fs vfs.FS, dir string, ph packageHook) error {
	if len(ph.hook.Run) == 0 {
		return nil // compileHook has already refused an empty run
	}
	prog := ph.hook.Run[0]
	if !strings.HasPrefix(prog, dir+string(filepath.Separator)) {
		return nil
	}
	fi, err := fs.Stat(prog)
	if err != nil {
		if vfs.IsNotExist(err) {
			return fmt.Errorf("hook %s: %s is not there; a relative run path names a program the package ships", ph.id, prog)
		}
		return fmt.Errorf("hook %s: %s: %w", ph.id, prog, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("hook %s: %s is a directory, not a program", ph.id, prog)
	}
	return nil
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
func collectUse(fs vfs.FS, refs []PackageRef, home, configDir, scope string, seen map[string]string) (loadedPackage, []PackageInfo, error) {
	var (
		loaded loadedPackage
		infos  []PackageInfo
		first  error
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

		// Identity is both the resolved directory and the package name, and an
		// entry that repeats either is shadowed — reported, contributing nothing
		// (§3.5 rule 3). The name has to count: effective ids are
		// `pkg:<name>:<hook>`, so two directories under one name would mint the
		// same ids and a denial could not say which package refused.
		//
		// Erroring on the repeated name instead is what rule 3 rules out in so
		// many words — "one person's machine-wide install [would] break the
		// repository for every colleague who has it" — and the shipped
		// task-writing package reaches that state by following its own README,
		// which offers a machine-wide install and a vendored copy of the same
		// package. Shadowing is also not the suppression rule 5 forbids: the
		// entry that wins is the one the *earlier* list named, so a store still
		// cannot displace a package it inherits.
		if at, dup := seen[dir]; dup {
			info.Status = PackageOK
			info.Shadowed = true
			info.Detail = fmt.Sprintf("an earlier entry already uses this directory (%s)", at)
			infos = append(infos, info)
			continue
		}
		if at, dup := seen[name]; dup {
			info.Status = PackageOK
			info.Shadowed = true
			info.Detail = fmt.Sprintf("an earlier entry already provides the package name %q, from %s", name, at)
			infos = append(infos, info)
			continue
		}

		lp, err := loadPackage(fs, dir, name)
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

		seen[dir], seen[name] = dir, dir

		info.Status = PackageOK
		info.Hooks = len(lp.hooks)
		info.Guide = len(lp.guide)
		infos = append(infos, info)
		loaded.hooks = append(loaded.hooks, lp.hooks...)
		loaded.guide = append(loaded.guide, lp.guide...)
	}
	return loaded, infos, first
}

// packageChain resolves both `use:` lists into the effective hook chain: the
// per-user config's packages first, then the store's, each in list order and
// each package's hooks in manifest order (HOOK-SPEC §3.5).
//
// A home that cannot be located is not an error — there is then simply nothing
// machine-wide to inherit, exactly as before packages existed.
func packageChain(fs vfs.FS, e env.Environment, storeDir string, global GlobalConfig, cfg Config) (loadedPackage, []PackageInfo, error) {
	// A home that cannot be located is not an error in itself — there is then
	// nothing machine-wide to inherit. It only becomes one for an entry that
	// needs it, which packageDir reports per entry rather than by substituting a
	// directory that resolves somewhere unintended.
	home, _ := taskmgrHome(e)

	seen := make(map[string]string)
	var (
		loaded loadedPackage
		infos  []PackageInfo
	)

	// A file's own package defects come first, ahead of its entries: they are
	// faults of the file rather than of any one package, and they fail every
	// mutation on the same terms (§3.4).
	gdi, gderr := defectInfos(global.defects, filepath.Join(home, globalConfigName), scopeGlobal)
	infos = append(infos, gdi...)

	gl, gi, gerr := collectUse(fs, global.Use, home, home, scopeGlobal, seen)
	loaded.hooks, loaded.guide = append(loaded.hooks, gl.hooks...), append(loaded.guide, gl.guide...)
	infos = append(infos, gi...)

	sdi, sderr := defectInfos(cfg.defects, filepath.Join(storeDir, ConfigFileName), scopeStore)
	infos = append(infos, sdi...)

	// Both lists are always walked. Returning at the first failing per-user entry
	// dropped every store row from the listing — in exactly the state where a
	// reader needs them, since that listing is the documented way out.
	sl, si, serr := collectUse(fs, cfg.Use, home, storeDir, scopeStore, seen)
	loaded.hooks, loaded.guide = append(loaded.hooks, sl.hooks...), append(loaded.guide, sl.guide...)
	infos = append(infos, si...)

	for _, err := range []error{gderr, gerr, sderr, serr} {
		if err != nil {
			return loaded, infos, err
		}
	}
	return loaded, infos, nil
}

// defectInfos turns one config file's package defects into listing rows and the
// error that fails every mutation until they are fixed.
//
// A row names the *key* rather than a package, because that is what is wrong and
// what the reader has to edit. Without one, a file carrying the withdrawn
// `hooks:` block, or a `use:` value that is not a list, would stop every write
// while `taskmgr package list` showed nothing amiss — the listing being the
// documented way out of exactly that state.
func defectInfos(defects []configDefect, path, scope string) ([]PackageInfo, error) {
	if len(defects) == 0 {
		return nil, nil
	}
	out := make([]PackageInfo, 0, len(defects))
	var first error
	for _, d := range defects {
		out = append(out, PackageInfo{
			Name:   d.key + ":",
			Path:   path,
			Scope:  scope,
			Status: PackageBroken,
			Detail: d.detail,
		})
		if first == nil {
			first = fmt.Errorf("%s: %s", path, d.detail)
		}
	}
	return out, first
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

// inspectRef resolves one `use:` entry against the entries that already apply,
// and reports what it is, without writing anything. It is what lets a command
// check a package *before* it adds the entry that depends on it.
//
// seen carries those entries — the same map collectUse threads through a chain,
// pre-walked by the caller. Passing a fresh one instead put every cross-entry
// rule out of reach, since all of them live in that map: a new entry could not
// be seen to repeat a directory or a package name, so `taskmgr package add`
// accepted a clash that surfaced at the next unrelated mutation, and the
// Shadowed field it reports could never be set.
func inspectRef(fs vfs.FS, ref PackageRef, home, configDir, scope string, seen map[string]string) PackageInfo {
	_, infos, _ := collectUse(fs, []PackageRef{ref}, home, configDir, scope, seen)
	if len(infos) == 1 {
		return infos[0]
	}
	return PackageInfo{Name: ref.Name, Path: ref.Path, Scope: scope, Status: PackageBroken}
}

// priorSeen replays the `use:` lists that already apply, so inspectRef can judge
// a new entry against them. It resolves the same lists packageChain does, in the
// same order, and keeps only the map: the chain is built to run hooks, and this
// caller writes nothing and runs nothing.
//
// storeDir empty means the per-user scope, where only that file's own list runs
// earlier — a new entry there cannot be shadowed by a store, because the
// per-user list runs first (§3.5 rule 1).
func priorSeen(fs vfs.FS, home, storeDir string, global GlobalConfig, cfg Config) map[string]string {
	seen := make(map[string]string)
	_, _, _ = collectUse(fs, global.Use, home, home, scopeGlobal, seen)
	if storeDir != "" {
		_, _, _ = collectUse(fs, cfg.Use, home, storeDir, scopeStore, seen)
	}
	return seen
}

// GuideTopic is one guide fragment a package contributes, with its text already
// read (HOOK-SPEC §3.7). It is what `taskmgr guide` appends to its own sections.
//
// A topic is reported whether or not its file could be read: Detail explains a
// fragment that is missing or unreadable, and Text is then empty. Nothing about
// a guide is allowed to fail the caller — a guide is not a gate, and a document
// that cannot be read is a smaller problem than a command that will not run.
type GuideTopic struct {
	// ID is the effective topic id, "pkg:<package>:<id>" — what a caller names
	// to select this fragment alone.
	ID string
	// Overview marks the fragment the package's `overview:` key declared: it
	// belongs in the guide's overview rather than waiting to be asked for, and is
	// held to the tighter MaxGuideOverviewBytes.
	Overview bool
	// Package is the package that contributes the fragment.
	Package string
	// Scope is "global" or "store": which config file's `use:` list brought the
	// package in.
	Scope string
	// Path is the fragment file on this machine.
	Path string
	// Text is the fragment, empty when Detail is set.
	Text string
	// Detail explains a fragment that could not be read; empty when Text is good.
	Detail string
	// Truncated reports a fragment cut down to MaxGuideFragmentBytes. The text is
	// whole lines, so a caller can print it as it stands; saying that it was cut
	// is the caller's to render.
	Truncated bool
}

// readGuideFragment reads one fragment through the disk seam and caps it.
//
// The cut falls at the last line break under the cap so the text stays whole
// lines: a fragment is Markdown, and a document severed mid-sentence reads as
// though the author meant it.
//
// A window holding no line break at all has no such seam to cut on, and that is
// the ordinary shape under the 1 KiB overview cap — one paragraph of prose. The
// cut then falls back to the last whole rune, because the byte offset lands
// mid-sequence for any multi-byte character (an em dash is three bytes), and the
// text goes verbatim into a caller's context or into a JSON field, where a
// broken rune is not a cosmetic problem.
func readGuideFragment(fs vfs.FS, path string, limit int) (text string, truncated bool, err error) {
	data, err := fs.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	if len(data) <= limit {
		return string(data), false, nil
	}
	cut := string(data[:limit])
	if i := strings.LastIndexByte(cut, '\n'); i >= 0 {
		return cut[:i+1], true, nil
	}
	return trimPartialRune(cut), true, nil
}

// trimPartialRune drops a truncated UTF-8 sequence from the end of s. It removes
// at most three bytes: a rune is at most four, so anything longer is invalid
// input rather than a cut. A real U+FFFD in the text is left alone —
// DecodeLastRuneInString reports size 1 only for a byte that decodes to nothing.
func trimPartialRune(s string) string {
	for s != "" {
		r, size := utf8.DecodeLastRuneInString(s)
		if r != utf8.RuneError || size != 1 {
			return s
		}
		s = s[:len(s)-1]
	}
	return s
}

// guideTopics reads every fragment of a resolved chain, tagging each with the
// config file whose `use:` list brought its package in.
func guideTopics(fs vfs.FS, guide []packageGuide, infos []PackageInfo) []GuideTopic {
	scopeOf := make(map[string]string, len(infos))
	for _, in := range infos {
		if !in.Shadowed && in.Status == PackageOK {
			scopeOf[in.Name] = in.Scope
		}
	}
	out := make([]GuideTopic, 0, len(guide))
	for _, pg := range guide {
		pkg := packageOfID(pg.id)
		t := GuideTopic{ID: pg.id, Overview: pg.overview, Package: pkg, Scope: scopeOf[pkg], Path: pg.path}
		limit := MaxGuideFragmentBytes
		if pg.overview {
			limit = MaxGuideOverviewBytes
		}
		text, truncated, err := readGuideFragment(fs, pg.path, limit)
		if err != nil {
			t.Detail = err.Error()
		} else {
			t.Text, t.Truncated = text, truncated
		}
		out = append(out, t)
	}
	return out
}

// GlobalGuideTopics reports the guide fragments contributed by the per-user
// config's packages (CONFIG-SPEC §2). It needs no store, so it answers in a
// directory where nothing resolves — which is the case `taskmgr guide` has to
// serve, since the guide is the one command an agent runs before it knows
// whether it is standing in a project at all.
func GlobalGuideTopics() ([]GuideTopic, error) {
	return globalGuideTopics(vfs.NewOS(), env.NewOS())
}

func globalGuideTopics(fs vfs.FS, e env.Environment) ([]GuideTopic, error) {
	home, err := taskmgrHome(e)
	if err != nil {
		return nil, err
	}
	cfg, err := loadGlobalConfig(fs, home)
	if err != nil {
		return nil, err
	}
	loaded, infos, _ := collectUse(fs, cfg.Use, home, home, scopeGlobal, make(map[string]string))
	return guideTopics(fs, loaded.guide, infos), nil
}
