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

// L1 test for mergeChanges: the paths come from a plain channel, so the test
// needs no store and no vfs.
package tasks

import (
	"testing"
	"testing/synctest"
	"time"
)

// vfs.Mem never loses a change, so no L2 test sends the empty path that
// reports one.
func TestMergeChanges_LostChanges_Signal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		changes := make(chan string)
		defer close(changes)
		signals := mergeChanges(changes)

		changes <- ""
		time.Sleep(watchQuietPeriod)
		synctest.Wait()

		select {
		case <-signals:
		default:
			t.Fatal("no signal for the empty path")
		}
	})
}
