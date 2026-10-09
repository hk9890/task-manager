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

package tasks

import (
	"path/filepath"
	"time"
)

// watchQuietPeriod is how long mergeChanges collects changes before it
// signals. One write touches up to three files, and a signal for each would
// make a consumer read the store while the write is still in progress.
const watchQuietPeriod = 100 * time.Millisecond

// mergeChanges turns the changed paths of a store into the signals of
// Store.Watch. The first path starts a quiet period, and one signal follows
// when it ends. The channel holds one signal, so a signal that is not received
// yet absorbs the next one. An empty path reports lost changes and signals like
// any other path. The signals end when changes is closed.
func mergeChanges(changes <-chan string) <-chan struct{} {
	signals := make(chan struct{}, 1)
	go func() {
		defer close(signals)
		var quiet <-chan time.Time
		for {
			select {
			case path, ok := <-changes:
				if !ok {
					return
				}
				// The lock file appears on the first write, also when that
				// write changes nothing.
				if filepath.Base(path) == lockFileName {
					continue
				}
				if quiet == nil {
					quiet = time.After(watchQuietPeriod)
				}
			case <-quiet:
				quiet = nil
				select {
				case signals <- struct{}{}:
				default:
				}
			}
		}
	}()
	return signals
}
