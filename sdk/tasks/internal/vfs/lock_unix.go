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
	"fmt"
	"os"
	"syscall"
)

// Lock acquires an exclusive advisory flock on path, creating the file if
// necessary. It blocks until the lock is available. The returned unlock
// function releases the lock and closes the file descriptor.
// This is the unix implementation; see lock_other.go for the fail-closed stub.
func (osFS) Lock(path string) (func() error, error) {
	unlock, _, err := flock(path, syscall.LOCK_EX)
	return unlock, err
}

// TryLock is Lock with LOCK_NB: a lock another open file holds is ok false.
func (osFS) TryLock(path string) (func() error, bool, error) {
	return flock(path, syscall.LOCK_EX|syscall.LOCK_NB)
}

func flock(path string, how int) (unlock func() error, ok bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, fmt.Errorf("vfs.Lock open: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("vfs.Lock flock: %w", err)
	}
	unlock = func() error {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
			_ = f.Close()
			return fmt.Errorf("vfs.Lock unlock: %w", err)
		}
		return f.Close()
	}
	return unlock, true, nil
}
