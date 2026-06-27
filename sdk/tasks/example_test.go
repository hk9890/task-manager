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

package tasks_test

import (
	"fmt"
	"log"

	"github.com/hk9890/task-manager/sdk/tasks"
)

// Example shows the typical flow: open the store, create an issue, and list the
// issues that are ready to work on (those with no open blockers).
func Example() {
	// Open the .tasks store by searching upward from the current directory.
	store, err := tasks.Open("")
	if err != nil {
		log.Fatal(err)
	}

	res, err := store.Create(tasks.CreateInput{
		Title: "Fix nav",
		Type:  tasks.TypeBug,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("created", res.Issue.ID)

	ready, err := store.Ready()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("ready:", len(ready))
}
