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

// task_writing_package_cli_test.go — L4 tests for the package this repository
// ships, task-manager-packages/task-writing.
//
// It is a shipped artifact with no other gate: a typo in its manifest, a hook
// script that lost its execute bit, or a fragment renamed out from under the
// manifest would all reach a user before anyone noticed. Installing it the way
// the README says and driving the real binary is what catches that.
package cmd_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// shippedPackage is the package directory in this repository, relative to cmd/.
const shippedPackage = "../task-manager-packages/task-writing"

// installShippedPackage creates a store and installs the shipped package into it
// the way the package's README says to, returning the project root.
func installShippedPackage(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(shippedPackage, tasks.PackageManifestName)); err != nil {
		t.Fatalf("the shipped package is not where this test expects it: %v", err)
	}
	root := t.TempDir()
	if _, err := tasks.Init(root, "tw"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	dest := filepath.Join(root, ".tasks", "packages")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir packages: %v", err)
	}
	// cp -r, so the execute bit on the hook script is carried the way an actual
	// install carries it. A copy that dropped it would make every hook a "not
	// executable" error, which is the failure this test exists to catch.
	if out, err := exec.Command("cp", "-r", shippedPackage, dest).CombinedOutput(); err != nil {
		t.Fatalf("copy package: %v\n%s", err, out)
	}
	cfg := filepath.Join(root, ".tasks", "config.yaml")
	body := "prefix: tw\nuse:\n    - path: packages/task-writing\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root
}

func TestL4_TaskWritingPackage_LoadsAndContributesBothHalves(t *testing.T) {
	root := installShippedPackage(t)

	stdout, stderr, code := taskmgr(t, root, "package", "list")
	if code != 0 {
		t.Fatalf("package list: exit %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "task-writing") || !strings.Contains(stdout, "ok") {
		t.Fatalf("the shipped package must load:\n%s", stdout)
	}

	guideOut, _, code := taskmgr(t, root, "guide", "pkg:task-writing:bodies")
	if code != 0 {
		t.Fatalf("guide: exit %d", code)
	}
	if !strings.Contains(guideOut, "The four sections") {
		t.Errorf("the package's guide fragment must be reachable by its topic:\n%s", guideOut)
	}
}

func TestL4_TaskWritingPackage_RefusesABodylessIssue(t *testing.T) {
	root := installShippedPackage(t)

	stdout, stderr, code := taskmgr(t, root, "create", "--title", "no body", "--type", "bug")
	if code == 0 {
		t.Fatalf("a bug with no body must be refused\nstdout: %s", stdout)
	}
	// The refusal names the gate and the sections, so the caller can fix the
	// body without opening the package.
	if !strings.Contains(stderr, "pkg:task-writing:body-sections") {
		t.Errorf("the denial must name the effective hook id:\n%s", stderr)
	}
	for _, want := range []string{"## Context", "## Acceptance criteria"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the denial must name the missing section %q:\n%s", want, stderr)
		}
	}
}

func TestL4_TaskWritingPackage_AcceptsAWellFormedBody(t *testing.T) {
	root := installShippedPackage(t)
	body := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(body, []byte(`## Context

cmd/guide.go, renderGuideTopic.

## Problem

A fragment prints without naming its package.

## Recommended action

Print a heading naming the package above each fragment.

## Acceptance criteria

- [ ] taskmgr guide packages prints a heading naming the package
`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := taskmgr(t, root, "create", "--title", "name the package",
		"--type", "bug", "--description-file", body)
	if code != 0 {
		t.Fatalf("a well-formed body must pass the gate: exit %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Created") {
		t.Errorf("stdout = %q", stdout)
	}
}

// The criteria checklist is the one part of the body a structural gate can
// actually check beyond a heading, so it has its own case.
func TestL4_TaskWritingPackage_RefusesCriteriaWithNoChecklist(t *testing.T) {
	root := installShippedPackage(t)
	body := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(body, []byte(`## Context

somewhere.

## Problem

something.

## Recommended action

do it.

## Acceptance criteria

It works correctly.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := taskmgr(t, root, "create", "--title", "prose criteria",
		"--type", "bug", "--description-file", body)
	if code == 0 {
		t.Fatal("acceptance criteria with no checklist item must be refused")
	}
	if !strings.Contains(stderr, "no checklist item") {
		t.Errorf("stderr = %q", stderr)
	}
}

// A doc holds a document, not a path through work, so no gate applies to it.
func TestL4_TaskWritingPackage_DoesNotGateADoc(t *testing.T) {
	root := installShippedPackage(t)

	_, stderr, code := taskmgr(t, root, "create", "--title", "design page",
		"--type", "doc", "--label", "kind:design", "--description", "just prose")
	if code != 0 {
		t.Fatalf("a doc must not be gated: exit %d\nstderr: %s", code, stderr)
	}
}
