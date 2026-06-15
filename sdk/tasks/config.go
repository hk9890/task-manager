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

	// envTaskmgrHome overrides the home; envTaskmgrDir is a store-path override.
	envTaskmgrHome = "TASKMGR_HOME"
	envTaskmgrDir  = "TASKMGR_DIR"
)

// globalConfig is the per-user configuration (CONFIG-SPEC §2). Every field is
// optional; the zero value plus defaults is valid. Unknown keys are ignored.
type globalConfig struct {
	Version     int    `yaml:"version"`
	CentralRoot string `yaml:"central_root"`
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
func loadGlobalConfig(fs vfs.FS, home string) (globalConfig, error) {
	cfg := globalConfig{Version: 1}
	data, err := fs.ReadFile(filepath.Join(home, globalConfigName))
	if err != nil {
		if vfs.IsNotExist(err) {
			return cfg, nil // defaults
		}
		return globalConfig{}, fmt.Errorf("read global config: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return globalConfig{}, fmt.Errorf("parse global config: %w", err)
	}
	return cfg, nil
}

// centralRoot resolves the central store root (CONFIG-SPEC §2/§3): cfg.CentralRoot
// with a leading ~ expanded and a relative value resolved against home,
// defaulting to home when unset.
func centralRoot(cfg globalConfig, home string) string {
	if cfg.CentralRoot == "" {
		return home
	}
	return lexCanon(cfg.CentralRoot, home, home)
}
