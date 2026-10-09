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
	"encoding/json"
	"strings"
	"testing"
)

func TestShow_SeveralIDs_PrintsBlocksInArgumentOrder(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root)
	b := createIssue(t, root)

	out, errOut, code := run(t, "--dir", root, "show", b, a)
	if code != 0 {
		t.Fatalf("show: exit %d, stderr %q", code, errOut)
	}
	first, second := strings.Index(out, b+"  "), strings.Index(out, "\n\n"+a+"  ")
	if first != 0 || second < 0 {
		t.Errorf("want the block of %s, a blank line, then the block of %s; got:\n%s", b, a, out)
	}
}

func TestShow_OneID_JSONIsAnObject(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root)

	out, errOut, code := run(t, "--dir", root, "--json", "show", a)
	if code != 0 {
		t.Fatalf("show: exit %d, stderr %q", code, errOut)
	}
	var dto struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("one ID must stay a JSON object: %v\n%s", err, out)
	}
	if dto.ID != a {
		t.Errorf("id = %q, want %q", dto.ID, a)
	}
}

func TestShow_SeveralIDs_JSONIsAnArrayInArgumentOrder(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root)
	b := createIssue(t, root)

	out, errOut, code := run(t, "--dir", root, "--json", "show", b, a)
	if code != 0 {
		t.Fatalf("show: exit %d, stderr %q", code, errOut)
	}
	var dtos []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &dtos); err != nil {
		t.Fatalf("several IDs must print a JSON array: %v\n%s", err, out)
	}
	if len(dtos) != 2 || dtos[0].ID != b || dtos[1].ID != a {
		t.Errorf("got %+v, want [%s %s]", dtos, b, a)
	}
}

func TestShow_SeveralIDs_OneMissing_PrintsNothing(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root)

	for _, jsonFlag := range []bool{false, true} {
		args := []string{"--dir", root, "show", a, "tst-9999"}
		if jsonFlag {
			args = append([]string{"--json"}, args...)
		}
		out, errOut, code := run(t, args...)
		if code != 1 {
			t.Errorf("json=%v: exit %d, want 1", jsonFlag, code)
		}
		if out != "" {
			t.Errorf("json=%v: a missing ID must leave stdout empty; got %q", jsonFlag, out)
		}
		if !strings.Contains(errOut, "issue not found: tst-9999") {
			t.Errorf("json=%v: stderr %q does not name the missing ID", jsonFlag, errOut)
		}
	}
}

func TestShow_Fields_JSONKeepsIDAndNamedKeysInDTOOrder(t *testing.T) {
	root := newStore(t)
	blocker := createIssue(t, root)
	a := createIssue(t, root, "--blocked-by", blocker, "--description", "<body>")

	out, errOut, code := run(t, "--dir", root, "--json", "show", a, "--fields", "blocked_by,status")
	if code != 0 {
		t.Fatalf("show: exit %d, stderr %q", code, errOut)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if len(got) != 4 || got["id"] == nil || got["status"] == nil || got["blocked_by"] == nil || got["blocked_by_refs"] == nil {
		t.Errorf("want exactly id, status, and the two keys of the blocked-by line; got:\n%s", out)
	}
	if id, status, blocked := strings.Index(out, `"id"`), strings.Index(out, `"status"`), strings.Index(out, `"blocked_by"`); id >= status || status >= blocked {
		t.Errorf("keys must keep the order of the full object (id, status, blocked_by); got:\n%s", out)
	}
}

func TestShow_Fields_EmptyOptionalFieldStaysOmitted(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root)

	out, _, code := run(t, "--dir", root, "--json", "show", a, "--fields", "blocked_by")
	if code != 0 {
		t.Fatalf("show: exit %d", code)
	}
	if strings.Contains(out, "blocked_by") {
		t.Errorf("an issue with no blockers must not gain a blocked_by key; got:\n%s", out)
	}
}

func TestShow_Fields_JSONDoesNotEscapeHTML(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root, "--description", "a <b> & c")

	out, _, code := run(t, "--dir", root, "--json", "show", a, "--fields", "description")
	if code != 0 {
		t.Fatalf("show: exit %d", code)
	}
	if !strings.Contains(out, "a <b> & c") {
		t.Errorf("a selected field must print like the full object, HTML unescaped; got:\n%s", out)
	}
}

func TestShow_Fields_SeveralIDs_SelectsInEachObject(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root)
	b := createIssue(t, root)

	out, _, code := run(t, "--dir", root, "--json", "show", a, b, "--fields", "status")
	if code != 0 {
		t.Fatalf("show: exit %d", code)
	}
	var got []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if len(got) != 2 || len(got[0]) != 2 || len(got[1]) != 2 {
		t.Errorf("want two objects of id and status; got:\n%s", out)
	}
}

func TestShow_Fields_NamedDescription_PrintsCompleteBody(t *testing.T) {
	root := newStore(t)
	body := strings.Repeat("a", 10000)
	a := createIssue(t, root, "--description", body)

	out, _, code := run(t, "--dir", root, "show", a, "--fields", "description")
	if code != 0 {
		t.Fatalf("show: exit %d", code)
	}
	if !strings.Contains(out, body) || strings.Contains(out, "body is") {
		t.Errorf("a description named in --fields must print complete and without a notice; got %d bytes", len(out))
	}
	if strings.Contains(out, "status:") {
		t.Errorf("only the named field prints below the header; got:\n%.200s", out)
	}
}

