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

package vfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// Watch reports changes through the operating system's file notifications. It
// leaves out the temp files of WriteAtomic, which are never an entry of the
// store, and permission changes, which change no content.
func (osFS) Watch(ctx context.Context, dir string, subdirs []string) (<-chan string, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("vfs.Watch: %w", err)
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("vfs.Watch: %w", err)
	}
	isSubdir := make(map[string]bool, len(subdirs))
	for _, name := range subdirs {
		sub := filepath.Join(dir, name)
		isSubdir[sub] = true
		if err := w.Add(sub); err != nil && !pathMissing(sub) {
			_ = w.Close()
			return nil, fmt.Errorf("vfs.Watch: %w", err)
		}
	}

	changes := make(chan string)
	go func() {
		defer close(changes)
		defer func() { _ = w.Close() }()
		for {
			path := ""
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if ev.Name == dir && (ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename)) {
					return
				}
				if ev.Op == fsnotify.Chmod || strings.HasPrefix(filepath.Base(ev.Name), tempPrefix) {
					continue
				}
				if isSubdir[ev.Name] && ev.Has(fsnotify.Create) {
					// A failed Add leaves the subdirectory unwatched; the
					// path sent below still reports that it appeared.
					_ = w.Add(ev.Name)
				}
				path = ev.Name
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
			select {
			case changes <- path:
			case <-ctx.Done():
				return
			}
		}
	}()
	return changes, nil
}

func pathMissing(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}
