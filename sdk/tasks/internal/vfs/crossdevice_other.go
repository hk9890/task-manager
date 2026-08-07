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

//go:build !unix

package vfs

// isCrossDevice always reports false on non-unix targets, where there is no
// portable EXDEV to test for: MoveTree then surfaces the rename error unchanged
// instead of falling back to a copy. FS is unix-only in production anyway (see
// the Lock platform contract in fs.go).
func isCrossDevice(error) bool { return false }
