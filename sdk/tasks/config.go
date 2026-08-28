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
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// This file is the imperative shell for the per-user configuration (CONFIG-SPEC
// §1–§2): locating the taskmgr home and loading the global config through the
// env and vfs seams. The read path never writes — a missing config yields
// built-in defaults.

const (
	// homeDirName is the default per-user home under the OS user home dir.
	homeDirName = ".taskmgr"
	// globalConfigName is the per-user config file inside the home.
	globalConfigName = "config.yaml"
	// registryFileName is the central registry inside the central root.
	registryFileName = "mapping.yaml"
	// storesSubdir holds the central stores under the central root.
	storesSubdir = "stores"
	// centralLockName is the advisory lock for registry writes (CONFIG-SPEC §3).
	centralLockName = ".lock"
	// globalConfigLockName is the advisory lock for per-user config writes. It is
	// a second lock file rather than centralLockName because the two guard
	// different files: the home and the central root are the same directory only
	// by default (CONFIG-SPEC §2/§3).
	globalConfigLockName = ".config.lock"

	// envTaskmgrHome overrides the per-user home (CONFIG-SPEC §1).
	envTaskmgrHome = "TASKMGR_HOME"
	// envTaskmgrDir is the withdrawn store-directory override. It is read only
	// so resolution can refuse when it is set (CONFIG-SPEC §4) — a pin that is
	// silently ignored files work into the wrong store.
	envTaskmgrDir = "TASKMGR_DIR"
)

// GlobalConfig is the per-user configuration (CONFIG-SPEC §2). Every field is
// optional; the zero value plus defaults is valid. Unknown keys are ignored.
//
// HookTimeout and Use configure lifecycle gates for every store on this
// machine (HOOK-SPEC §3.5). Like a store's own packages they are read lazily on
// the first write, so an unusable package never breaks a read — but it then
// fails mutations in every store on the machine, not just one.
type GlobalConfig struct {
	Version     int    `yaml:"version"`
	CentralRoot string `yaml:"central_root,omitempty"`

	// HookTimeout is the fallback per-hook wall-clock limit for a store that
	// does not set its own (HOOK-SPEC §3.1); the store's value wins when both
	// are set.
	HookTimeout string `yaml:"hook_timeout,omitempty"`

	// Use lists the hook packages that apply to every store on this machine
	// (HOOK-SPEC §3.5, §3.6). Their hooks run before the store's own packages,
	// in list order. This file is machine-local — it does not travel with a
	// repository — so an invariant the data depends on belongs in the store's
	// config, not here.
	Use []PackageRef `yaml:"use,omitempty"`
}

// taskmgrHome returns the per-user home (CONFIG-SPEC §1): $TASKMGR_HOME if set,
// else <user-home>/.taskmgr.
func taskmgrHome(e env.Environment) (string, error) {
	if h := e.Getenv(envTaskmgrHome); h != "" {
		return filepath.Clean(h), nil
	}
	home, err := e.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate taskmgr home: %w", err)
	}
	return filepath.Join(home, homeDirName), nil
}

