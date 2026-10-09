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
// command prints (CLI-SPEC §5).
//
// Coverage:
//   - The entry is served in both formats and names the fields of show, list
//     and create.
//   - Every command whose source reaches printJSON declares its type, and
//     nothing is declared that does not print.
//   - The reflected shape of every declared type equals what encoding/json
//     prints for it, field for field.
package cmd

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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
		"show":   {"object", []string{"description", "comments", "blocked_by_refs", "children"}},
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

// sourceCommand is one `var xCmd = &cobra.Command{...}` as the source spells it.
type sourceCommand struct {
	use string
	run ast.Expr
}

// A command that prints JSON and is absent from jsonOutputs would be served
// without an `output` entry, and nothing at run time can notice: the map is the
// only link between a command and its type. So this reads the source — every
// RunE that reaches printJSON, directly or through the functions it calls, must
// be declared, and nothing may be declared that does not print.
func TestCommands_EveryJSONPrinterDeclaresItsOutput(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	funcs := map[string]*ast.BlockStmt{}
	commands := map[string]sourceCommand{}
	parent := map[string]string{}
	fset := token.NewFileSet()
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl:
				if n.Recv == nil {
					funcs[n.Name.Name] = n.Body
				}
			case *ast.ValueSpec:
				if len(n.Names) == 1 && len(n.Values) == 1 {
					if c, ok := parseCommandLiteral(n.Values[0]); ok {
						commands[n.Names[0].Name] = c
					}
				}
			case *ast.CallExpr:
				sel, ok := n.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "AddCommand" {
					break
				}
				for _, arg := range n.Args {
					parent[arg.(*ast.Ident).Name] = sel.X.(*ast.Ident).Name
				}
			}
			return true
		})
	}

	var reachesPrintJSON func(n ast.Node, seen map[string]bool) bool
	reachesPrintJSON = func(n ast.Node, seen map[string]bool) bool {
		found := false
		ast.Inspect(n, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || found {
				return !found
			}
			callee, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			if callee.Name == "printJSON" {
				found = true
			} else if body, known := funcs[callee.Name]; known && !seen[callee.Name] {
				seen[callee.Name] = true
				found = reachesPrintJSON(body, seen)
			}
			return !found
		})
		return found
	}

	var catalogName func(varName string) string
	catalogName = func(varName string) string {
		if varName == "rootCmd" {
			return ""
		}
		return strings.TrimSpace(catalogName(parent[varName]) + " " + commands[varName].use)
	}

	served := map[string]bool{}
	for _, c := range buildCatalog(rootCmd).Commands {
		served[c.Name] = c.Output != nil
	}
	checked := 0
	for varName, c := range commands {
		name := catalogName(varName)
		hasOutput, inCatalog := served[name]
		// The catalog is the one command that describes itself by being read.
		if !inCatalog || name == "commands" {
			continue
		}
		checked++
		prints := false
		switch run := c.run.(type) {
		case *ast.Ident:
			prints = reachesPrintJSON(funcs[run.Name], map[string]bool{})
		case *ast.FuncLit:
			prints = reachesPrintJSON(run, map[string]bool{})
		}
		switch {
		case prints && !hasOutput:
			t.Errorf("%q (%s) prints JSON but jsonOutputs does not declare its type", name, varName)
		case !prints && hasOutput:
			t.Errorf("%q (%s) is declared in jsonOutputs but never reaches printJSON", name, varName)
		}
	}
	if checked < len(jsonOutputs) {
		t.Errorf("read %d commands from the source, fewer than the %d declared", checked, len(jsonOutputs))
	}

	for name, decl := range jsonOutputs {
		if _, ok := served[name]; !ok {
			t.Errorf("jsonOutputs declares %q, which is not a command in the catalog", name)
			continue
		}
		c, _, err := rootCmd.Find(strings.Fields(name))
		if err != nil {
			t.Fatal(err)
		}
		for flag := range decl.withFlag {
			if c.Flags().Lookup(flag) == nil {
				t.Errorf("jsonOutputs declares a shape for %q --%s, which is not a flag of it", name, flag)
			}
		}
	}
}

// parseCommandLiteral reads the name and the RunE of a `&cobra.Command{...}`.
func parseCommandLiteral(e ast.Expr) (sourceCommand, bool) {
	addr, ok := e.(*ast.UnaryExpr)
	if !ok {
		return sourceCommand{}, false
	}
	lit, ok := addr.X.(*ast.CompositeLit)
	if !ok {
		return sourceCommand{}, false
	}
	if typ, ok := lit.Type.(*ast.SelectorExpr); !ok || typ.Sel.Name != "Command" {
		return sourceCommand{}, false
	}
	var c sourceCommand
	for _, elt := range lit.Elts {
		kv := elt.(*ast.KeyValueExpr)
		switch kv.Key.(*ast.Ident).Name {
		case "Use":
			use, err := strconv.Unquote(kv.Value.(*ast.BasicLit).Value)
			if err != nil {
				return sourceCommand{}, false
			}
			c.use = strings.Fields(use)[0]
		case "RunE":
			c.run = kv.Value
		}
	}
	return c, true
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
		assertShapeMatchesJSON(t, name, out.shapeDoc, encodeFilled(t, decl.prints))
		if len(out.WithFlag) != len(decl.withFlag) {
			t.Errorf("%q: catalog carries %d flag shapes, declared %d", name, len(out.WithFlag), len(decl.withFlag))
		}
		for _, v := range out.WithFlag {
			assertShapeMatchesJSON(t, name+" --"+v.Flag, v.shapeDoc, encodeFilled(t, decl.withFlag[v.Flag]))
		}
	}
}

// encodeFilled returns what encoding/json makes of a value of prototype's type
// in which every field, at every depth, is non-empty.
func encodeFilled(t *testing.T, prototype any) any {
	t.Helper()
	v := reflect.New(reflect.TypeOf(prototype))
	fill(v.Elem())
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

func fill(v reflect.Value) {
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int:
		v.SetInt(1)
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fill(v.Elem())
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fill(v.Index(0))
	case reflect.Struct:
		if v.Type() == timeType {
			v.Set(reflect.ValueOf(time.Unix(1, 0)))
			return
		}
		for i := range v.NumField() {
			fill(v.Field(i))
		}
	default:
		panic("fill: no value for " + v.Type().String())
	}
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
			names = append(names, f.Name)
			if value, ok := got[f.Name]; ok {
				assertShapeMatchesJSON(t, path+"."+f.Name, shapeDoc{Type: f.Type, Items: f.Items, Fields: f.Fields}, value)
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
