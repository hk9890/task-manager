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

//go:build unix && !linux

package exec

import osexec "os/exec"

// runKeptInGroup runs a KeepGroup process in taskmgr's group, where a Ctrl-C
// at the terminal reaches it. macOS and the BSDs have no Pdeathsig, so a
// taskmgr killed alone leaves the process to run until it ends on its own,
// with no Timeout left to end it: a git push runs until git gives up on the
// connection.
func runKeptInGroup(cmd *osexec.Cmd) error { return cmd.Run() }
