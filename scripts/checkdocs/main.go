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

// Command checkdocs is the doc gate. It fails when a Markdown file in this
// repository cites something that is no longer there.
//
// The docs anchor their claims to the source tree — `sdk/tasks/internal/vfs`,
// `cmd/root.go`, `TESTING.md#conventions`. Nothing else in the project checks
// those: a rename leaves the citation reading as if it were still true, and the
// reader follows a dead path. Four classes are caught:
//
//  1. A `cmd/…` or `sdk/…` path that no longer exists on disk.
//  2. A citation pinned to a LINE NUMBER (`path:42`, `path#L42`). It rots faster
//     than the path ever does — it survives no edit above it — and this gate
//     cannot see that it moved, because the file still exists. Cite the symbol
//     instead: it is stable across edits, and it is greppable, which a line
//     number is not.
//  3. A relative Markdown link whose target file, or whose `#anchor` inside that
//     file, does not resolve.
//  4. An audience leak in docs/user-guide/ — a Go source path or a link into
//     docs/specs/ on a page written for someone who never opens this repository
//     (docs/DOCUMENTING.md owns that rule).
//
// Every spelling of a citation counts, fenced blocks included: a stale path in a
// code block misleads a reader exactly as far as one in prose.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// citation matches a path token rooted at one of the two source trees. The
// character class stops at a space, a backtick, a paren and a comma, so an
// ellipsis placeholder (`sdk/…`) ends at the slash and is never reported.
// `:\d+` / `#L\d+` are captured deliberately — that is finding 2, not a miss.
var citation = regexp.MustCompile(`(cmd|sdk)/[A-Za-z0-9._*/-]+(?::\d+|#L\d+)?`)

// mdLink matches an inline Markdown link. Reference-style links are not used in
// this doc set; add them here if that changes.
var mdLink = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)

