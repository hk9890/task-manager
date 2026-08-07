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

//go:build integration

// L4 CLI tests for documents and body overflow, driven through the built binary:
// creating a doc from a file, the truncated human `show` versus the complete
// JSON one, and docs staying out of `ready` while remaining listable and
// searchable.
package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// bigHTML builds a page comfortably over the inline cap, with a marker only
// findable inside the body.
func bigHTML(marker string) string {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html><head><title>Design</title></head><body>\n")
	b.WriteString("<p>" + marker + "</p>\n")
	for b.Len() < tasks.MaxInlineBody+4096 {
		b.WriteString("<div class=\"row\">filler content for the design page</div>\n")
	}
	b.WriteString("</body></html>\n")
	return b.String()
}

func initCLIStore(t *testing.T, prefix string) string {
	t.Helper()
	root := t.TempDir()
	if _, err := tasks.Init(root, prefix); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return root
}

func writeFileFor(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "page.html")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// createDoc creates a doc from a file and returns its ID.
func createDoc(t *testing.T, root, title, path string, extra ...string) string {
	t.Helper()
	args := append([]string{"--json", "create", "--title", title, "--type", "doc",
		"--description-file", path}, extra...)
	out, stderr, code := taskmgr(t, root, args...)
	if code != 0 {
		t.Fatalf("create doc: exit %d, stderr: %s", code, stderr)
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse create JSON %q: %v", out, err)
	}
	return res.ID
}

// TestL4_Doc_CreateFromFileOverflows is the whole user-facing story in one test:
// a generated HTML page goes in through an existing flag, no new command, and
// lands in the sidecar rather than the hot directory.
func TestL4_Doc_CreateFromFileOverflows(t *testing.T) {
	root := initCLIStore(t, "l4d")
	page := bigHTML("auth-redesign-marker")
	id := createDoc(t, root, "Auth redesign", writeFileFor(t, page))

	dir := filepath.Join(root, tasks.DataDirName)
	md, err := os.ReadFile(filepath.Join(dir, id+tasks.FileExt)) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	if strings.Contains(string(md), "auth-redesign-marker") {
		t.Fatal("the page must not be stored in the hot .md")
	}
	if !strings.Contains(string(md), "type: doc") {
		t.Fatalf("expected type: doc, got:\n%s", md)
	}
	content, err := os.ReadFile(filepath.Join(dir, "content", id)) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read content sidecar: %v", err)
	}
	if !strings.Contains(string(content), "auth-redesign-marker") {
		t.Fatal("the page must be in the content sidecar")
	}
}

