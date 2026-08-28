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

// configdoc_test.go — L1 for the config writer. It is pure (bytes to bytes), so
// the forward-compatibility promise of TASK-STORAGE-SPEC §4.2 is testable
// without a store.
package tasks

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestApplyConfigToDoc_KeepsUnknownTopLevelKeys is the regression this file
// exists for: before it, a `config set` round-tripped the file through Config
// and silently deleted every key the struct does not model — including one a
// newer taskmgr had written.
func TestApplyConfigToDoc_KeepsUnknownTopLevelKeys(t *testing.T) {
	old := "prefix: proj\nfuture_feature: keep-me\n"

	got, err := applyConfigToDoc([]byte(old), Config{Prefix: "proj", HookTimeout: "5m"})
	if err != nil {
		t.Fatalf("applyConfigToDoc: %v", err)
	}
	if !strings.Contains(string(got), "future_feature: keep-me") {
		t.Errorf("unknown key was dropped:\n%s", got)
	}
	if !strings.Contains(string(got), "hook_timeout: 5m") {
		t.Errorf("the edit did not land:\n%s", got)
	}
}

func TestApplyConfigToDoc_KeepsComments(t *testing.T) {
	old := `# the store's ID prefix
prefix: proj

# raised for the test gate
hook_timeout: 5m
`
	got, err := applyConfigToDoc([]byte(old), Config{Prefix: "proj", HookTimeout: "10m"})
	if err != nil {
		t.Fatalf("applyConfigToDoc: %v", err)
	}
	for _, want := range []string{"# the store's ID prefix", "# raised for the test gate"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("comment %q was lost:\n%s", want, got)
		}
	}
	if !strings.Contains(string(got), "hook_timeout: 10m") {
		t.Errorf("the edit did not land:\n%s", got)
	}
}

// TestApplyConfigToDoc_KeepsUnknownKeysInsideAnUntouchedHook covers the same
// promise one level down (HOOK-SPEC §3.4): adding a hook must not strip a key a
// newer taskmgr wrote on a different one.
func TestApplyConfigToDoc_KeepsUnknownKeysInsideAnUntouchedHook(t *testing.T) {
	old := `prefix: proj
hooks:
    - id: gate
      event: pre-create
      run:
        - /bin/true
      future_hook_key: keep-me
`
	cfg := Config{Prefix: "proj", Hooks: []Hook{
		{ID: "gate", Event: "pre-create", Run: []string{"/bin/true"}},
		{ID: "second", Event: "post-close", Run: []string{"/bin/true"}},
	}}

	got, err := applyConfigToDoc([]byte(old), cfg)
	if err != nil {
		t.Fatalf("applyConfigToDoc: %v", err)
	}
	if !strings.Contains(string(got), "future_hook_key: keep-me") {
		t.Errorf("an unrelated hook's unknown key was dropped:\n%s", got)
	}
	if !strings.Contains(string(got), "id: second") {
		t.Errorf("the added hook is missing:\n%s", got)
	}
}

func TestApplyConfigToDoc_UnsetRemovesTheKey(t *testing.T) {
	old := "prefix: proj\nhook_timeout: 5m\n"

	got, err := applyConfigToDoc([]byte(old), Config{Prefix: "proj"})
	if err != nil {
		t.Fatalf("applyConfigToDoc: %v", err)
	}
	if strings.Contains(string(got), "hook_timeout") {
		t.Errorf("unset must remove the line, not blank it:\n%s", got)
	}
}

func TestApplyConfigToDoc_RemovingTheLastHookDropsTheBlock(t *testing.T) {
	old := "prefix: proj\nhooks:\n    - id: gate\n      event: pre-create\n      run: [/bin/true]\n"

	got, err := applyConfigToDoc([]byte(old), Config{Prefix: "proj"})
	if err != nil {
		t.Fatalf("applyConfigToDoc: %v", err)
	}
	if strings.Contains(string(got), "hooks:") {
		t.Errorf("an empty hooks list must not be written:\n%s", got)
	}
}