// loadGlobalConfig reads <home>/config.yaml, returning built-in defaults when it
// is absent (CONFIG-SPEC §1/§2). A corrupt file is an error.
func loadGlobalConfig(fs vfs.FS, home string) (GlobalConfig, error) {
	cfg := GlobalConfig{Version: 1}
	data, err := fs.ReadFile(filepath.Join(home, globalConfigName))
	if err != nil {
		if vfs.IsNotExist(err) {
			return cfg, nil // defaults
		}
		return GlobalConfig{}, fmt.Errorf("read global config: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return GlobalConfig{}, fmt.Errorf("parse global config: %w", err)
	}
	return cfg, nil
}

// saveGlobalConfig writes cfg to <home>/config.yaml atomically. The caller must
// already hold the home's config lock (updateGlobalConfig is the only path in).
func saveGlobalConfig(fs vfs.FS, home string, cfg GlobalConfig) error {
	path := filepath.Join(home, globalConfigName)
	old, err := fs.ReadFile(path)
	if err != nil && !vfs.IsNotExist(err) {
		return fmt.Errorf("read global config: %w", err)
	}
	// Edit the document rather than regenerate it, so unknown keys and comments
	// survive a write (configdoc.go).
	data, err := applyGlobalConfigToDoc(old, cfg)
	if err != nil {
		return err
	}
	return fs.WriteAtomic(path, data, 0o644)
}

// updateGlobalConfig is UpdateGlobalConfig with injectable seams, for hermetic
// tests. Creating the home is expected here: unlike the read path (CONFIG-SPEC
// §1) this is a write command.
func updateGlobalConfig(fs vfs.FS, e env.Environment, mutate func(*GlobalConfig) error) error {
	home, err := taskmgrHome(e)
	if err != nil {
		return err
	}
	if err := fs.MkdirAll(home, 0o755); err != nil {
		return err
	}
	unlock, err := fs.Lock(filepath.Join(home, globalConfigLockName))
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()

	cur, err := loadGlobalConfig(fs, home)
	if err != nil {
		return err
	}
	next := cur
	next.Use = clonePackageRefs(cur.Use)
	if err := mutate(&next); err != nil {
		return err
	}
	if strings.TrimSpace(next.HookTimeout) != strings.TrimSpace(cur.HookTimeout) {
		if _, err := parseHookTimeout(next.HookTimeout); err != nil {
			return err
		}
	}
	if err := checkUseChange(cur.Use, next.Use); err != nil {
		return fmt.Errorf("per-user config: %w", err)
	}
	return saveGlobalConfig(fs, home, next)
}

// LoadGlobalConfig reads the per-user configuration (CONFIG-SPEC §2), returning
// built-in defaults when the home or the file is absent. A corrupt file is an
// error.
func LoadGlobalConfig() (GlobalConfig, error) {
	home, err := taskmgrHome(env.NewOS())
	if err != nil {
		return GlobalConfig{}, err
	}
	return loadGlobalConfig(vfs.NewOS(), home)
}

// UpdateGlobalConfig applies mutate to the per-user configuration and writes the
// result (CONFIG-SPEC §2), creating the taskmgr home if needed. The read, the
// mutation and the write all happen inside one hold of the home's config lock,
// so two processes editing different keys cannot discard each other's work.
//
// mutate receives the configuration as it is on disk right now, not a snapshot
// read earlier. Returning an error from it abandons the write.
//
// A `use:` entry the write introduces is checked before anything is written: a
// malformed reference here blocks mutations in *every* store on the machine
// (HOOK-SPEC §3.4/§3.5), so it is refused at the command that adds it rather
// than left for the next write in some unrelated project to discover. An entry
// already on disk is not re-checked — that is what leaves the write that removes
// a bad one able to succeed.
func UpdateGlobalConfig(mutate func(*GlobalConfig) error) error {
	return updateGlobalConfig(vfs.NewOS(), env.NewOS(), mutate)
}

// SaveGlobalConfig writes cfg as the per-user configuration (CONFIG-SPEC §2),
// creating the taskmgr home if needed.
//
// It is last-writer-wins by construction: cfg was built from a read that happened
// outside the lock, so a concurrent edit made between that read and this call is
// overwritten. Use UpdateGlobalConfig to change one key without discarding the
// rest.
func SaveGlobalConfig(cfg GlobalConfig) error {
	return UpdateGlobalConfig(func(g *GlobalConfig) error {
		*g = cfg
		return nil
	})
}

// InspectGlobalPackage reports what one `use:` entry would resolve to in the
// per-user config, without writing it (CONFIG-SPEC §2). It needs no store.
func InspectGlobalPackage(ref PackageRef) (PackageInfo, error) {
	home, err := taskmgrHome(env.NewOS())
	if err != nil {
		return PackageInfo{}, err
	}
	return inspectRef(vfs.NewOS(), ref, home, home, scopeGlobal), nil
}

// GlobalConfigPath returns the absolute path of the per-user config file
// (CONFIG-SPEC §1/§2), whether or not it exists. Callers report it in errors and
// in `taskmgr config list --global`.
func GlobalConfigPath() (string, error) {
	home, err := taskmgrHome(env.NewOS())
	if err != nil {
		return "", err
	}
	return filepath.Join(home, globalConfigName), nil
}

// centralRoot resolves the central store root (CONFIG-SPEC §2/§3): cfg.CentralRoot
// with a leading ~ expanded and a relative value resolved against home,
// defaulting to home when unset.
func centralRoot(cfg GlobalConfig, home string) string {
	if cfg.CentralRoot == "" {
		return home
	}
	return lexCanon(cfg.CentralRoot, home, home)
}
