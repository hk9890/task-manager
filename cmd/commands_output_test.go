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

// Tests for the `output` entry of the command catalog — the JSON shape each
// command prints (CLI-SPEC §5.2).
//
// Coverage:
//   - The entry is served in both formats and names the fields of show, list
//     and create.
//   - Every command is run against a store, and each prints the type that
//     jsonOutputs declares for it, under each flag that changes the shape.
//   - The reflected shape of every declared type equals what encoding/json
//     prints for it: field for field, in print order, with `optional` on
//     exactly the fields the encoder omits.
//   - A type that contains itself is served as `recursive`.
package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// catOutput decodes the part of a served `output` entry the tests assert on. It
// is spelled out rather than borrowed from outputDoc so that a renamed key
// fails here.
type catOutput struct {
	Commands []struct {
		Name   string `yaml:"name" json:"name"`
		Output *struct {
			Type   string `yaml:"type" json:"type"`
			Fields []struct {
				Name string `yaml:"name" json:"name"`
			} `yaml:"fields" json:"fields"`
		} `yaml:"output" json:"output"`
	} `yaml:"commands" json:"commands"`
}

// The fields agents guessed at before the catalog carried them (at-05cqt3), in
// both formats: YAML is what an agent reads, JSON what it pipes into jq.
func TestCommands_Output_NamesTheFieldsOfShowListAndCreate(t *testing.T) {
	want := map[string]struct {
		kind   string
		fields []string
	}{
		"show":   {"array", []string{"description", "comments", "blocked_by_refs", "children"}},
		"list":   {"array", []string{"id", "title", "status", "parent", "created", "updated"}},
		"create": {"object", []string{"id", "store"}},
	}
	formats := []struct {
		args      []string
		unmarshal func([]byte, any) error
	}{
		{[]string{"commands"}, yaml.Unmarshal},
		{[]string{"commands", "--json"}, json.Unmarshal},
	}
	for _, format := range formats {
		stdout, stderr, code := run(t, format.args...)
		if code != 0 {
			t.Fatalf("%v exit=%d stderr=%q", format.args, code, stderr)
		}
		var cat catOutput
		if err := format.unmarshal([]byte(stdout), &cat); err != nil {
			t.Fatalf("%v: %v", format.args, err)
		}
		seen := 0
		for _, c := range cat.Commands {
			w, ok := want[c.Name]
			if !ok {
				continue
			}
			seen++
			if c.Output == nil {
				t.Errorf("%v: %q has no output entry", format.args, c.Name)
				continue
			}
			if c.Output.Type != w.kind {
				t.Errorf("%v: %q output type = %q, want %q", format.args, c.Name, c.Output.Type, w.kind)
			}
			var got []string
			for _, f := range c.Output.Fields {
				got = append(got, f.Name)
			}
			for _, f := range w.fields {
				if !slices.Contains(got, f) {
					t.Errorf("%v: %q output lacks field %q; has %v", format.args, c.Name, f, got)
				}
			}
		}
		if seen != len(want) {
			t.Errorf("%v: found %d of the %d commands under test", format.args, seen, len(want))
		}
	}
}

// declaredOutput returns what jsonOutputs declares for c as it was last invoked:
// the declaration's name — the catalog name, or "name --flag" when a flag of
// with_flag was set — and the type, nil for a command that declares none.
func declaredOutput(c *cobra.Command) (string, reflect.Type) {
	name := displayName(c)
	decl, ok := jsonOutputs[name]
	if !ok {
		return name, nil
	}
	for flag, v := range decl.withFlag {
		if c.Flags().Changed(flag) {
			return name + " --" + flag, reflect.TypeOf(v)
		}
	}
	return name, reflect.TypeOf(decl.prints)
}

