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
		known := detailFieldNames()
		for _, f := range showFlags.fields {
			if !slices.Contains(known, f) {
				return &usageError{cmd: cmd, msg: fmt.Sprintf("unknown field %q for --fields (want: %s)", f, strings.Join(known, ", "))}
			}
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
				printDetail(d, showFlags.fields)
			}
			return nil
		}
		dtos := make([]any, len(details))
		for i, d := range details {
			dto := toDetailDTO(s.Name(), d)
			dtos[i] = dto
			if len(showFlags.fields) > 0 {
				if dtos[i], err = selectFields(dto, append([]string{"id"}, showFlags.fields...)); err != nil {
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
		fmt.Fprintf(&b, "%q:%s", f.key, f.value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// selectFields keeps the named keys of dto that are present in its JSON form; an
// empty optional field stays omitted, as it is in the full object.
func selectFields(dto detailDTO, keep []string) (fieldSelection, error) {
	var raw bytes.Buffer
	enc := json.NewEncoder(&raw)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(dto); err != nil {
		return nil, err
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw.Bytes(), &values); err != nil {
		return nil, err
	}
	var sel fieldSelection
	for _, name := range detailFieldNames() {
		if v, ok := values[name]; ok && slices.Contains(keep, name) {
			sel = append(sel, selectedField{name, v})
		}
	}
	return sel, nil
}

func init() {
	showCmd.Flags().StringSliceVar(&showFlags.fields, "fields", nil, "print only these fields, comma-separated (the keys of show --json: status, description, comments, ...)")
	rootCmd.AddCommand(showCmd)
}
