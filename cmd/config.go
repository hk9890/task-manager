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
// of a configuration file, and manage its hooks block.
//
// Two files are addressable. Without --global the target is the resolved store's
// config.yaml (TASK-STORAGE-SPEC §4.2); with it, the per-user config.yaml
// (CONFIG-SPEC §2), which resolves no store and therefore works anywhere.
//
// Every write goes through the SDK (Store.SetConfig / tasks.SaveGlobalConfig),
// which validates the hooks block before a byte lands: a malformed one fails
// every later mutation (HOOK-SPEC §3.4), so it is refused at the command that
// caused it rather than at the next unrelated write.
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

// configHookDTO is one entry of `config hook list`. ID is the *effective* id —
// the one a denial reason and `config hook rm` use, including the "<event>#<n>"
// default and the "global:" scope prefix (HOOK-SPEC §3.5).
type configHookDTO struct {
	ID    string   `json:"id"`
	Scope string   `json:"scope"`
	Event string   `json:"event"`
	When  string   `json:"when,omitempty"`
	Run   []string `json:"run"`
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

// save persists the edited body. Both paths validate before writing.
func (t *configTarget) save() error {
	if t.global {
		return tasks.SaveGlobalConfig(t.gcfg)
	}
	return t.store.SetConfig(t.cfg)
}

func (t *configTarget) hooks() []tasks.Hook {
	if t.global {
		return t.gcfg.Hooks
	}
	return t.cfg.Hooks
}

func (t *configTarget) setHooks(hooks []tasks.Hook) {
	if t.global {
		t.gcfg.Hooks = hooks
		return
	}
	t.cfg.Hooks = hooks
}

// ── the key catalog ──────────────────────────────────────────────────────────

// configKeyDef is one scalar key of a configuration file. `hooks` is
// deliberately absent: it is a list, and `config hook` owns it.
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

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Read and write store and per-user configuration",
	Long: `Read and write configuration.

Without --global a command acts on the resolved store's config.yaml
(TASK-STORAGE-SPEC §4.2). With --global it acts on the per-user config.yaml
(CONFIG-SPEC §2), which needs no store and works in any directory.

'config keys' lists every supported key. The hooks block is a list, not a scalar,
so it is managed with 'config hook' rather than 'config set'.`,
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

var configListFlags struct{ global bool }

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show the current value of every key in one config file",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		t, err := loadConfigTarget(configListFlags.global)
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

var configGetFlags struct{ global bool }

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print the value of one configuration key",
	Long: `Print one key's current value and nothing else, so a script can consume it
without parsing. An unset key prints an empty line and exits 0.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		t, err := loadConfigTarget(configGetFlags.global)
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

var configSetFlags struct{ global bool }

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set one configuration key",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return writeConfigKey(configSetFlags.global, args[0], args[1])
	},
}

var configUnsetFlags struct{ global bool }

var configUnsetCmd = &cobra.Command{
	Use:   "unset <key>",
	Short: "Clear one configuration key, restoring its default",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return writeConfigKey(configUnsetFlags.global, args[0], "")
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
	if err := k.set(t, value); err != nil {
		return err
	}
	if err := t.save(); err != nil {
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

// ── config hook ──────────────────────────────────────────────────────────────

var configHookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Manage the lifecycle-gate hooks of one config file",
	Long: `Manage the hooks block (HOOK-SPEC). Hooks in the per-user config (--global) run
before the store's own hooks and apply to every store on this machine; they do
not travel with a repository, so a rule the data depends on belongs in the
store's config instead.`,
}

var configHookAddFlags struct {
	global bool
	id     string
	event  string
	when   string
	run    []string
}

