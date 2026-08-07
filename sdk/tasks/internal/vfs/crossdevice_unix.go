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

//go:build unix

package vfs

import (
	"errors"
	"syscall"
)

// isCrossDevice reports whether err is EXDEV — the kernel's refusal to rename a
// path across filesystems. It is MoveTree's signal to fall back to copy+remove;
// every other rename failure is surfaced unchanged. This is the unix
// implementation; see crossdevice_other.go for the stub.
func isCrossDevice(err error) bool { return errors.Is(err, syscall.EXDEV) }
