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

package cmd

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// jsonOutput declares what one command prints under --json, as a zero value of
// the printed type. withFlag names the flags that replace that shape with
// another one.
type jsonOutput struct {
	prints   any
	withFlag map[string]any
}

// jsonOutputs is the one place a command is tied to the type it prints, keyed by
// the command's catalog name. The catalog reflects the field list out of the
// type, so a declared type cannot drift from its fields. What a map cannot
// enforce is that the declaration names the type the command prints, or that a
// command is declared at all: printJSON records the type it is handed, and
// TestCommands_Output_EveryCommandPrintsItsDeclaredType runs every command and
// compares the two.
//
// A pre-hook denial (hookDeniedDTO) is an error shape printed at exit 1 and is
// not declared here.
var jsonOutputs = map[string]jsonOutput{
	"blocked":             {prints: []blockedDTO{}},
	"close":               {prints: mutationDTO{}},
	"comment add":         {prints: commentDTO{}},
	"comment edit":        {prints: commentDTO{}},
	"comment rm":          {prints: commentDeleteDTO{}},
	"config get":          {prints: configValueDTO{}},
	"config keys":         {prints: []configKeyDTO{}},
	"config list":         {prints: configListDTO{}},
	"config set":          {prints: configValueDTO{}},
	"config unset":        {prints: configValueDTO{}},
	"create":              {prints: createResultDTO{}, withFlag: map[string]any{"from": []createFromResultDTO{}}},
	"dep add":             {prints: edgeResultDTO{}},
	"dep rm":              {prints: edgeResultDTO{}},
	"guide":               {prints: guideDTO{}, withFlag: map[string]any{"list": []guideTopicDTO{}}},
	"hook list":           {prints: []hookDTO{}},
	"import":              {prints: importResult{}, withFlag: map[string]any{"batch": []importResult{}}},
	"init":                {prints: initDTO{}},
	"labels":              {prints: []string{}},
	"list":                {prints: []issueDTO{}},
	"package add":         {prints: packageDTO{}},
	"package list":        {prints: []packageDTO{}},
	"package repo add":    {prints: repoAddedDTO{}},
	"package repo list":   {prints: []repoDTO{}},
	"package repo rm":     {prints: repoRemovedDTO{}},
	"package repo update": {prints: []repoAddedDTO{}},
	"package rm":          {prints: packageRemovedDTO{}},
	"ready":               {prints: []issueDTO{}},
	"rel add":             {prints: edgeResultDTO{}},
	"rel rm":              {prints: edgeResultDTO{}},
	"reopen":              {prints: mutationDTO{}},
	"search":              {prints: []issueDTO{}},
	"show":                {prints: []detailDTO{}},
	"statuses":            {prints: []string{}},
	"store list":          {prints: []storeEntryDTO{}},
	"store move":          {prints: storeMoveDTO{}},
	"tree":                {prints: []treeDTO{}},
	"types":               {prints: []string{}},
	"update":              {prints: mutationDTO{}},
	"version":             {prints: versionDTO{}},
	"where":               {prints: whereDTO{}},
}

// shapeDoc is the JSON shape of a value: its JSON type, the element type of an
// array, and the fields of an object or of each object in an array. Recursive
// stands in for the fields where they are those of the object the value is a
// field of, as with the children of a tree node.
type shapeDoc struct {
	Type      string     `yaml:"type" json:"type"`
	Items     string     `yaml:"items,omitempty" json:"items,omitempty"`
	Recursive bool       `yaml:"recursive,omitempty" json:"recursive,omitempty"`
	Fields    []fieldDoc `yaml:"fields,omitempty" json:"fields,omitempty"`
}

// fieldDoc is one field of a printed object: a name, the keys of a shapeDoc,
// and Optional, which marks a field the encoder omits when it is empty.
type fieldDoc struct {
	Name      string     `yaml:"name" json:"name"`
	Type      string     `yaml:"type" json:"type"`
	Items     string     `yaml:"items,omitempty" json:"items,omitempty"`
	Recursive bool       `yaml:"recursive,omitempty" json:"recursive,omitempty"`
	Optional  bool       `yaml:"optional,omitempty" json:"optional,omitempty"`
	Fields    []fieldDoc `yaml:"fields,omitempty" json:"fields,omitempty"`
}

