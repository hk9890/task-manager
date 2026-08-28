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

// config.go — the `taskmgr config` command tree: read and write the scalar keys
// of a configuration file. The `use:` list of hook packages is a list, not a
// scalar, and belongs to `taskmgr package` (pkg.go).
//
// Two files are addressable. Without --global the target is the resolved store's
// config.yaml (TASK-STORAGE-SPEC §4.2); with it, the per-user config.yaml
// (CONFIG-SPEC §2), which resolves no store and therefore works anywhere.
//
// Every write goes through the SDK (Store.UpdateConfig / tasks.UpdateGlobalConfig),
// which re-reads the file under its lock and applies the edit there, so two
// concurrent invocations cannot discard each other's work.
package cmd

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// Scope names, used in output and in the key table.
const (
	scopeStore  = "store"
	scopeGlobal = "global"
)

// ── JSON DTOs (CLI-SPEC §6) ──────────────────────────────────────────────────

// configValueDTO is one key and its current value.
type configValueDTO struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Writable bool   `json:"writable"`
}

// configListDTO is the shape of `config list`.
type configListDTO struct {
	Scope string           `json:"scope"`
	Path  string           `json:"path"`
	Keys  []configValueDTO `json:"keys"`
}

// configKeyDTO is one row of `config keys` — the static catalog, not a value.
type configKeyDTO struct {
	Key         string `json:"key"`
	Scope       string `json:"scope"`
	Writable    bool   `json:"writable"`
	Description string `json:"description"`
}

// ── the target file ──────────────────────────────────────────────────────────

// configTarget is the configuration a subcommand reads and writes. Exactly one
// of the two config bodies is meaningful, selected by global.
type configTarget struct {
	global bool
	path   string
	store  *tasks.Store       // nil when global
	cfg    tasks.Config       // meaningful when !global
	gcfg   tasks.GlobalConfig // meaningful when global
}

// loadConfigTarget resolves and reads the file a subcommand acts on. The global
// config needs no store, so --global works in a directory that resolves none.
func loadConfigTarget(global bool) (*configTarget, error) {
	if global {
		path, err := tasks.GlobalConfigPath()
		if err != nil {
			return nil, err
		}
		gcfg, err := tasks.LoadGlobalConfig()
		if err != nil {
			return nil, err
		}
		return &configTarget{global: true, path: path, gcfg: gcfg}, nil
	}
	s, err := openStore()
	if err != nil {
		return nil, err
	}
	return &configTarget{
		path:  filepath.Join(s.Dir(), tasks.ConfigFileName),
		store: s,
		cfg:   s.Config(),
	}, nil
}

func (t *configTarget) scope() string {
	if t.global {
		return scopeGlobal
	}
	return scopeStore
}

// update applies edit to the target file under that file's lock. The body edit
// sees is the one on disk right now, not the copy loadConfigTarget read: the
// read has to happen inside the lock, or two `taskmgr config` processes each
// write a file built from a snapshot taken before the other one ran and the
// second silently drops the first's change.
func (t *configTarget) update(edit func(*configTarget) error) error {
	if t.global {
		return tasks.UpdateGlobalConfig(func(g *tasks.GlobalConfig) error {
			t.gcfg = *g
			if err := edit(t); err != nil {
				return err
			}
			*g = t.gcfg
			return nil
		})
	}
	return t.store.UpdateConfig(func(c *tasks.Config) error {
		t.cfg = *c
		if err := edit(t); err != nil {
			return err
		}
		*c = t.cfg
		return nil
	})
}

// use and setUse reach the `use:` list of whichever file this target names, so
// `taskmgr package` can edit either one through the same code path.
func (t *configTarget) use() []tasks.PackageRef {
	if t.global {
		return t.gcfg.Use
	}
	return t.cfg.Use
}

func (t *configTarget) setUse(refs []tasks.PackageRef) {
	if t.global {
		t.gcfg.Use = refs
		return
	}
	t.cfg.Use = refs
}