// assertPrintsDeclaredType is the link between the catalog and the output: the
// type the invocation handed printJSON must be the one jsonOutputs declares for
// the command and its flags. run calls it after every successful invocation, so
// each in-process CLI test is a guard for the commands it runs.
func assertPrintsDeclaredType(t *testing.T, args []string, stdout []byte) {
	t.Helper()
	c, _, err := rootCmd.Find(args)
	// The catalog is the one command that describes itself by being read.
	if err != nil || displayName(c) == "commands" {
		return
	}
	if printedType == nil {
		if flagJSON && json.Valid(stdout) {
			t.Errorf("%q printed JSON without printJSON, so the catalog cannot describe it", displayName(c))
		}
		return
	}
	declaration, want := declaredOutput(c)
	switch {
	case want == nil:
		t.Errorf("%q printed a %s, but jsonOutputs declares no output for it", declaration, printedType)
	case printedType != want:
		t.Errorf("%q printed a %s, but jsonOutputs declares %s", declaration, printedType, want)
	}
}

// Every command of the catalog is run here under --json, and run compares what
// each printed with jsonOutputs. A command added without an invocation below
// fails, as does a declaration no invocation exercised, so the table cannot
// name a type the command does not print.
func TestCommands_Output_EveryCommandPrintsItsDeclaredType(t *testing.T) {
	isolatedHome(t)
	root, _ := treeStore(t)
	ran := map[string]bool{}
	printed := map[string]bool{}
	invoke := func(args ...string) string {
		t.Helper()
		args = append([]string{"--json"}, args...)
		out, errOut, code := run(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
		}
		c, _, err := rootCmd.Find(args)
		if err != nil {
			t.Fatal(err)
		}
		ran[displayName(c)] = true
		if printedType != nil {
			declaration, _ := declaredOutput(c)
			printed[declaration] = true
		}
		return out
	}
	inStore := func(args ...string) string {
		t.Helper()
		return invoke(append([]string{"--dir", root}, args...)...)
	}
	idOf := func(out string) string {
		t.Helper()
		var d struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(out), &d); err != nil || d.ID == "" {
			t.Fatalf("no id in %q: %v", out, err)
		}
		return d.ID
	}

	invoke("version")
	invoke("statuses")
	invoke("types")
	invoke("guide")
	invoke("guide", "--list")
	invoke("config", "keys")

	invoke("--dir", t.TempDir(), "init")
	central := t.TempDir()
	invoke("--dir", central, "init", "--central", "--store-name", "central")
	invoke("store", "list")
	invoke("--dir", central, "store", "move", "--rename", "--to", "renamed")

	inStore("where")
	inStore("config", "list")
	inStore("config", "set", "hook_timeout", "5s")
	inStore("config", "get", "hook_timeout")
	inStore("config", "unset", "hook_timeout")

	inStore("create", "--title", "created")
	inStore("show", "tst-0001")
	inStore("list")
	inStore("search", "schema")
	inStore("ready")
	inStore("blocked")
	inStore("tree")
	inStore("labels")
	inStore("update", "tst-0004", "--title", "retitled")
	inStore("dep", "add", "tst-0004", "tst-0002")
	inStore("dep", "rm", "tst-0004", "tst-0002")
	inStore("rel", "add", "tst-0004", "tst-0002")
	inStore("rel", "rm", "tst-0004", "tst-0002")
	comment := idOf(inStore("comment", "add", "tst-0004", "a note"))
	comment = idOf(inStore("comment", "edit", "tst-0004", comment, "an edited note"))
	inStore("comment", "rm", "tst-0004", comment)
	inStore("close", "tst-0004", "--reason", "done")
	inStore("reopen", "tst-0004")

	envelope := filepath.Join(t.TempDir(), "envelope.json")
	if err := os.WriteFile(envelope, []byte(`{"title": "imported"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	set := filepath.Join(t.TempDir(), "set.yaml")
	if err := os.WriteFile(set, []byte("- title: from a set\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inStore("create", "--from", set)
	inStore("import", "--file", envelope)
	inStore("import", "--batch", "--file", envelope)

	origin := originRepo(t, "task-writing")
	invoke("package", "repo", "add", origin)
	invoke("package", "repo", "list")
	invoke("package", "repo", "update")
	inStore("package", "add", "task-writing")
	inStore("package", "list")
	inStore("hook", "list")
	inStore("package", "rm", "task-writing")
	invoke("package", "repo", "rm", filepath.Base(origin))

	for _, entry := range buildCatalog(rootCmd).Commands {
		c, _, err := rootCmd.Find(strings.Fields(entry.Name))
		if err != nil {
			t.Fatal(err)
		}
		if c.HasAvailableSubCommands() || entry.Name == "commands" {
			continue
		}
		if !ran[entry.Name] {
			t.Errorf("%q is not run here, so nothing compares what it prints with jsonOutputs", entry.Name)
		}
	}
	for name, decl := range jsonOutputs {
		if !printed[name] {
			t.Errorf("jsonOutputs declares %q, and no invocation printed JSON for it", name)
		}
		for flag := range decl.withFlag {
			if declaration := name + " --" + flag; !printed[declaration] {
				t.Errorf("jsonOutputs declares %q, and no invocation printed JSON for it", declaration)
			}
		}
	}
}

// The catalog reflects each shape out of the declared type, and this checks the
// reflection against the encoder itself: every declared type is filled so that
// no omitempty field drops out, encoded, and compared with what the catalog
// claims. A field added to issueDTO that `list` did not carry would fail here,
// as would a field the catalog names or types differently from the JSON.
func TestCommands_Output_CarriesEveryFieldTheTypeEncodes(t *testing.T) {
	outputs := map[string]*outputDoc{}
	for _, c := range buildCatalog(rootCmd).Commands {
		outputs[c.Name] = c.Output
	}
	for name, decl := range jsonOutputs {
		out := outputs[name]
		if out == nil {
			t.Errorf("%q: declared in jsonOutputs but the catalog carries no output", name)
			continue
		}
		assertShapeMatchesJSON(t, name, out.shapeDoc, encodeFilled(t, reflect.TypeOf(decl.prints)))
		if len(out.WithFlag) != len(decl.withFlag) {
			t.Errorf("%q: catalog carries %d flag shapes, declared %d", name, len(out.WithFlag), len(decl.withFlag))
		}
		for _, v := range out.WithFlag {
			assertShapeMatchesJSON(t, name+" --"+v.Flag, v.shapeDoc, encodeFilled(t, reflect.TypeOf(decl.withFlag[v.Flag])))
		}
	}
}

// A tree node holds tree nodes. The catalog says so with `recursive` in place of
// the fields, where walking the type again would never end.
func TestCommands_Output_ATypeThatContainsItselfIsRecursive(t *testing.T) {
	out := outputFor("tree")
	if out == nil || out.Type != "array" || out.Items != "object" {
		t.Fatalf("tree output is %+v, want an array of objects", out)
	}
	i := slices.IndexFunc(out.Fields, func(f fieldDoc) bool { return f.Name == "children" })
	if i < 0 {
		t.Fatalf("tree output has no children field: %+v", out.Fields)
	}
	want := fieldDoc{Name: "children", Type: "array", Items: "object", Recursive: true, Optional: true}
	if got := out.Fields[i]; !reflect.DeepEqual(got, want) {
		t.Errorf("children = %+v, want %+v", got, want)
	}
}

// optionalProbe carries the tag forms no DTO uses yet, so the two tests below
// hold the reflection to the encoder on them as well.
type optionalProbe struct {
	Always    time.Time  `json:"always,omitempty"`
	Zero      time.Time  `json:"zero,omitzero"`
	Pointer   *time.Time `json:"pointer,omitempty"`
	Omitempty string     `json:"omitempty"`
}

// printedStructs returns every struct a declared output is made of, each once.
func printedStructs() []reflect.Type {
	structs := []reflect.Type{reflect.TypeOf(optionalProbe{})}
	var collect func(t reflect.Type)
	collect = func(t reflect.Type) {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice:
			collect(t.Elem())
		case reflect.Struct:
			if t == timeType || slices.Contains(structs, t) {
				return
			}
			structs = append(structs, t)
			for i := range t.NumField() {
				collect(t.Field(i).Type)
			}
		}
	}
	for _, decl := range jsonOutputs {
		collect(reflect.TypeOf(decl.prints))
		for _, v := range decl.withFlag {
			collect(reflect.TypeOf(v))
		}
	}
	return structs
}

// `optional` promises that a field is absent while it is empty, so it is held to
// what the encoder does with a zero value rather than to how the tag reads.
func TestCommands_Output_OptionalIsWhatTheEncoderOmits(t *testing.T) {
	for _, typ := range printedStructs() {
		raw, err := json.Marshal(reflect.Zero(typ).Interface())
		if err != nil {
			t.Fatal(err)
		}
		var encoded map[string]any
		if err := json.Unmarshal(raw, &encoded); err != nil {
			t.Fatal(err)
		}
		for _, f := range fieldsOf(typ, []reflect.Type{typ}) {
			if _, present := encoded[f.Name]; present == f.Optional {
				t.Errorf("%s.%s: catalog says optional=%v, the encoder printed a zero value as %s", typ, f.Name, f.Optional, raw)
			}
		}
	}
}

func TestCommands_Output_FieldsAreInPrintOrder(t *testing.T) {
	for _, typ := range printedStructs() {
		v := reflect.New(typ)
		fill(v.Elem(), nil)
		raw, err := json.Marshal(v.Interface())
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, f := range fieldsOf(typ, []reflect.Type{typ}) {
			names = append(names, f.Name)
		}
		if printed := objectKeys(t, raw); !slices.Equal(names, printed) {
			t.Errorf("%s: catalog fields %v, the encoder prints %v", typ, names, printed)
		}
	}
}

// objectKeys returns the keys of an encoded object in the order they were
// written, which decoding into a map loses.
func objectKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key.(string))
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// encodeFilled returns what encoding/json makes of a value of type typ in which
// every field, at every depth, is non-empty.
func encodeFilled(t *testing.T, typ reflect.Type) any {
	t.Helper()
	v := reflect.New(typ)
	fill(v.Elem(), nil)
	raw, err := json.Marshal(v.Interface())
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// fill makes v non-empty at every depth. enclosing holds the structs v is nested
// in: a struct that contains itself is filled one level down and left empty
// below that, which is where the walk ends.
func fill(v reflect.Value, enclosing []reflect.Type) {
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int:
		v.SetInt(1)
	case reflect.Pointer:
		if !filledTwice(enclosing, v.Type().Elem()) {
			v.Set(reflect.New(v.Type().Elem()))
			fill(v.Elem(), enclosing)
		}
	case reflect.Slice:
		if !filledTwice(enclosing, v.Type().Elem()) {
			v.Set(reflect.MakeSlice(v.Type(), 1, 1))
			fill(v.Index(0), enclosing)
		}
	case reflect.Struct:
		if v.Type() == timeType {
			v.Set(reflect.ValueOf(time.Unix(1, 0)))
			return
		}
		enclosing = append(slices.Clip(enclosing), v.Type())
		for i := range v.NumField() {
			fill(v.Field(i), enclosing)
		}
	default:
		panic("fill: no value for " + v.Type().String())
	}
}

func filledTwice(enclosing []reflect.Type, t reflect.Type) bool {
	n := 0
	for _, e := range enclosing {
		if e == t {
			n++
		}
	}
	return n >= 2
}

func assertShapeMatchesJSON(t *testing.T, path string, shape shapeDoc, got any) {
	t.Helper()
	if typ := jsonTypeOf(got); typ != shape.Type {
		t.Errorf("%s: catalog says %q, the encoder prints %q", path, shape.Type, typ)
		return
	}
	switch got := got.(type) {
	case []any:
		assertShapeMatchesJSON(t, path+"[]", shapeDoc{Type: shape.Items, Fields: shape.Fields}, got[0])
	case map[string]any:
		var names []string
		for _, f := range shape.Fields {
			value, ok := got[f.Name]
			fields := f.Fields
			if f.Recursive {
				// fill leaves the field empty one level down, and it is optional.
				if !ok {
					continue
				}
				fields = shape.Fields
			}
			names = append(names, f.Name)
			if ok {
				assertShapeMatchesJSON(t, path+"."+f.Name, shapeDoc{Type: f.Type, Items: f.Items, Fields: fields}, value)
			}
		}
		var encoded []string
		for k := range got {
			encoded = append(encoded, k)
		}
		slices.Sort(names)
		slices.Sort(encoded)
		if !slices.Equal(names, encoded) {
			t.Errorf("%s: catalog fields %v, the encoder prints %v", path, names, encoded)
		}
	}
}

// jsonTypeOf names the JSON type of a decoded value. Every number a DTO carries
// is a Go int, so a number is an integer.
func jsonTypeOf(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "integer"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "null"
}
