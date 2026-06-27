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

//go:build integration

package cmd_test

import "github.com/hk9890/task-manager/sdk/tasks"

// unwrap adapts the (*MutationResult, error) returned by the SDK's gated
// mutation methods back to (*Issue, error) for L4 CLI tests that seed fixtures
// via the SDK and only need the resulting issue.
func unwrap(r *tasks.MutationResult, err error) (*tasks.Issue, error) {
	if err != nil {
		return nil, err
	}
	return r.Issue, nil
}