// ── the key catalog ──────────────────────────────────────────────────────────

// configKeyDef is one scalar key of a configuration file. `use` is deliberately
// absent: it is a list, and `taskmgr package` owns it.
type configKeyDef struct {
	name     string
	scope    string
	writable bool
	desc     string
	get      func(*configTarget) string
	set      func(*configTarget, string) error // nil when read-only
}

// configKeyDefs is the single source of truth for what `config get`/`set`/
// `unset`/`list` accept and what `config keys` prints.
var configKeyDefs = []configKeyDef{
	{
		name: "prefix", scope: scopeStore, writable: false,
		desc: "ID prefix for this store (read-only: it is part of every issue ID)",
		get:  func(t *configTarget) string { return t.cfg.Prefix },
	},
	{
		name: "hook_timeout", scope: scopeStore, writable: true,
		desc: "per-hook wall-clock limit, e.g. 2s or 5m; 0 disables it; unset means 2s",
		get:  func(t *configTarget) string { return t.cfg.HookTimeout },
		set:  func(t *configTarget, v string) error { t.cfg.HookTimeout = v; return nil },
	},
	{
		name: "version", scope: scopeGlobal, writable: false,
		desc: "schema version of the per-user config (read-only)",
		get:  func(t *configTarget) string { return strconv.Itoa(t.gcfg.Version) },
	},
	{
		name: "central_root", scope: scopeGlobal, writable: true,
		desc: "directory holding the registry and central stores; unset means the taskmgr home",
		get:  func(t *configTarget) string { return t.gcfg.CentralRoot },
		set:  func(t *configTarget, v string) error { t.gcfg.CentralRoot = v; return nil },
	},
	{
		name: "hook_timeout", scope: scopeGlobal, writable: true,
		desc: "fallback per-hook limit for a store that sets none",
		get:  func(t *configTarget) string { return t.gcfg.HookTimeout },
		set:  func(t *configTarget, v string) error { t.gcfg.HookTimeout = v; return nil },
	},
}

// keysForScope returns the catalog entries of one scope, in table order.
func keysForScope(scope string) []configKeyDef {
	var out []configKeyDef
	for _, k := range configKeyDefs {
		if k.scope == scope {
			out = append(out, k)
		}
	}
	return out
}

// lookupConfigKey finds a key within one scope. An unknown name names the ones
// that would have worked, so a typo does not require a second command.
func lookupConfigKey(name, scope string) (configKeyDef, error) {
	for _, k := range keysForScope(scope) {
		if k.name == name {
			return k, nil
		}
	}
	var known []string
	for _, k := range keysForScope(scope) {
		known = append(known, k.name)
	}
	return configKeyDef{}, fmt.Errorf("unknown %s config key %q — known keys: %s (see 'taskmgr config keys')",
		scope, name, strings.Join(known, ", "))
}

// ── commands ─────────────────────────────────────────────────────────────────

// configFlags holds the one selector every subcommand of this group reads. It is
// a persistent flag on the group rather than seven identical registrations on
// the leaves, so `config --global list` and `config list --global` both work and
// a new subcommand cannot silently default to writing the store's file.
var configFlags struct{ global bool }

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Read and write store and per-user configuration",
	Long: `Read and write configuration.

Without --global a command acts on the resolved store's config.yaml
(TASK-STORAGE-SPEC §4.2). With --global it acts on the per-user config.yaml
(CONFIG-SPEC §2), which needs no store and works in any directory.

'config keys' lists every supported key. The 'use:' list of hook packages is a
list, not a scalar, so it is managed with 'taskmgr package' rather than
'config set'.`,
}

