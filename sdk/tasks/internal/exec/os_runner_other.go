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

package exec

import (
	"fmt"
	"runtime"
)

// osRunner fails closed on platforms without unix signals. Like vfs.Lock, hook
// execution is unix-only today; a non-unix target reports a spawn error rather
// than silently running hooks without the SIGTERM/SIGKILL timeout contract.
// Production builds must target a unix OS.
type osRunner struct{}

func (osRunner) Run(spec Spec) Result {
	return Result{
		Category: SpawnError,
		Err:      fmt.Errorf("exec: hook execution unsupported on %s; build for a unix target", runtime.GOOS),
	}
}