func TestApplyConfigToDoc_EmptyInputProducesAValidConfig(t *testing.T) {
	got, err := applyConfigToDoc(nil, Config{Prefix: "proj"})
	if err != nil {
		t.Fatalf("applyConfigToDoc: %v", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(got, &cfg); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if cfg.Prefix != "proj" {
		t.Errorf("prefix = %q, want proj", cfg.Prefix)
	}
}

// TestApplyConfigToDoc_RefusesANonMapping keeps a stray file from being
// overwritten: whatever it is, it is not a config, and replacing it would lose
// it.
func TestApplyConfigToDoc_RefusesANonMapping(t *testing.T) {
	if _, err := applyConfigToDoc([]byte("- just\n- a list\n"), Config{Prefix: "proj"}); err == nil {
		t.Fatal("a non-mapping document must not be silently replaced")
	}
}

func TestApplyGlobalConfigToDoc_KeepsUnknownKeysAndSetsVersion(t *testing.T) {
	old := "version: 1\ncentral_root: /srv/stores\nfuture_global_key: keep-me\n"

	got, err := applyGlobalConfigToDoc([]byte(old), GlobalConfig{
		Version: 1, CentralRoot: "/srv/stores", HookTimeout: "30s",
	})
	if err != nil {
		t.Fatalf("applyGlobalConfigToDoc: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "future_global_key: keep-me") {
		t.Errorf("unknown key was dropped:\n%s", s)
	}
	if !strings.Contains(s, "central_root: /srv/stores") {
		t.Errorf("central_root was lost:\n%s", s)
	}
	if !strings.Contains(s, "hook_timeout: 30s") {
		t.Errorf("the edit did not land:\n%s", s)
	}
	if !strings.Contains(s, "version: 1") {
		t.Errorf("version must always be written:\n%s", s)
	}
}

func TestApplyGlobalConfigToDoc_DefaultsVersionOnAFreshFile(t *testing.T) {
	got, err := applyGlobalConfigToDoc(nil, GlobalConfig{CentralRoot: "/srv"})
	if err != nil {
		t.Fatalf("applyGlobalConfigToDoc: %v", err)
	}
	if !strings.Contains(string(got), "version: 1") {
		t.Errorf("a fresh file must carry version 1:\n%s", got)
	}
}

// TestApplyToDoc_WritesEveryModelledField is the guard for the writer's one
// structural weakness: it names the keys it writes, one call per field, so a
// field added to Config or GlobalConfig is decoded on read (the YAML tag is
// enough for that) and then silently dropped on the next write. Nothing else
// notices — the round trip through the struct looks right, and the value only
// disappears once someone edits an unrelated key.
//
// Each field is filled with a sentinel and the rendered document must carry the
// field's YAML key. A field whose type this cannot fill fails loudly rather than
// passing vacuously: extend fillField with the new kind, and make sure the
// writer handles it.
func TestApplyToDoc_WritesEveryModelledField(t *testing.T) {
	t.Run("Config", func(t *testing.T) {
		var cfg Config
		keys := fillStruct(t, reflect.ValueOf(&cfg).Elem())
		got, err := applyConfigToDoc(nil, cfg)
		if err != nil {
			t.Fatalf("applyConfigToDoc: %v", err)
		}
		assertKeysWritten(t, string(got), keys)
	})

	t.Run("GlobalConfig", func(t *testing.T) {
		var cfg GlobalConfig
		keys := fillStruct(t, reflect.ValueOf(&cfg).Elem())
		got, err := applyGlobalConfigToDoc(nil, cfg)
		if err != nil {
			t.Fatalf("applyGlobalConfigToDoc: %v", err)
		}
		assertKeysWritten(t, string(got), keys)
	})
}

// fillStruct sets every field of v to a non-zero sentinel and returns the YAML
// key of each.
func fillStruct(t *testing.T, v reflect.Value) []string {
	t.Helper()
	var keys []string
	for i := range v.NumField() {
		field := v.Type().Field(i)
		key, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if key == "" || key == "-" {
			t.Fatalf("field %s has no YAML key: a modelled field needs one to survive a round trip", field.Name)
		}
		fillField(t, field.Name, v.Field(i))
		keys = append(keys, key)
	}
	return keys
}

func fillField(t *testing.T, name string, f reflect.Value) {
	t.Helper()
	switch {
	case f.Kind() == reflect.String:
		f.SetString("sentinel-" + strings.ToLower(name))
	case f.Kind() == reflect.Int:
		f.SetInt(7)
	case f.Type() == reflect.TypeOf([]Hook(nil)):
		f.Set(reflect.ValueOf([]Hook{{ID: "sentinel-hook", Event: eventPreClose, Run: []string{"true"}}}))
	default:
		t.Fatalf("field %s has type %s, which this guard cannot fill — extend fillField, "+
			"and check that the config writer handles the new type", name, f.Type())
	}
}

func assertKeysWritten(t *testing.T, doc string, keys []string) {
	t.Helper()
	for _, key := range keys {
		if !strings.Contains(doc, key+":") {
			t.Errorf("key %q is missing from the written document — the writer names its keys one "+
				"call at a time, so a new field is read but never written:\n%s", key, doc)
		}
	}
}
