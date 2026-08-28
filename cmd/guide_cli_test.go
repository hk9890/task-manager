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

// guide_cli_test.go — L4 CLI tests for `taskmgr guide`: topic selection, the
// sections packages contribute (HOOK-SPEC §3.7), and the exit-code contract.
//
// The exit code is the subject here, not an incidental: a caller pastes this
// command's output into its own instructions, and a non-zero exit is what it
// reads as "abort". These run against the real binary because that is where an
// exit code exists.
package cmd_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// initStoreWithGuidePackage creates a store whose package contributes one guide
// fragment, and returns the project root.
func initStoreWithGuidePackage(t *testing.T, prefix, fragment string) string {
	t.Helper()
	root := t.TempDir()
	if _, err := tasks.Init(root, prefix); err != nil {
		t.Fatalf("Init: %v", err)
	}
	pkg := filepath.Join(root, ".tasks", "packages", "policy")
	if err := os.MkdirAll(filepath.Join(pkg, "guide"), 0o755); err != nil {
		t.Fatalf("mkdir package: %v", err)
	}
	manifest := "version: 1\nguide:\n    - id: bodies\n      file: ./guide/bodies.md\n"
	if err := os.WriteFile(filepath.Join(pkg, tasks.PackageManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if fragment != "" {
		if err := os.WriteFile(filepath.Join(pkg, "guide", "bodies.md"), []byte(fragment), 0o644); err != nil {
			t.Fatalf("write fragment: %v", err)
		}
	}
	cfg := filepath.Join(root, ".tasks", "config.yaml")
	body := fmt.Sprintf("prefix: %s\nuse:\n    - path: packages/policy\n", prefix)
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root
}

func TestL4_Guide_PrintsPackageSectionsWithTheirProvenance(t *testing.T) {
	root := initStoreWithGuidePackage(t, "gd", "every bug names its repro.\n")

	stdout, stderr, code := taskmgr(t, root, "guide")
	if code != 0 {
		t.Fatalf("guide: exit %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "every bug names its repro") {
		t.Error("the package's fragment must be part of the default guide")
	}
	// A reader has to be able to tell a store convention from a rule of the
	// tool, and the heading is the only thing that says which it is holding.
	if !strings.Contains(stdout, "pkg:policy:bodies") || !strings.Contains(stdout, "From package policy") {
		t.Errorf("the fragment must be printed under a heading naming its package:\n%s", stdout)
	}
}

func TestL4_Guide_TopicSelectsOneSection(t *testing.T) {
	root := initStoreWithGuidePackage(t, "gd", "every bug names its repro.\n")

	stdout, _, code := taskmgr(t, root, "guide", "query")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stdout, "Finding work with filters") {
		t.Error("the named topic must be printed")
	}
	if strings.Contains(stdout, "The core loop") {
		t.Error("naming one topic must not print the others — that is the point of naming it")
	}
}

func TestL4_Guide_PackagesTopicSelectsOnlyThePackageSections(t *testing.T) {
	root := initStoreWithGuidePackage(t, "gd", "every bug names its repro.\n")

	stdout, _, code := taskmgr(t, root, "guide", "packages")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stdout, "every bug names its repro") {
		t.Error("the package fragment must be printed")
	}
	if strings.Contains(stdout, "## The model") {
		t.Error("the packages topic must not print core sections")
	}
}

func TestL4_GuideList_NamesCoreAndPackageTopics(t *testing.T) {
	root := initStoreWithGuidePackage(t, "gd", "body rules\n")

	stdout, _, code := taskmgr(t, root, "--json", "guide", "--list")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	var rows []struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Package string `json:"package"`
	}
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("guide --list --json is not an array: %v\n%s", err, stdout)
	}
	var sawCore, sawPackage bool
	for _, r := range rows {
		if r.ID == "model" && r.Kind == "core" {
			sawCore = true
		}
		if r.ID == "pkg:policy:bodies" && r.Kind == "package" && r.Package == "policy" {
			sawPackage = true
		}
	}
	if !sawCore || !sawPackage {
		t.Errorf("the roster must name both kinds of topic: %v", rows)
	}
}

// An unreadable fragment is reported inside the output and the command still
// succeeds: a guide is not a gate, and a caller that pastes this output must
// never be left with nothing because one document is missing.
func TestL4_Guide_AnUnreadableFragmentDoesNotFailTheCommand(t *testing.T) {
	root := initStoreWithGuidePackage(t, "gd", "") // manifest names a file that is not there

	stdout, stderr, code := taskmgr(t, root, "guide")
	if code != 0 {
		t.Fatalf("guide must exit 0 with an unreadable fragment: exit %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "could not be read") {
		t.Errorf("the missing fragment must be reported in the output:\n%s", stdout)
	}
}

// A store that will not resolve is the ordinary case for this command — an agent
// runs the guide before it knows where it is standing.
func TestL4_Guide_Human(t *testing.T) {
	root := t.TempDir() // no store required; guide must work anywhere.
	stdout, stderr, code := taskmgr(t, root, "guide")
	if code != 0 {
		t.Fatalf("guide exit=%d stderr=%q", code, stderr)
	}
	// It must teach the model, the loop, and — above all — how to get more info.
	for _, want := range []string{
		"The core loop",
		"taskmgr create --title",
		"Finding work with filters",
		"Get more information",
		"taskmgr commands",
		"taskmgr <command> --help",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("guide output missing %q\n---\n%s", want, stdout)
		}
	}
}

func TestL4_Guide_JSON(t *testing.T) {
	root := t.TempDir()
	stdout, stderr, code := taskmgr(t, root, "--json", "guide")
	if code != 0 {
		t.Fatalf("guide --json exit=%d stderr=%q", code, stderr)
	}
	var obj map[string]string
	if err := json.Unmarshal([]byte(stdout), &obj); err != nil {
		t.Fatalf("guide --json is not valid JSON: %v\n---\n%s", err, stdout)
	}
	if strings.TrimSpace(obj["guide"]) == "" {
		t.Errorf("guide --json: 'guide' field is empty; got keys %v", keysOf(obj))
	}
	if !strings.Contains(obj["guide"], "The core loop") {
		t.Errorf("guide --json: 'guide' text missing core content")
	}
}

func keysOf(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// Whether a package is installed is a property of the machine, so naming one
// that is not here is reported rather than refused — otherwise a colleague who
// has not installed it yet gets a failed command instead of a guide.
func TestL4_Guide_AnAbsentPackageTopicIsReportedNotRefused(t *testing.T) {
	root := initStoreWithGuidePackage(t, "gd", "body rules\n")

	stdout, _, code := taskmgr(t, root, "guide", "pkg:absent:bodies")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stdout, "not available here") {
		t.Errorf("an absent package topic must say so:\n%s", stdout)
	}
}

// A core topic that does not exist can never be made to exist by installing
// anything, so it is only ever a mistake in the caller.
func TestL4_Guide_AnUnknownCoreTopicIsAnError(t *testing.T) {
	root := initStoreWithGuidePackage(t, "gd", "body rules\n")

	_, stderr, code := taskmgr(t, root, "guide", "nosuchtopic")
	if code == 0 {
		t.Fatal("an unknown core topic must fail")
	}
	if !strings.Contains(stderr, "unknown guide topic") {
		t.Errorf("stderr = %q", stderr)
	}
}