// TestL4_Show_TruncatesHumanKeepsJSON: the terminal gets a bounded excerpt and a
// pointer; --json gets every byte, because a script asked for the whole thing.
func TestL4_Show_TruncatesHumanKeepsJSON(t *testing.T) {
	root := initCLIStore(t, "l4s")
	page := bigHTML("show-marker")
	id := createDoc(t, root, "Auth redesign", writeFileFor(t, page))

	human, stderr, code := taskmgr(t, root, "show", id)
	if code != 0 {
		t.Fatalf("show: exit %d, stderr: %s", code, stderr)
	}
	if len(human) >= len(page) {
		t.Fatalf("human show must be truncated: got %d bytes for a %d-byte body", len(human), len(page))
	}
	if !strings.Contains(human, "body is") || !strings.Contains(human, "content/"+id) {
		t.Fatalf("truncation notice must point at the content file, got:\n%s", human)
	}

	jsonOut, stderr, code := taskmgr(t, root, "--json", "show", id)
	if code != 0 {
		t.Fatalf("show --json: exit %d, stderr: %s", code, stderr)
	}
	var d struct {
		Description  string `json:"description"`
		BodyExternal bool   `json:"body_external"`
		Type         string `json:"type"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &d); err != nil {
		t.Fatalf("parse show JSON: %v", err)
	}
	if d.Description != strings.TrimSpace(page) {
		t.Fatalf("JSON must carry the complete body: got %d bytes, want %d",
			len(d.Description), len(strings.TrimSpace(page)))
	}
	if !d.BodyExternal {
		t.Fatal("JSON must report body_external for an overflowed issue")
	}
	if d.Type != "doc" {
		t.Fatalf("type = %q, want doc", d.Type)
	}
}

// TestL4_Show_SmallBodyNotTruncated guards against the truncation notice leaking
// into ordinary output.
func TestL4_Show_SmallBodyNotTruncated(t *testing.T) {
	root := initCLIStore(t, "l4t")
	out, stderr, code := taskmgr(t, root, "--json", "create", "--title", "small",
		"--description", "a short body")
	if code != 0 {
		t.Fatalf("create: exit %d, stderr: %s", code, stderr)
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse: %v", err)
	}

	human, _, code := taskmgr(t, root, "show", res.ID)
	if code != 0 {
		t.Fatalf("show: exit %d", code)
	}
	if !strings.Contains(human, "a short body") {
		t.Fatal("a small body must be shown in full")
	}
	if strings.Contains(human, "body is") {
		t.Fatalf("no truncation notice for a small body, got:\n%s", human)
	}
}

// TestL4_Doc_NotReadyButListedAndSearchable is the work-view contract end to
// end: docs never appear as ready work, but stay ordinary issues everywhere else.
func TestL4_Doc_NotReadyButListedAndSearchable(t *testing.T) {
	root := initCLIStore(t, "l4r")
	docID := createDoc(t, root, "Auth redesign", writeFileFor(t, bigHTML("searchable-marker")))

	out, stderr, code := taskmgr(t, root, "--json", "create", "--title", "real work")
	if code != 0 {
		t.Fatalf("create task: exit %d, stderr: %s", code, stderr)
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse: %v", err)
	}
	taskID := res.ID

	ready, stderr, code := taskmgr(t, root, "--json", "ready")
	if code != 0 {
		t.Fatalf("ready: exit %d, stderr: %s", code, stderr)
	}
	if strings.Contains(ready, docID) {
		t.Fatalf("a doc must never be ready work, got:\n%s", ready)
	}
	if !strings.Contains(ready, taskID) {
		t.Fatalf("the real task must be ready, got:\n%s", ready)
	}

	list, _, code := taskmgr(t, root, "--json", "list")
	if code != 0 {
		t.Fatalf("list: exit %d", code)
	}
	if !strings.Contains(list, docID) {
		t.Fatalf("a doc must still be listed, got:\n%s", list)
	}

	// Searchable on content that exists only in the sidecar.
	found, stderr, code := taskmgr(t, root, "--json", "search", "searchable-marker")
	if code != 0 {
		t.Fatalf("search: exit %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(found, docID) {
		t.Fatalf("search must reach an overflowed body, got:\n%s", found)
	}

	// And the raw query language must give the identical answer — `text` is one
	// virtual field, not two search engines.
	viaQuery, _, code := taskmgr(t, root, "--json", "list", "-q", `text ~ "searchable-marker"`)
	if code != 0 {
		t.Fatalf("list -q: exit %d", code)
	}
	if !strings.Contains(viaQuery, docID) {
		t.Fatalf("list -q text must agree with search, got:\n%s", viaQuery)
	}
}

// TestL4_Comment_OversizedRejected: comments are bounded rather than overflowed,
// and the error says where large content belongs instead.
func TestL4_Comment_OversizedRejected(t *testing.T) {
	root := initCLIStore(t, "l4c")
	out, _, code := taskmgr(t, root, "--json", "create", "--title", "issue")
	if code != 0 {
		t.Fatalf("create: exit %d", code)
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse: %v", err)
	}

	big := filepath.Join(t.TempDir(), "huge.txt")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", tasks.MaxCommentBody+1)), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, stderr, code := taskmgr(t, root, "comment", "add", res.ID, "--file", big)
	if code == 0 {
		t.Fatal("an oversized comment must be rejected")
	}
	if !strings.Contains(stderr, "doc issue") {
		t.Fatalf("the error should point at docs for large content, got: %s", stderr)
	}
}
