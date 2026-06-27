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

package query

import "fmt"

// ParseError is returned when the filter expression cannot be parsed.
// Pos is the byte offset in the expression string where the error was detected.
type ParseError struct {
	Pos     int
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("parse error at byte %d: %s", e.Pos, e.Message)
}

// parseErr constructs a *ParseError at the given byte offset.
func parseErr(pos int, format string, args ...interface{}) *ParseError {
	return &ParseError{Pos: pos, Message: fmt.Sprintf(format, args...)}
}
