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

// Package env is the environment seam: the only package in sdk/tasks besides
// vfs (disk) and exec (hook processes) permitted to import os. Store resolution
// (CONFIG-SPEC) must read the user's home directory and the TASKMGR_* variables;
// concentrating that here keeps the rest of sdk/tasks free of os, exactly as vfs
// does for the filesystem and exec for processes, so resolution stays
// hermetically testable with no real HOME or environment touched.
package env

// Environment is the seam for reading the user environment during store
// resolution. The OS implementation (NewOS) reads the real process environment;
// Fake scripts values for tests.
type Environment interface {
	// Getenv returns the value of the named environment variable, or "" if it is
	// unset (matching os.Getenv).
	Getenv(key string) string

	// UserHomeDir returns the current user's home directory (matching
	// os.UserHomeDir): $HOME on unix.
	UserHomeDir() (string, error)
}

// NewOS returns an Environment backed by the real process environment.
func NewOS() Environment { return osEnv{} }
