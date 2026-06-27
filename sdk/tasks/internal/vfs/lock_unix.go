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
	"fmt"
	"os"
	"syscall"
)

// Lock acquires an exclusive advisory flock on path, creating the file if
// necessary. It blocks until the lock is available. The returned unlock
// function releases the lock and closes the file descriptor.
// This is the unix implementation; see lock_other.go for the fail-closed stub.
func (osFS) Lock(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("vfs.Lock open: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("vfs.Lock flock: %w", err)
	}
	unlock := func() error {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
			_ = f.Close()
			return fmt.Errorf("vfs.Lock unlock: %w", err)
		}
		return f.Close()
	}
	return unlock, nil
}