// heading matches an ATX heading, for the anchor table a link fragment is
// checked against.
var heading = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*$`)

// mdLinkText matches the same link, capturing its text: a heading that contains
// a link contributes the text to its anchor, never the target.
var mdLinkText = regexp.MustCompile(`\[([^\]]*)\]\([^)\s]*\)`)

// lineAnchor matches the `:42` tail of a line-pinned citation.
var lineAnchor = regexp.MustCompile(`:\d+$`)

// slugPunct is everything GitHub drops when it derives an anchor from a heading:
// anything that is not alphanumeric, a space, a hyphen or an underscore.
var slugPunct = regexp.MustCompile(`[^\p{L}\p{N} _-]+`)

type finding struct {
	file string
	line int
	msg  string
}

func main() {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "checkdocs:", err)
		os.Exit(1)
	}

	docs, err := markdownFiles(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "checkdocs:", err)
		os.Exit(1)
	}

	anchors := map[string]map[string]bool{}
	var findings []finding
	for _, doc := range docs {
		body, err := os.ReadFile(filepath.Join(root, doc))
		if err != nil {
			fmt.Fprintln(os.Stderr, "checkdocs:", err)
			os.Exit(1)
		}
		text := string(body)
		findings = append(findings, checkCitations(doc, text, root)...)
		findings = append(findings, checkLinks(doc, text, root, anchors)...)
	}

	if len(findings) == 0 {
		fmt.Printf("checkdocs: %d files, no rot\n", len(docs))
		return
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].file != findings[j].file {
			return findings[i].file < findings[j].file
		}
		return findings[i].line < findings[j].line
	})
	for _, f := range findings {
		fmt.Fprintf(os.Stderr, "%s:%d: %s\n", f.file, f.line, f.msg)
	}
	fmt.Fprintf(os.Stderr, "\ncheckdocs: %d problem(s). docs/DOCUMENTING.md has the rules.\n", len(findings))
	os.Exit(1)
}

// repoRoot walks up from the working directory to the tree holding go.work, so
// the gate runs the same from a worktree, a subdirectory, or a mise task.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.work above %s", dir)
		}
		dir = parent
	}
}

// markdownFiles returns every doc the gate owns, repo-relative: the root
// steering files and everything under docs/. Worktrees under .claude/ hold
// copies of both and are skipped.
func markdownFiles(root string) ([]string, error) {
	var out []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			out = append(out, e.Name())
		}
	}
	err = filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".md") {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func checkCitations(doc, text, root string) []finding {
	var out []finding
	userGuide := strings.HasPrefix(doc, "docs/user-guide/")
	for i, line := range strings.Split(text, "\n") {
		for _, loc := range citation.FindAllStringIndex(line, -1) {
			if !rooted(line, loc[0]) {
				continue
			}
			token := line[loc[0]:loc[1]]
			if userGuide {
				out = append(out, finding{doc, i + 1, fmt.Sprintf(
					"user-guide page cites the source tree (%q) — it is written for someone who never opens this repository", token)})
				continue
			}
			if anchored(token) {
				out = append(out, finding{doc, i + 1, fmt.Sprintf(
					"line-anchored citation %q — cite the symbol, not the line", token)})
				continue
			}
			if !resolves(root, token) {
				out = append(out, finding{doc, i + 1, fmt.Sprintf("cited path does not exist: %q", token)})
			}
		}
	}
	return out
}

// rooted reports whether a match starts a token rather than ending someone
// else's. Without it the `sdk/tasks` inside the module path
// `github.com/hk9890/task-manager/sdk/tasks` and inside the pkg.go.dev badge URL
// are both reported against this tree.
func rooted(line string, start int) bool {
	if start == 0 {
		return true
	}
	switch line[start-1] {
	case '/', '.', '-', '@':
		return false
	}
	c := line[start-1]
	isWord := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
	return !isWord
}

func anchored(token string) bool {
	return strings.Contains(token, "#L") || lineAnchor.MatchString(token)
}

// resolves reports whether a cited token names something on disk. Three shapes
// are not plain paths and are normalised first.
func resolves(root, token string) bool {
	// A module tag, not a path: `sdk/vX.Y.Z` is what a release is called.
	if strings.HasPrefix(token, "sdk/v") {
		return true
	}
	// A qualified symbol: `sdk/tasks/internal/vfs.FS` names the package's type.
	// Only an exported suffix is stripped, so `cmd/root.go` keeps its extension.
	if i := strings.LastIndex(token, "."); i > 0 {
		suffix := token[i+1:]
		if suffix != "" && suffix[0] >= 'A' && suffix[0] <= 'Z' {
			token = token[:i]
		}
	}
	token = strings.TrimSuffix(token, "/")
	if strings.ContainsAny(token, "*?") {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(token)))
		return err == nil && len(matches) > 0
	}
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(token)))
	return err == nil
}

func checkLinks(doc, text, root string, anchors map[string]map[string]bool) []finding {
	var out []finding
	dir := filepath.Dir(doc)
	userGuide := strings.HasPrefix(doc, "docs/user-guide/")
	for i, line := range strings.Split(text, "\n") {
		for _, m := range mdLink.FindAllStringSubmatch(line, -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") || target == "#" {
				continue
			}
			path, fragment, _ := strings.Cut(target, "#")
			if path == "" {
				path = doc // a bare `#anchor` points into this same file
			} else {
				path = filepath.ToSlash(filepath.Clean(filepath.Join(dir, path)))
			}
			if userGuide && strings.HasPrefix(path, "docs/specs/") {
				out = append(out, finding{doc, i + 1, fmt.Sprintf(
					"user-guide page links to a spec (%q) — the specs are normative developer documents", target)})
				continue
			}
			info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
			if err != nil {
				out = append(out, finding{doc, i + 1, fmt.Sprintf("link target does not exist: %q", target)})
				continue
			}
			if fragment == "" || info.IsDir() || !strings.HasSuffix(path, ".md") {
				continue
			}
			table, ok := anchors[path]
			if !ok {
				table = anchorsOf(filepath.Join(root, filepath.FromSlash(path)))
				anchors[path] = table
			}
			if !table[fragment] {
				out = append(out, finding{doc, i + 1, fmt.Sprintf("no heading in %s matches anchor #%s", path, fragment)})
			}
		}
	}
	return out
}

// anchorsOf derives the set of `#fragment`s a Markdown file answers to, the way
// GitHub does: drop punctuation, lowercase, spaces to hyphens, and disambiguate
// a repeated heading with a numeric suffix.
func anchorsOf(path string) map[string]bool {
	out := map[string]bool{}
	body, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	seen := map[string]int{}
	for _, line := range strings.Split(string(body), "\n") {
		m := heading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		slug := slugify(m[2])
		if slug == "" {
			continue
		}
		if n := seen[slug]; n > 0 {
			out[fmt.Sprintf("%s-%d", slug, n)] = true
		}
		seen[slug]++
		out[slug] = true
	}
	return out
}

func slugify(text string) string {
	text = strings.ReplaceAll(text, "`", "")
	// Link syntax in a heading contributes only its text.
	text = mdLinkText.ReplaceAllString(text, "$1")
	text = slugPunct.ReplaceAllString(text, "")
	text = strings.ToLower(strings.TrimSpace(text))
	return strings.ReplaceAll(text, " ", "-")
}