// MarshalYAML prints a field without nested fields on one line. The catalog
// carries several hundred fields, and three lines for each would triple it.
func (f fieldDoc) MarshalYAML() (any, error) {
	type plain fieldDoc
	var n yaml.Node
	if err := n.Encode(plain(f)); err != nil {
		return nil, err
	}
	if len(f.Fields) == 0 {
		n.Style = yaml.FlowStyle
	}
	return &n, nil
}

// flagOutputDoc is the shape a command prints instead when Flag is set.
type flagOutputDoc struct {
	Flag     string `yaml:"flag" json:"flag"`
	shapeDoc `yaml:",inline"`
}

// outputDoc is the `output` entry of a command in the catalog.
type outputDoc struct {
	shapeDoc `yaml:",inline"`
	WithFlag []flagOutputDoc `yaml:"with_flag,omitempty" json:"with_flag,omitempty"`
}

// outputFor builds the `output` entry of the named command, nil for a command
// that prints no JSON.
func outputFor(name string) *outputDoc {
	decl, ok := jsonOutputs[name]
	if !ok {
		return nil
	}
	out := &outputDoc{shapeDoc: shapeOf(reflect.TypeOf(decl.prints), nil)}
	for flag, v := range decl.withFlag {
		out.WithFlag = append(out.WithFlag, flagOutputDoc{Flag: flag, shapeDoc: shapeOf(reflect.TypeOf(v), nil)})
	}
	sort.Slice(out.WithFlag, func(i, j int) bool { return out.WithFlag[i].Flag < out.WithFlag[j].Flag })
	return out
}

var timeType = reflect.TypeOf(time.Time{})

// shapeOf maps a Go type to the JSON shape encoding/json gives it. enclosing
// holds the structs the type is nested in, innermost last: a struct that
// contains itself is reported as recursive instead of being walked again.
func shapeOf(t reflect.Type, enclosing []reflect.Type) shapeDoc {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == timeType {
		return shapeDoc{Type: "string"}
	}
	switch t.Kind() {
	case reflect.String:
		return shapeDoc{Type: "string"}
	case reflect.Bool:
		return shapeDoc{Type: "boolean"}
	case reflect.Int:
		return shapeDoc{Type: "integer"}
	case reflect.Slice:
		elem := shapeOf(t.Elem(), enclosing)
		return shapeDoc{Type: "array", Items: elem.Type, Recursive: elem.Recursive, Fields: elem.Fields}
	case reflect.Struct:
		if n := len(enclosing); n > 0 && enclosing[n-1] == t {
			return shapeDoc{Type: "object", Recursive: true}
		}
		if slices.Contains(enclosing, t) {
			panic(fmt.Sprintf("commands catalog: %s contains itself through another type, which `recursive` cannot express", t))
		}
		return shapeDoc{Type: "object", Fields: fieldsOf(t, append(slices.Clip(enclosing), t))}
	}
	panic(fmt.Sprintf("commands catalog: no JSON shape for %s", t))
}

// fieldsOf lists the fields of a struct as encoding/json names them. An
// embedded struct contributes its fields to the object that embeds it.
func fieldsOf(t reflect.Type, enclosing []reflect.Type) []fieldDoc {
	var out []fieldDoc
	for i := range t.NumField() {
		f := t.Field(i)
		name, options, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous && name == "" {
			out = append(out, fieldsOf(f.Type, enclosing)...)
			continue
		}
		shape := shapeOf(f.Type, enclosing)
		out = append(out, fieldDoc{
			Name:      name,
			Type:      shape.Type,
			Items:     shape.Items,
			Recursive: shape.Recursive,
			Optional:  omittedWhenEmpty(f.Type, strings.Split(options, ",")),
			Fields:    shape.Fields,
		})
	}
	return out
}

// omittedWhenEmpty reports whether encoding/json leaves a field of type t out
// under the given tag options. omitempty never omits a struct, so a time.Time
// that carries it is always printed.
func omittedWhenEmpty(t reflect.Type, options []string) bool {
	return slices.Contains(options, "omitzero") ||
		slices.Contains(options, "omitempty") && t.Kind() != reflect.Struct
}
