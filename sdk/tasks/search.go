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

// search.go — Free-text search sugar. SearchExpr turns a user's search-box query
// into the canonical filter expression so the CLI `search` command and any SDK/UI
// caller share one definition of what a text search means. It is sugar over
// Criteria{Text, TextMatch: TextAllWords}.Build() — NOT a second search engine.
//
// SDK-SPEC §3 (Criteria/SearchExpr). QUERY-SPEC §4 (the `text` field).
//
// Pure-core: no os, no vfs import. No filesystem access anywhere in this file.
package tasks

// SearchExpr returns the canonical filter expression for a free-text search query,
// applying the shared search semantics — AND-of-words: the query is split on
// whitespace and every word must appear in the issue's id/title/description
// (order-independent). Matching is per-word substring (inherited from `~`), so
// "cat dog" also matches "category dogma". An empty or whitespace-only query yields
// "" (the always-true predicate). The result is always a valid filter expression,
// usable as Filter.Expr or with Store.Query.
//
// SearchExpr is total by contract: it always returns a usable expression and never
// reports an error — a search box must never reject what a user typed. (This is why
// it returns a bare string rather than mirroring Criteria.Build's (string, error).)
//
// Use this as the single shared entry point for user-facing text search; building
// the expression in one place keeps the CLI and any UI behaving identically. To
// combine search with structured facets (status, priority, …), build a Criteria
// with TextMatch: TextAllWords and call Build instead of concatenating expression
// strings by hand.
func SearchExpr(query string) string {
	// Build never returns an error for a text-only Criteria (there are no enums or
	// numeric bounds to validate), so the error is safely discarded.
	expr, _ := Criteria{Text: query, TextMatch: TextAllWords}.Build()
	return expr
}
