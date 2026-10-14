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

// Command checkgo fails when the Go toolchain that go.work pins is behind the
// newest patch release of its minor version.
//
// The `toolchain` line in go.work is the one Go pin: setup-go reads it in CI and
// the release, mise reads it locally (.mise.toml enables Go's version files, and
// mise prefers go.work to go.mod), and the go command honours it in the
// workspace. Dependabot does not update that line, so without this check a Go
// security release reaches no build until someone notices it. A newer minor
// version does not fail the check: moving to one is a decision, not an update.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// releasesURL lists the newest patch of each supported Go minor version.
const releasesURL = "https://go.dev/dl/?mode=json"

func main() {
	gowork, err := os.ReadFile("go.work")
	if err != nil {
		fail(err)
	}
	pinned, err := toolchainVersion(gowork)
	if err != nil {
		fail(err)
	}
	releases, err := stableReleases(releasesURL)
	if err != nil {
		fail(err)
	}
	if newer, ok := newerPatch(pinned, releases); ok {
		fail(fmt.Errorf("go.work pins go%s, but go%s is released; set `toolchain go%s` in go.work", pinned, newer, newer))
	}
	fmt.Printf("checkgo: go%s is the newest patch of its minor version\n", pinned)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "checkgo:", err)
	os.Exit(1)
}

// toolchainVersion returns the version of go.work's `toolchain` line, without
// the "go" prefix.
func toolchainVersion(file []byte) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(file))
	for scanner.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "toolchain go"); ok {
			return v, nil
		}
	}
	return "", fmt.Errorf("go.work has no `toolchain` line")
}

// stableReleases returns the stable versions the release feed lists, without
// the "go" prefix.
func stableReleases(url string) ([]string, error) {
	client := http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	var feed []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	var versions []string
	for _, r := range feed {
		if r.Stable {
			versions = append(versions, strings.TrimPrefix(r.Version, "go"))
		}
	}
	return versions, nil
}

// newerPatch returns the highest release with pinned's major and minor version
// and a higher patch number, and whether there is one. A version that is not
// major.minor.patch matches nothing.
func newerPatch(pinned string, releases []string) (string, bool) {
	line, patch, ok := splitPatch(pinned)
	if !ok {
		return "", false
	}
	newest, found := "", false
	for _, r := range releases {
		rLine, rPatch, ok := splitPatch(r)
		if ok && rLine == line && rPatch > patch {
			newest, patch, found = r, rPatch, true
		}
	}
	return newest, found
}

// splitPatch splits "1.27.2" into "1.27" and 2.
func splitPatch(version string) (string, int, bool) {
	i := strings.LastIndexByte(version, '.')
	if i < 0 || strings.Count(version, ".") != 2 {
		return "", 0, false
	}
	patch, err := strconv.Atoi(version[i+1:])
	if err != nil {
		return "", 0, false
	}
	return version[:i], patch, true
}
