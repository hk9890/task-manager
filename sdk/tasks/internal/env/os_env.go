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

import "os"

// osEnv is the real-environment implementation of Environment. It is, alongside
// vfs.osFS and exec's OS runner, one of the few types in sdk/tasks permitted to
// import os.
type osEnv struct{}

func (osEnv) Getenv(key string) string { return os.Getenv(key) }

func (osEnv) UserHomeDir() (string, error) { return os.UserHomeDir() }
