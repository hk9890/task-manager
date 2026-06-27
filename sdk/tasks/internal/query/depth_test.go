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

// depth_test.go — tests that deep/adversarial inputs return clean errors, not crashes.
//
// These tests guard against the uncatchable stack-overflow (fatal error) that
// would occur without the depth cap in the parser.

import (
	"errors"
	"strings"
	"testing"

	"github.com/hk9890/task-manager/sdk/tasks/internal/query"
)

// buildDeeplyNestedExpr builds an expression with n levels of parentheses:
// (((...(ready)...)))
// Each extra pair of parens adds one nesting level.
func buildDeeplyNestedExpr(depth int) string {
	return strings.Repeat("(", depth) + "ready" + strings.Repeat(")", depth)
}

// TestParse_NestingDepth_AtLimit verifies that an expression at exactly
// maxExprDepth (256) parens parses successfully.
func TestParse_NestingDepth_AtLimit(t *testing.T) {
	expr := buildDeeplyNestedExpr(256)
	_, err := query.Parse(expr)
	if err != nil {
		t.Errorf("Parse at depth 256 should succeed, got: %v", err)
	}
}

// TestParse_NestingDepth_OneOverLimit verifies that 257 nesting levels returns
// a *ParseError ("expression nesting too deep"), not a crash.
func TestParse_NestingDepth_OneOverLimit(t *testing.T) {
	expr := buildDeeplyNestedExpr(257)
	_, err := query.Parse(expr)
	if err == nil {
		t.Fatal("Parse at depth 257 should return error, got nil")
	}
	var pe *query.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ParseError, got %T: %v", err, err)
	}
	if !strings.Contains(pe.Message, "nesting too deep") {
		t.Errorf("expected 'nesting too deep' in error message, got %q", pe.Message)
	}
}

// TestParse_NestingDepth_Extreme verifies that a very deeply nested expression
// (100k parens) returns a clean *ParseError without crashing.
// This test would produce a fatal stack overflow without the depth cap.
func TestParse_NestingDepth_Extreme(t *testing.T) {
	expr := buildDeeplyNestedExpr(100_000)
	_, err := query.Parse(expr)
	if err == nil {
		t.Fatal("Parse with 100k nesting levels should return error")
	}
	var pe *query.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ParseError, got %T: %v", err, err)
	}
}

// TestCompile_NestingDepth_Extreme verifies Compile also returns *ParseError
// on deeply nested input (since Compile delegates to Parse).
func TestCompile_NestingDepth_Extreme(t *testing.T) {
	expr := buildDeeplyNestedExpr(100_000)
	_, err := query.Compile(expr)
	if err == nil {
		t.Fatal("Compile with 100k nesting levels should return error")
	}
	var pe *query.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ParseError, got %T: %v", err, err)
	}
}

// TestParse_NestingDepth_ShallowStillWorks verifies ordinary nested expressions
// (e.g. 5 levels) still parse fine after the depth-cap change.
func TestParse_NestingDepth_ShallowStillWorks(t *testing.T) {
	cases := []string{
		`(ready)`,
		`((ready))`,
		`(((ready)))`,
		`((((ready))))`,
		`(((((ready)))))`,
		`(status == "open" && (type == bug || (priority <= 2)))`,
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			_, err := query.Parse(expr)
			if err != nil {
				t.Errorf("Parse(%q) unexpected error: %v", expr, err)
			}
		})
	}
}
