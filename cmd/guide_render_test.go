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

// L4, in-process: how `taskmgr guide` renders a package fragment that was cut at
// its cap (HOOK-SPEC §3.7). The subject is what the command printed, so it runs
// through cmd.Run against a temp store.
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// storeWithOverview builds a store whose one package contributes an overview
// fragment of the given text, and returns the project root.
func storeWithOverview(t *testing.T, overview string) string {
	t.Helper()
	root := t.TempDir()
	if _, err := tasks.Init(root, "tst"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	pkg := filepath.Join(root, ".tasks", "packages", "policy")
	if err := os.MkdirAll(filepath.Join(pkg, "guide"), 0o755); err != nil {
		t.Fatalf("mkdir package: %v", err)
	}
	manifest := "version: 1\noverview: ./guide/overview.md\n"
	if err := os.WriteFile(filepath.Join(pkg, tasks.PackageManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "guide", "overview.md"), []byte(overview), 0o644); err != nil {
		t.Fatalf("write overview: %v", err)
	}
	cfg := filepath.Join(root, ".tasks", "config.yaml")
	if err := os.WriteFile(cfg, []byte("prefix: tst\nuse:\n    - path: packages/policy\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root
}

// oversizedOverview is one paragraph past the 1 KiB cap — the shape an overview
// fragment actually takes, and the one with no line break to cut on.
func oversizedOverview() string {
	return strings.Repeat("policy text. ", (tasks.MaxGuideOverviewBytes/13)+40)
}

// HOOK-SPEC §3.7 is normative: a fragment over its cap is cut and **marked** as
// cut. The overview renderer never read Truncated, so half a rule reached every
// caller of `taskmgr guide` reading as a whole one.
func TestGuideOverview_MarksAFragmentItCut(t *testing.T) {
	isolatedHome(t)
	root := storeWithOverview(t, oversizedOverview())

	out, errOut, code := run(t, "--dir", root, "guide")
	if code != 0 {
		t.Fatalf("guide: exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "cut at") {
		t.Errorf("a cut overview fragment must say so:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("%d bytes", tasks.MaxGuideOverviewBytes)) {
		t.Errorf("the mark must name the cap that applied (%d):\n%s", tasks.MaxGuideOverviewBytes, out)
	}
	if !strings.Contains(out, "overview.md") {
		t.Errorf("the mark must name the whole fragment's file:\n%s", out)
	}
}

// The topic renderer hardcoded the section cap, so an author whose overview was
// being cut read "8192", concluded the cap was not what cut it, and trimmed to
// just under a limit that was never in force.
func TestGuideTopic_ReportsTheCapThatActuallyApplied(t *testing.T) {
	isolatedHome(t)
	root := storeWithOverview(t, oversizedOverview())

	out, errOut, code := run(t, "--dir", root, "guide", "pkg:policy:overview")
	if code != 0 {
		t.Fatalf("guide topic: exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, fmt.Sprintf("truncated at %d bytes", tasks.MaxGuideOverviewBytes)) {
		t.Errorf("the notice must name the overview cap:\n%s", out)
	}
	if strings.Contains(out, fmt.Sprintf("%d bytes", tasks.MaxGuideFragmentBytes)) {
		t.Errorf("the notice must not name the section cap for an overview fragment:\n%s", out)
	}
}

// A fragment under its cap is printed whole, with no notice.
func TestGuideOverview_SaysNothingAboutAFragmentItDidNotCut(t *testing.T) {
	isolatedHome(t)
	root := storeWithOverview(t, "every bug names its repro.\n")

	out, _, code := run(t, "--dir", root, "guide")
	if code != 0 {
		t.Fatalf("guide: exit %d", code)
	}
	if !strings.Contains(out, "every bug names its repro") {
		t.Errorf("the fragment must be printed:\n%s", out)
	}
	if strings.Contains(out, "cut at") {
		t.Errorf("a fragment under its cap must carry no notice:\n%s", out)
	}
}
