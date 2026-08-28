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

// White-box unit test (L1, no FS): the guide is hand-written prose, so unlike the
// derived `commands` catalog it can drift from the model. This is its drift guard.
package cmd

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// TestGuideText_CoversModel fails if a status or type is added to the SDK without
// also being reflected in the guide's "## The model" section. It is a presence
// check: the point is to catch a *new* value (a fresh status/type would be absent
// here), not to validate phrasing.
func TestGuideText_CoversModel(t *testing.T) {
	for _, s := range tasks.Statuses {
		if !strings.Contains(guideText, string(s)) {
			t.Errorf("guideText omits status %q — update the guide's model section", s)
		}
	}
	for _, ty := range tasks.Types {
		if !strings.Contains(guideText, string(ty)) {
			t.Errorf("guideText omits type %q — update the guide's model section", ty)
		}
	}
}

// guideFlagRE finds the long flags the guide's examples promise. The guide is the
// text a caller pastes into its own instructions before it runs anything, so a
// flag named here that the CLI does not have is not a typo in a document — it is
// a command the caller will build and the binary will reject.
var guideFlagRE = regexp.MustCompile(`--[a-z][a-z0-9-]+`)

// TestGuideText_NamesOnlyRealFlags fails if the guide promises a flag no command
// in the live tree defines. `commands` is derived and cannot drift; this prose is
// hand-maintained, and it grew examples that name flags, so it needs the guard
// the derived catalog gets for free.
func TestGuideText_NamesOnlyRealFlags(t *testing.T) {
	defined := make(map[string]bool)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		// --help is cobra's own, added when a command is first executed rather
		// than at registration. Ask for it so the guard reads the tree a user
		// meets, not the one that exists before anything has run.
		c.InitDefaultHelpFlag()
		c.Flags().VisitAll(func(f *pflag.Flag) { defined[f.Name] = true })
		c.PersistentFlags().VisitAll(func(f *pflag.Flag) { defined[f.Name] = true })
		c.InheritedFlags().VisitAll(func(f *pflag.Flag) { defined[f.Name] = true })
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)

	var missing []string
	seen := make(map[string]bool)
	for _, m := range guideFlagRE.FindAllString(guideText, -1) {
		name := strings.TrimPrefix(m, "--")
		if seen[name] {
			continue
		}
		seen[name] = true
		if !defined[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		t.Errorf("the guide promises --%s, which no command defines — fix the guide or the flag", name)
	}
}

// TestGuideSections_HaveUniqueIDs guards the topic roster: an id is the caller's
// stable handle on a part of the guide, so two sections sharing one would make
// `taskmgr guide <topic>` print whichever came first and silently hide the other.
func TestGuideSections_HaveUniqueIDs(t *testing.T) {
	seen := make(map[string]bool, len(guideSections))
	for _, s := range guideSections {
		if s.id == "" {
			t.Error("every guide section needs an id — it is what a caller names")
			continue
		}
		if s.id == guidePackagesTopic {
			t.Errorf("section id %q collides with the reserved topic that selects every package section", s.id)
		}
		if seen[s.id] {
			t.Errorf("guide section id %q is used twice", s.id)
		}
		seen[s.id] = true
		if s.summary == "" {
			t.Errorf("section %q has no summary — `guide --list` is the discovery surface", s.id)
		}
	}
}