var configHookAddCmd = &cobra.Command{
	Use:   "add --event <event> --run <arg> [--run <arg>…]",
	Short: "Append a hook to the config file",
	Long: `Append one hook.

--run is repeatable and each occurrence is exactly one argv element, matching the
way hooks are executed: directly via execve, with no shell. For shell features
pass the shell explicitly:

  taskmgr config hook add --event pre-close \
    --run sh --run -c --run 'make lint && make test'

--when takes a filter expression (QUERY-SPEC) evaluated against the candidate
issue; omitted, the hook runs for every occurrence of its event.

The working directory of a hook is always the project root, so a relative path in
a --global hook resolves against whichever project it runs in: give a global hook
an absolute path.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(configHookAddFlags.event) == "" {
			return &usageError{cmd: cmd, msg: "--event is required"}
		}
		if len(configHookAddFlags.run) == 0 {
			return &usageError{cmd: cmd, msg: "--run is required (repeat it once per argv element)"}
		}
		// The hook compile rejects this too, but that happens on the next write:
		// without the guard here `add` would save a config file that then fails
		// every mutation until it is hand-edited.
		if strings.Contains(configHookAddFlags.id, "#") {
			return &usageError{cmd: cmd, msg: `--id must not contain '#' (reserved for the defaulted "<event>#<index>" id)`}
		}
		t, err := loadConfigTarget(configHookAddFlags.global)
		if err != nil {
			return err
		}
		hook := tasks.Hook{
			ID:    strings.TrimSpace(configHookAddFlags.id),
			Event: strings.TrimSpace(configHookAddFlags.event),
			When:  strings.TrimSpace(configHookAddFlags.when),
			Run:   configHookAddFlags.run,
		}
		// Effective ids must stay unique within a file, or `config hook rm` could
		// not name one of the two and the second hook would be unaddressable.
		// Since a declared id cannot contain '#', the only remaining way to
		// collide is two entries declaring the same id.
		newID := tasks.HookID(hook, len(t.hooks()), t.global)
		for i, h := range t.hooks() {
			if tasks.HookID(h, i, t.global) == newID {
				return fmt.Errorf("hook id %q is already in use in %s", newID, t.path)
			}
		}
		t.setHooks(append(t.hooks(), hook))
		if err := t.save(); err != nil {
			return err
		}
		id := tasks.HookID(hook, len(t.hooks())-1, t.global)
		if flagJSON {
			return printJSON(hookDTO(hook, id, t.scope()))
		}
		_, _ = fmt.Fprintf(stdout, "Added hook %s (%s) to %s\n", id, hook.Event, t.path)
		return nil
	},
}

var configHookListFlags struct{ global bool }

var configHookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the hooks configured in one config file",
	Long: `List one file's hooks in the order they run, with the effective id of each —
the id a denial reason reports and 'config hook rm' takes.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		t, err := loadConfigTarget(configHookListFlags.global)
		if err != nil {
			return err
		}
		hooks := t.hooks()
		out := make([]configHookDTO, 0, len(hooks))
		for i, h := range hooks {
			out = append(out, hookDTO(h, tasks.HookID(h, i, t.global), t.scope()))
		}
		if flagJSON {
			return printJSON(out)
		}
		if len(out) == 0 {
			_, _ = fmt.Fprintf(stdout, "no hooks in %s\n", t.path)
			return nil
		}
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tEVENT\tWHEN\tRUN")
		for _, h := range out {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", h.ID, h.Event, orUnset(h.When), strings.Join(h.Run, " "))
		}
		return w.Flush()
	},
}

var configHookRmFlags struct{ global bool }

var configHookRmCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "Remove one hook by its effective id",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		t, err := loadConfigTarget(configHookRmFlags.global)
		if err != nil {
			return err
		}
		hooks := t.hooks()
		var kept []tasks.Hook
		var known []string
		found := false
		for i, h := range hooks {
			id := tasks.HookID(h, i, t.global)
			known = append(known, id)
			if id == args[0] && !found {
				found = true
				continue
			}
			kept = append(kept, h)
		}
		if !found {
			if len(known) == 0 {
				return fmt.Errorf("no hook %q: %s configures no hooks", args[0], t.path)
			}
			return fmt.Errorf("no hook %q in %s — configured: %s", args[0], t.path, strings.Join(known, ", "))
		}
		t.setHooks(kept)
		if err := t.save(); err != nil {
			return err
		}
		if flagJSON {
			return printJSON(map[string]string{"removed": args[0], "path": t.path})
		}
		_, _ = fmt.Fprintf(stdout, "Removed hook %s from %s\n", args[0], t.path)
		return nil
	},
}

// ── helpers ──────────────────────────────────────────────────────────────────

func hookDTO(h tasks.Hook, id, scope string) configHookDTO {
	return configHookDTO{ID: id, Scope: scope, Event: h.Event, When: h.When, Run: h.Run}
}

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
	configListCmd.Flags().BoolVar(&configListFlags.global, "global", false, "act on the per-user config instead of the store's")
	configGetCmd.Flags().BoolVar(&configGetFlags.global, "global", false, "act on the per-user config instead of the store's")
	configSetCmd.Flags().BoolVar(&configSetFlags.global, "global", false, "act on the per-user config instead of the store's")
	configUnsetCmd.Flags().BoolVar(&configUnsetFlags.global, "global", false, "act on the per-user config instead of the store's")

	configHookAddCmd.Flags().BoolVar(&configHookAddFlags.global, "global", false, "act on the per-user config instead of the store's")
	configHookAddCmd.Flags().StringVar(&configHookAddFlags.id, "id", "", "hook id used in messages, logs and 'config hook rm'")
	configHookAddCmd.Flags().StringVar(&configHookAddFlags.event, "event", "", "lifecycle event that fires the hook (required)")
	configHookAddCmd.Flags().StringVar(&configHookAddFlags.when, "when", "", "filter expression scoping the hook to matching issues")
	configHookAddCmd.Flags().StringArrayVar(&configHookAddFlags.run, "run", nil, "one argv element; repeat once per element (required)")

	configHookListCmd.Flags().BoolVar(&configHookListFlags.global, "global", false, "act on the per-user config instead of the store's")
	configHookRmCmd.Flags().BoolVar(&configHookRmFlags.global, "global", false, "act on the per-user config instead of the store's")

	configHookCmd.AddCommand(configHookAddCmd)
	configHookCmd.AddCommand(configHookListCmd)
	configHookCmd.AddCommand(configHookRmCmd)

	configCmd.AddCommand(configKeysCmd)
	configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configUnsetCmd)
	configCmd.AddCommand(configHookCmd)
	rootCmd.AddCommand(configCmd)
}
