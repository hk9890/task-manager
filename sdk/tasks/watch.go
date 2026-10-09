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
	"context"
	"fmt"
	"path/filepath"
	"time"
)

// watchQuietPeriod is how long Watch collects changes before it signals. One
// write touches up to three files, and a signal for each would make a consumer
// read the store while the write is still in progress.
const watchQuietPeriod = 100 * time.Millisecond

// Watch signals each change to the store, from any process and from this
// handle alike: an issue, a comment, a body sidecar or the configuration that
// is written, moved or removed, by the engine or by hand.
//
// A value on the channel says "the store changed since your last receive, read
// it again". It carries nothing else. Changes that arrive close together give
// one signal, and a signal that is not received yet absorbs the next one, so a
// slow consumer never falls behind. A signal can be redundant: it reports that
// files changed, not that any query now answers differently.
//
// The channel closes when ctx is done, or when the store directory is removed
// or renamed. Call Watch again on a new handle to follow a moved store.
func (s *Store) Watch(ctx context.Context) (<-chan struct{}, error) {
	changes, err := s.fs.Watch(ctx, s.dir, []string{closedDirName, commentsDirName, contentDirName})
	if err != nil {
		return nil, fmt.Errorf("watch store: %w", err)
	}

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
	return signals, nil
}
