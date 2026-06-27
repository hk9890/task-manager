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

import (
	"fmt"
	"runtime"
)

// Lock fails closed on platforms without flock. FS.Lock is unix-only today
// (see fs.go). A caller on a non-unix target receives an error immediately
// rather than silently proceeding without mutual exclusion.
func (osFS) Lock(path string) (func() error, error) {
	return nil, fmt.Errorf("vfs.Lock: file locking unsupported on %s; build for a unix target", runtime.GOOS)
}