func TestShow_Fields_Comments_OmitsDescription(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root, "--description", "the-body")
	if _, errOut, code := run(t, "--dir", root, "comment", "add", a, "the-comment"); code != 0 {
		t.Fatalf("comment add: exit %d, stderr %q", code, errOut)
	}

	out, _, code := run(t, "--dir", root, "show", a, "--fields", "comments")
	if code != 0 {
		t.Fatalf("show: exit %d", code)
	}
	if !strings.Contains(out, "the-comment") || strings.Contains(out, "the-body") {
		t.Errorf("want the comments and no description; got:\n%s", out)
	}
}

func TestShow_Fields_UnknownName_IsMisuseListingTheNames(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root)

	out, errOut, code := run(t, "--dir", root, "show", a, "--fields", "nope")
	if code != 1 || out != "" {
		t.Fatalf("exit %d, stdout %q; want 1 and empty", code, out)
	}
	for _, want := range []string{`unknown field "nope"`, "description", "blocked_by_refs", "usage:"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
}

// `related` is stored on one side and derived on the other. Naming it must show
// the link from the side that stores nothing, in both output modes.
func TestShow_Fields_Related_ShowsADerivedLinkInBothModes(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root)
	b := createIssue(t, root, "--related", a)

	human, _, code := run(t, "--dir", root, "show", a, "--fields", "related")
	if code != 0 || !strings.Contains(human, b) {
		t.Errorf("human output of the derived side lacks %s (exit %d):\n%s", b, code, human)
	}
	jsonOut, _, code := run(t, "--dir", root, "--json", "show", a, "--fields", "related")
	if code != 0 || !strings.Contains(jsonOut, b) {
		t.Errorf("JSON output of the derived side lacks %s (exit %d):\n%s", b, code, jsonOut)
	}
}

func TestShow_Fields_OneKeyOfASharedLine_PrintsTheLine(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root, "--priority", "1")

	out, _, code := run(t, "--dir", root, "show", a, "--fields", "priority")
	if code != 0 || !strings.Contains(out, "priority: P1") || strings.Contains(out, "status:") {
		t.Errorf("want the type/priority line and no other (exit %d):\n%s", code, out)
	}
}

func TestShow_Fields_NamesAreTrimmedAndEmptyOnesDropped(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root)

	out, errOut, code := run(t, "--dir", root, "show", a, "--fields", "status, created,")
	if code != 0 || !strings.Contains(out, "status:") || !strings.Contains(out, "created:") {
		t.Errorf("exit %d, stderr %q, stdout:\n%s", code, errOut, out)
	}
}

// An empty value is what `--fields "$F"` gives with $F unset. Printing everything
// would be the opposite of what the caller asked for.
func TestShow_Fields_Empty_IsMisuse(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root)

	for _, value := range []string{"", " , "} {
		out, errOut, code := run(t, "--dir", root, "--json", "show", a, "--fields", value)
		if code != 1 || out != "" || !strings.Contains(errOut, "--fields needs at least one field name") {
			t.Errorf("--fields %q: exit %d, stdout %q, stderr %q", value, code, out, errOut)
		}
	}
}

func TestShow_Fields_KeyWithNoHumanLine_IsRefusedWithoutJSON(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root, "--description", strings.Repeat("a", 10000))

	for _, name := range jsonOnlyDetailFields {
		out, errOut, code := run(t, "--dir", root, "show", a, "--fields", name)
		if code != 1 || out != "" || !strings.Contains(errOut, "add --json") {
			t.Errorf("--fields %s: exit %d, stdout %d bytes, stderr %q", name, code, len(out), errOut)
		}
		if _, _, code := run(t, "--dir", root, "--json", "show", a, "--fields", name); code != 0 {
			t.Errorf("--json --fields %s: exit %d, want 0", name, code)
		}
	}
}

func TestShow_Fields_BodyExternal_DoesNotCarryTheBody(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root, "--description", "the-body")

	out, _, code := run(t, "--dir", root, "--json", "show", a, "--fields", "body_external")
	if code != 0 || strings.Contains(out, "the-body") {
		t.Errorf("exit %d; body_external must not select the description:\n%s", code, out)
	}
}

func TestShow_TruncationNotice_NamesFieldsDescription(t *testing.T) {
	root := newStore(t)
	a := createIssue(t, root, "--description", strings.Repeat("a", showBodyLimit+1))

	out, _, _ := run(t, "--dir", root, "show", a)
	if !strings.Contains(out, "--fields description") {
		t.Errorf("the notice must say how to get the full body:\n%.300s", out[max(0, len(out)-300):])
	}
}

// A detailDTO field no section renders would be accepted by --fields and print
// nothing in human output, with no test noticing.
func TestDetailSections_RenderEveryDetailField(t *testing.T) {
	rendered := map[string]bool{"id": true, "title": true} // the header line
	for _, f := range jsonOnlyDetailFields {
		rendered[f] = true
	}
	for _, sec := range detailSections {
		for _, f := range sec.fields {
			rendered[f] = true
		}
	}
	for _, name := range detailFieldNames() {
		if !rendered[name] {
			t.Errorf("detailDTO field %q has no detailSection", name)
		}
	}
}