var configKeysCmd = &cobra.Command{
	Use:   "keys",
	Short: "List every supported configuration key",
	Long: `List the configuration keys taskmgr understands, in both scopes, with whether
each one can be written. Reads nothing: it is the static catalog, so it works
without a store.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagJSON {
			out := make([]configKeyDTO, 0, len(configKeyDefs))
			for _, k := range configKeyDefs {
				out = append(out, configKeyDTO{Key: k.name, Scope: k.scope, Writable: k.writable, Description: k.desc})
			}
			return printJSON(out)
		}
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "KEY\tSCOPE\tWRITABLE\tDESCRIPTION")
		for _, k := range configKeyDefs {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", k.name, k.scope, yesNo(k.writable), k.desc)
		}
		return w.Flush()
	},
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show the current value of every key in one config file",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		t, err := loadConfigTarget(configFlags.global)
		if err != nil {
			return err
		}
		values := make([]configValueDTO, 0, 4)
		for _, k := range keysForScope(t.scope()) {
			values = append(values, configValueDTO{Key: k.name, Value: k.get(t), Writable: k.writable})
		}
		if flagJSON {
			return printJSON(configListDTO{Scope: t.scope(), Path: t.path, Keys: values})
		}
		_, _ = fmt.Fprintf(stdout, "scope: %s\n", t.scope())
		_, _ = fmt.Fprintf(stdout, "path:  %s\n\n", t.path)
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "KEY\tVALUE")
		for _, v := range values {
			_, _ = fmt.Fprintf(w, "%s\t%s\n", v.Key, orUnset(v.Value))
		}
		return w.Flush()
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print the value of one configuration key",
	Long: `Print one key's current value and nothing else, so a script can consume it
without parsing. An unset key prints an empty line and exits 0.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		t, err := loadConfigTarget(configFlags.global)
		if err != nil {
			return err
		}
		k, err := lookupConfigKey(args[0], t.scope())
		if err != nil {
			return err
		}
		if flagJSON {
			return printJSON(configValueDTO{Key: k.name, Value: k.get(t), Writable: k.writable})
		}
		_, _ = fmt.Fprintln(stdout, k.get(t))
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set one configuration key",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// An empty value is what `config unset` writes, and a wrapper passing an
		// unset shell variable would otherwise delete the key and report success.
		// The two commands exist separately so the destructive one has to be named.
		if args[1] == "" {
			return &usageError{cmd: cmd, msg: fmt.Sprintf(
				"empty value for %q — use 'taskmgr config unset %s' to clear it", args[0], args[0])}
		}
		return writeConfigKey(configFlags.global, args[0], args[1])
	},
}

var configUnsetCmd = &cobra.Command{
	Use:   "unset <key>",
	Short: "Clear one configuration key, restoring its default",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return writeConfigKey(configFlags.global, args[0], "")
	},
}

// writeConfigKey is the shared body of set and unset: same lookup, same
// read-only refusal, same save. Unset is set to the empty value, which is what
// restores a key's documented default.
func writeConfigKey(global bool, name, value string) error {
	t, err := loadConfigTarget(global)
	if err != nil {
		return err
	}
	k, err := lookupConfigKey(name, t.scope())
	if err != nil {
		return err
	}
	if !k.writable {
		return fmt.Errorf("%s config key %q is read-only: %s", t.scope(), k.name, k.desc)
	}
	if err := t.update(func(t *configTarget) error { return k.set(t, value) }); err != nil {
		return err
	}
	if flagJSON {
		return printJSON(configValueDTO{Key: k.name, Value: value, Writable: true})
	}
	if value == "" {
		_, _ = fmt.Fprintf(stdout, "Unset %s in %s\n", k.name, t.path)
		return nil
	}
	_, _ = fmt.Fprintf(stdout, "Set %s = %s in %s\n", k.name, value, t.path)
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

// orUnset renders an empty value in a human table, where a blank column reads as
// a rendering bug rather than as "not configured".
func orUnset(v string) string {
	if v == "" {
		return "(unset)"
	}
	return v
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func init() {
	configCmd.PersistentFlags().BoolVar(&configFlags.global, "global", false, "act on the per-user config instead of the store's")

	configCmd.AddCommand(configKeysCmd)
	configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configUnsetCmd)
	rootCmd.AddCommand(configCmd)
}
