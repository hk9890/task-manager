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
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hk9890/task-manager/sdk/tasks"
)

var showFlags struct {
	fields []string
}

var showCmd = &cobra.Command{
	Use:   "show <id> [more ids...]",
	Short: "Show full detail for one or more issues",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		selected, err := selectedDetailFields(cmd)
		if err != nil {
			return err
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		// Every ID resolves before anything prints, so a missing one leaves stdout empty.
		details := make([]*tasks.Detail, len(args))
		for i, id := range args {
			if details[i], err = s.Detail(id); err != nil {
				return err
			}
		}
		if !flagJSON {
			for i, d := range details {
				if i > 0 {
					_, _ = fmt.Fprintln(stdout)
				}
				printDetail(d, selected)
			}
			return nil
		}
		dtos := make([]any, len(details))
		for i, d := range details {
			dto := toDetailDTO(s.Name(), d)
			dtos[i] = dto
			if selected != nil {
				if dtos[i], err = selectFields(dto, selected); err != nil {
					return err
				}
			}
		}
		if len(dtos) == 1 {
			return printJSON(dtos[0])
		}
		return printJSON(dtos)
	},
}

// selectedDetailFields returns the detailDTO keys --fields selects, nil when the
// flag is absent. A name selects its whole detailSection, so `--fields related`
// means the same in both output modes: the line a person reads, and every key
// that line is rendered from.
func selectedDetailFields(cmd *cobra.Command) ([]string, error) {
	if !cmd.Flags().Changed("fields") {
		return nil, nil
	}
	known := detailFieldNames()
	var selected []string
	for _, name := range showFlags.fields {
		name = strings.TrimSpace(name)
		switch {
		case name == "":
			continue
		case !slices.Contains(known, name):
			return nil, &usageError{cmd: cmd, msg: fmt.Sprintf("unknown field %q for --fields (want: %s)", name, strings.Join(known, ", "))}
		case !flagJSON && slices.Contains(jsonOnlyDetailFields, name):
			return nil, &usageError{cmd: cmd, msg: fmt.Sprintf("field %q has no line in human output; add --json", name)}
		}
		selected = append(selected, name)
		for _, sec := range detailSections {
			if slices.Contains(sec.fields, name) {
				selected = append(selected, sec.fields...)
			}
		}
	}
	if selected == nil {
		return nil, &usageError{cmd: cmd, msg: "--fields needs at least one field name"}
	}
	return selected, nil
}

// detailFieldNames lists the top-level JSON keys of detailDTO in the order show
// prints them: the names --fields accepts.
func detailFieldNames() []string {
	var names []string
	for _, f := range reflect.VisibleFields(reflect.TypeFor[detailDTO]()) {
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != "" {
			names = append(names, name)
		}
	}
	return names
}

type selectedField struct {
	key   string
	value json.RawMessage
}

// fieldSelection is a detailDTO reduced to the keys a caller named. It marshals
// them in detailDTO order, so a selection reads like the full object with keys
// removed.
type fieldSelection []selectedField

func (sel fieldSelection) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range sel {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := json.Marshal(f.key)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(f.value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// selectFields keeps `id` and the selected keys of dto that are present in its
// JSON form; an empty optional field stays omitted, as it is in the full object.
func selectFields(dto detailDTO, selected []string) (fieldSelection, error) {
	// The two unbounded fields are dropped before the encode, not after it.
	if !slices.Contains(selected, "description") {
		dto.Description = ""
	}
	if !slices.Contains(selected, "comments") {
		dto.Comments = nil
	}
	var raw bytes.Buffer
	if err := newJSONEncoder(&raw).Encode(dto); err != nil {
		return nil, err
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw.Bytes(), &values); err != nil {
		return nil, err
	}
	var sel fieldSelection
	for _, name := range detailFieldNames() {
		if v, ok := values[name]; ok && (name == "id" || slices.Contains(selected, name)) {
			sel = append(sel, selectedField{name, v})
		}
	}
	return sel, nil
}

func init() {
	showCmd.Flags().StringSliceVar(&showFlags.fields, "fields", nil, "print only these fields, comma-separated (the keys of show --json: status, description, comments, ...)")
	rootCmd.AddCommand(showCmd)
}
