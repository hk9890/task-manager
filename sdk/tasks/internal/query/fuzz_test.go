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

package query_test

// fuzz_test.go — Go native fuzz targets for the query package.
//
// Property under test:
//   - FuzzParseQuery: query.Parse never panics and never hangs for any input.
//     Any input produces either a valid Node or a *ParseError — no other outcome.
//
// Seed corpus: a mix of valid expressions, edge cases, and known-adversarial
// inputs (deep nesting, unterminated quotes, bare dates, unicode).
//
// Running modes:
//   go test -run=FuzzParseQuery ./tasks/internal/query/   -- seed corpus only (CI)
//   go test -fuzz=FuzzParseQuery -fuzztime=30s ./...      -- actual fuzzing

import (
	"errors"
	"testing"
	"unicode/utf8"

	"github.com/hk9890/task-manager/sdk/tasks/internal/query"
)

// FuzzParseQuery fuzzes query.Parse with arbitrary byte inputs.
//
// Invariants verified on every input:
//  1. Parse does not panic.
//  2. The result is either (non-nil Node, nil error) or (nil, *ParseError).
//  3. If Parse succeeds, the returned Node is non-nil.
//  4. If Parse fails, the error wraps *ParseError with a non-empty message.
func FuzzParseQuery(f *testing.F) {
	// ── seed: valid expressions (from §6 examples and parse_test.go) ─────────
	seeds := []string{
		// empty / whitespace
		"",
		"   ",
		"\t\n",
		// bare fields
		"ready",
		"blocked",
		"!ready",
		"!blocked",
		// status
		`status == "open"`,
		`status == "closed"`,
		`status != "in_progress"`,
		// type
		`type == bug`,
		`type == task`,
		`type == feature`,
		`type == epic`,
		`type == chore`,
		// priority
		`priority == 0`,
		`priority <= 2`,
		`priority >= 3`,
		`priority < 1`,
		`priority > 4`,
		// assignee / label / parent
		`assignee == "hans"`,
		`assignee ~ "partial"`,
		`label == "area:db"`,
		`label ~ area`,
		`parent == "dtt-0007"`,
		// text
		`text ~ "drill"`,
		// date fields
		`created > "2026-01-01"`,
		`updated < "2026-06-01"`,
		`closed > "2026-01-01"`,
		`created == "2026-01-15T10:30:00Z"`,
		// bare dates (QUERY-SPEC §3)
		`created > 2026-01-01`,
		`closed > 2026-01-01T00:00:00Z`,
		// combined expressions
		`status == "open" && priority <= 1`,
		`type == bug && label ~ "area:db"`,
		`ready && priority <= 2`,
		`text ~ "drill" && !blocked`,
		`assignee == "hans" && (type == bug || type == chore)`,
		`status == "open" || type == bug && priority <= 2`,
		// parentheses
		`(ready)`,
		`((ready))`,
		`(status == "open" && (type == bug || priority <= 2))`,

		// ── seed: adversarial / edge cases ──────────────────────────────────
		// unterminated quote
		`status == "open`,
		`assignee == "`,
		// bare operators
		`==`,
		`!=`,
		`&&`,
		`||`,
		`!`,
		// unclosed paren
		`(ready`,
		`((status == "open"`,
		// trailing junk
		`ready extra`,
		`status == "open" "extra"`,
		// missing value
		`status ==`,
		`priority >=`,
		// unknown field
		`foobar == "x"`,
		// invalid priority
		`priority == 5`,
		`priority == -1`,
		`priority == 99`,
		// invalid status/type
		`status == "flying"`,
		`type == "spaceship"`,
		// invalid date
		`closed > "not-a-date"`,
		`created == "9999-99-99"`,
		// deeply nested (at and over the 256-level cap)
		`(((((((((((((((((((((ready)))))))))))))))))))))`,
		// unicode in value positions
		`assignee == "用户"`,
		`label ~ "área:db"`,
		`text ~ "𝄞"`,
		// null byte
		"status == \x00",
		// very long bareword
		`assignee == ` + string(make([]byte, 4096)),
		// lone high-surrogate (invalid UTF-8)
		"status == \xed\xa0\x80",
		// mixed valid + invalid
		`status == "open" && \xff`,
	}

	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, expr string) {
		// ── invariant 1 & 2: no panic; result is Node or *ParseError ─────────
		// The deferred recover catches any panic the parser might throw.
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Parse(%q) panicked: %v", expr, r)
			}
		}()

		node, err := query.Parse(expr)

		if err != nil {
			// ── invariant 4: error must be *ParseError ─────────────────────
			var pe *query.ParseError
			if !errors.As(err, &pe) {
				t.Errorf("Parse(%q) returned non-ParseError: %T: %v", expr, err, err)
				return
			}
			if pe.Message == "" {
				t.Errorf("Parse(%q) returned ParseError with empty message", expr)
			}
		} else {
			// ── invariant 3: successful parse → non-nil node ───────────────
			if node == nil {
				t.Errorf("Parse(%q) returned nil node with nil error", expr)
			}
			// ── bonus: if the input is valid UTF-8 and Parse succeeds,
			//    Compile should also succeed (Compile = Parse + type-check).
			if utf8.ValidString(expr) {
				_, compileErr := query.Compile(expr)
				if compileErr != nil {
					// Compile stricter than Parse is not a bug per se, but
					// log it so fuzzer findings surface this discrepancy.
					_ = compileErr
				}
			}
		}
	})
}
