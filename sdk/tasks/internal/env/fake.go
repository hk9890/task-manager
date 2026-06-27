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

package env

import "errors"

// Fake is an Environment that returns scripted values, so store resolution can
// be unit-tested hermetically (the env analogue of vfs.Mem) without reading the
// real HOME or process environment.
type Fake struct {
	// Vars holds environment variables; Getenv returns Vars[key] (or "").
	Vars map[string]string
	// Home is the UserHomeDir result. When empty, UserHomeDir returns an error,
	// matching os.UserHomeDir on a machine with no $HOME.
	Home string
}

func (f Fake) Getenv(key string) string { return f.Vars[key] }

func (f Fake) UserHomeDir() (string, error) {
	if f.Home == "" {
		return "", errors.New("env: $HOME is not defined")
	}
	return f.Home, nil
}

// compile-time check that Fake satisfies Environment.
var _ Environment = Fake{}
