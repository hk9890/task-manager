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
// section and one overview fragment, and returns the project root.
//
// An empty fragment leaves the file the manifest names absent, which is how the
// unreadable-fragment cases are built.
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
	manifest := "version: 1\noverview: ./guide/overview.md\nguide:\n    - id: bodies\n      file: ./guide/bodies.md\n"
	if err := os.WriteFile(filepath.Join(pkg, tasks.PackageManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if fragment != "" {
		if err := os.WriteFile(filepath.Join(pkg, "guide", "bodies.md"), []byte(fragment), 0o644); err != nil {
			t.Fatalf("write fragment: %v", err)
		}
		if err := os.WriteFile(filepath.Join(pkg, "guide", "overview.md"), []byte("bodies need four sections.\n"), 0o644); err != nil {
			t.Fatalf("write overview: %v", err)
		}
	}
	cfg := filepath.Join(root, ".tasks", "config.yaml")
	body := fmt.Sprintf("prefix: %s\nuse:\n    - path: packages/policy\n", prefix)
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root
}

// The overview is what a caller injects, so a package's `overview:` fragment has
// to reach it — that is the only way a store states an expectation to someone who
// has not asked a question yet.
func TestL4_Guide_OverviewCarriesThePackageOverviewFragment(t *testing.T) {
	root := initStoreWithGuidePackage(t, "gd", "every bug names its repro.\n")

	stdout, stderr, code := taskmgr(t, root, "guide")
	if code != 0 {
		t.Fatalf("guide: exit %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "bodies need four sections") {
		t.Errorf("the package's overview fragment must be in the overview:\n%s", stdout)
	}
	// A reader has to be able to tell a store convention from a rule of the tool.
	if !strings.Contains(stdout, "What policy expects of this store") {
		t.Errorf("the fragment must sit under a heading naming its package:\n%s", stdout)
	}
	// The section itself stays behind its command. Putting it in the overview is
	// what the overview exists to avoid.
	if strings.Contains(stdout, "every bug names its repro") {
		t.Error("a package's full section must not be inlined into the overview")
	}
	// A fragment the package owns outright is a job this store has and the tool
	// does not, so the job list is where it has to appear: a topic no line names
	// is a topic nothing can reach.
	if !strings.Contains(stdout, "taskmgr guide pkg:policy:bodies") {
		t.Errorf("the overview must name a package's own topic:\n%s", stdout)
	}
}

// The job list is generated from the section table, so a job cannot be added
// without the overview naming it — the failure that would make fetch-on-demand a
// trap, since a caller can only fetch what it was told exists.
//
// `packages` is deliberately not in it: it is a way of asking for every package
// fragment at once, not a job anyone sets out to do. `--list` is the complete
// roster, and the overview names that instead.
func TestL4_Guide_OverviewNamesEveryJob(t *testing.T) {
	root := initStoreWithGuidePackage(t, "gd", "body rules\n")

	overview, _, code := taskmgr(t, root, "guide")
	if code != 0 {
		t.Fatalf("guide: exit %d", code)
	}
	listOut, _, code := taskmgr(t, root, "--json", "guide", "--list")
	if code != 0 {
		t.Fatalf("guide --list: exit %d", code)
	}
	var rows []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(listOut), &rows); err != nil {
		t.Fatalf("guide --list --json: %v", err)
	}
	for _, r := range rows {
		if r.Kind != "core" || r.ID == "packages" {
			continue
		}
		if !strings.Contains(overview, "taskmgr guide "+r.ID) {
			t.Errorf("the overview does not name job %q, so a caller cannot fetch it:\n%s", r.ID, overview)
		}
	}
	if !strings.Contains(overview, "taskmgr guide --list") {
		t.Errorf("the overview must name the roster that holds what it does not:\n%s", overview)
	}
}

func TestL4_Guide_TopicSelectsOneSection(t *testing.T) {
	root := initStoreWithGuidePackage(t, "gd", "every bug names its repro.\n")

	stdout, _, code := taskmgr(t, root, "guide", "finding")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stdout, "## Finding work") {
		t.Error("the named topic must be printed")
	}
	if strings.Contains(stdout, "## Filing an issue") {
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
	if !strings.Contains(stdout, "pkg:policy:bodies") || !strings.Contains(stdout, "From package policy") {
		t.Errorf("each fragment must be printed under a heading naming its package:\n%s", stdout)
	}
	if strings.Contains(stdout, "## Filing an issue") {
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
		if r.ID == "filing" && r.Kind == "core" {
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

// initStoreWithPlacedFragment creates a store whose package contributes one guide
// fragment placed into the built-in topic `into`, and returns the project root.
// It declares a hook as well, so a test can check that a placement the guide
// cannot honour leaves the package's gate running.
func initStoreWithPlacedFragment(t *testing.T, into, fragment string) string {
	t.Helper()
	root := t.TempDir()
	if _, err := tasks.Init(root, "pl"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	pkg := filepath.Join(root, ".tasks", "packages", "policy")
	if err := os.MkdirAll(filepath.Join(pkg, "guide"), 0o755); err != nil {
		t.Fatalf("mkdir package: %v", err)
	}
	manifest := fmt.Sprintf(
		"version: 1\nguide:\n    - id: rules\n      into: %s\n      file: ./guide/rules.md\nhooks:\n    - id: gate\n      event: pre-create\n      run: [\"/bin/true\"]\n",
		into)
	if err := os.WriteFile(filepath.Join(pkg, tasks.PackageManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "guide", "rules.md"), []byte(fragment), 0o644); err != nil {
		t.Fatalf("write fragment: %v", err)
	}
	cfg := filepath.Join(root, ".tasks", "config.yaml")
	if err := os.WriteFile(cfg, []byte("prefix: pl\nuse:\n    - path: packages/policy\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root
}

// A job is one command, and what it prints has to be sufficient. A package's
// rules for that job therefore arrive with it — the caller never learns that a
// second topic existed, which is the round trip the placement removes.
func TestL4_Guide_APlacedFragmentPrintsInsideItsJob(t *testing.T) {
	root := initStoreWithPlacedFragment(t, "filing", "every bug names its repro.\n")

	stdout, stderr, code := taskmgr(t, root, "guide", "filing")
	if code != 0 {
		t.Fatalf("guide filing: exit %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "## Filing an issue") {
		t.Errorf("the job's own text must still be printed:\n%s", stdout)
	}
	if !strings.Contains(stdout, "every bug names its repro") {
		t.Errorf("the placed fragment must print inside the job:\n%s", stdout)
	}
	// A reader has to be able to tell a store convention from a rule of the tool,
	// and inside a built-in topic that distinction is the only thing separating
	// them.
	if !strings.Contains(stdout, "From package policy") {
		t.Errorf("a placed fragment must carry its provenance:\n%s", stdout)
	}
	// Placement adds a way to reach it; it does not remove one.
	byID, _, code := taskmgr(t, root, "guide", "pkg:policy:rules")
	if code != 0 {
		t.Fatalf("guide pkg:policy:rules: exit %d", code)
	}
	if !strings.Contains(byID, "every bug names its repro") {
		t.Errorf("a placed fragment must stay addressable by its own id:\n%s", byID)
	}
	// The job line says the store adds rules, so a caller choosing a job knows
	// before it spends the command.
	overview, _, _ := taskmgr(t, root, "guide")
	if !strings.Contains(overview, "this store adds rules") {
		t.Errorf("the job list must mark a job a package adds rules to:\n%s", overview)
	}
}

// A placement naming a topic this binary does not have is a documentation
// mismatch, and it must stay one: reported, exit 0, fragment still reachable, and
// above all the package's hooks still running. Failing the package here would
// turn a renamed topic into refused writes for every store that uses it.
func TestL4_Guide_APlacementIntoNothingIsReportedNotFatal(t *testing.T) {
	root := initStoreWithPlacedFragment(t, "nosuchtopic", "every bug names its repro.\n")

	stdout, stderr, code := taskmgr(t, root, "guide")
	if code != 0 {
		t.Fatalf("guide must exit 0 with an unplaceable fragment: exit %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "nosuchtopic") || !strings.Contains(stdout, "not a topic here") {
		t.Errorf("an unplaceable fragment must be reported in the output:\n%s", stdout)
	}
	byID, _, code := taskmgr(t, root, "guide", "pkg:policy:rules")
	if code != 0 || !strings.Contains(byID, "every bug names its repro") {
		t.Errorf("an unplaceable fragment must stay reachable by its own id: exit %d\n%s", code, byID)
	}
	// The gate is the half that must not be affected by any of this.
	hooks, _, code := taskmgr(t, root, "hook", "list")
	if code != 0 {
		t.Fatalf("hook list: exit %d", code)
	}
	if !strings.Contains(hooks, "pkg:policy:gate") {
		t.Errorf("the package's hooks must still run when its guide placement fails:\n%s", hooks)
	}
}

// The names that get guessed are the ones this tool uses elsewhere: an agent
// looking for what to work on reached for "ready", which is a command and a query
// field but never a topic.
func TestL4_Guide_AnUnknownTopicSuggestsTheJobThatHoldsIt(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct{ typed, want string }{
		{"ready", "finding"},
		{"model", "filing"},
		{"body", "filing"},
		{"output", "scripting"},
		{"fil", "filing"},
	} {
		_, stderr, code := taskmgr(t, root, "guide", tc.typed)
		if code == 0 {
			t.Errorf("guide %q must fail", tc.typed)
			continue
		}
		if !strings.Contains(stderr, fmt.Sprintf("did you mean %q?", tc.want)) {
			t.Errorf("guide %q: stderr must suggest %q, got %q", tc.typed, tc.want, stderr)
		}
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
	// The overview is a router: which job maps to which command, and where the
	// rest of the surface is. It carries no section text at all.
	for _, want := range []string{
		"taskmgr guide filing",
		"taskmgr guide finding",
		"taskmgr guide progress",
		"taskmgr guide scripting",
		"taskmgr commands",
		"taskmgr <command> --help",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("guide output missing %q\n---\n%s", want, stdout)
		}
	}
	// The router pays for itself only by staying small: it is the one part every
	// caller receives on every run, whatever it came to do.
	if n := len(stdout); n > 1024 {
		t.Errorf("the overview is %d bytes; it is paid on every invocation, so it stays under 1024\n---\n%s", n, stdout)
	}
	for _, unwanted := range []string{"## Filing an issue", "## Finding work", "--description-file"} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("the overview must carry no section text, but holds %q", unwanted)
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
	if !strings.Contains(obj["guide"], "taskmgr guide filing") {
		t.Errorf("guide --json: 'guide' text is not the overview")
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
