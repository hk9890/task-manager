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
// HookTimeout and Hooks configure lifecycle gates for every store on this
// machine (HOOK-SPEC §3.5). Like a store's own hooks they are validated lazily
// on the first write, so a malformed block never breaks a read — but it then
// fails mutations in every store on the machine, not just one.
type GlobalConfig struct {
	Version     int    `yaml:"version"`
	CentralRoot string `yaml:"central_root,omitempty"`

	// HookTimeout is the fallback per-hook wall-clock limit for a store that
	// does not set its own (HOOK-SPEC §3.1); the store's value wins when both
	// are set.
	HookTimeout string `yaml:"hook_timeout,omitempty"`

	// Hooks run before the store's own hooks, in config order (HOOK-SPEC §3.5).
	// They are machine-local — they do not travel with a repository — so an
	// invariant the data depends on belongs in the store's config, not here.
	Hooks []Hook `yaml:"hooks,omitempty"`
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

// saveGlobalConfig writes cfg to <home>/config.yaml atomically, creating the
// home if it is absent. Unlike the read path (CONFIG-SPEC §1) this is a write
// command, so creating the home is expected rather than a side effect.
//
// It takes no lock. The central-root lock guards mapping.yaml, which may live
// under a different directory than the home (CONFIG-SPEC §2/§3), and the file is
// hand-edited anyway; the atomic replace is what keeps a reader from ever seeing
// a partial document.
func saveGlobalConfig(fs vfs.FS, home string, cfg GlobalConfig) error {
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := fs.MkdirAll(home, 0o755); err != nil {
		return err
	}
	return fs.WriteAtomic(filepath.Join(home, globalConfigName), data, 0o644)
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

// SaveGlobalConfig writes cfg as the per-user configuration (CONFIG-SPEC §2),
// creating the taskmgr home if needed.
//
// The hooks block is validated before anything is written: a global hook that
// would fail to compile blocks mutations in *every* store on the machine
// (HOOK-SPEC §3.4/§3.5), so this refuses to persist one rather than leaving the
// error for the next write in some unrelated project to discover.
func SaveGlobalConfig(cfg GlobalConfig) error {
	if _, err := buildHookSet(cfg, Config{}); err != nil {
		return err
	}
	home, err := taskmgrHome(env.NewOS())
	if err != nil {
		return err
	}
	return saveGlobalConfig(vfs.NewOS(), home, cfg)
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
