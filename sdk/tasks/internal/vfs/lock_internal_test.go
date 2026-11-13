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
	"testing"
)

// A blocking Lock either holds the lock or fails: a caller handed no unlock
// and no error would write as if it held one.
func TestBlocking_NotOKIsAnError(t *testing.T) {
	if unlock, err := blocking(nil, false, nil); unlock != nil || err == nil {
		t.Errorf("not ok without an error = unlock %v, err %v; want an error", unlock != nil, err)
	}
	cause := errors.New("injected")
	if _, err := blocking(nil, false, cause); !errors.Is(err, cause) {
		t.Errorf("not ok with an error = %v, want the cause", err)
	}
	held := func() error { return nil }
	if unlock, err := blocking(held, true, nil); unlock == nil || err != nil {
		t.Errorf("ok = unlock %v, err %v; want the unlock", unlock != nil, err)
	}
}
