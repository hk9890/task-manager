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

// L2 tests for Store.Watch on vfs.Mem: which writes signal, how signals merge,
// and when the channel closes. A test that waits for the quiet period runs in a
// synctest bubble, so the wait passes on a fake clock and costs no real time.
package tasks_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks"
	"github.com/hk9890/task-manager/sdk/tasks/internal/storetest"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

const (
	// signalDeadline is far above the quiet period, so a loaded machine does
	// not fail a test that waits for a signal.
	signalDeadline = 5 * time.Second
	// silenceWindow is three quiet periods: a second signal for the same
	// writes would arrive inside it.
	silenceWindow = 300 * time.Millisecond
)

func watchStore(t *testing.T, s *tasks.Store) <-chan struct{} {
	t.Helper()
	signals, err := s.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	return signals
}

func wantSignal(t *testing.T, signals <-chan struct{}) {
	t.Helper()
	select {
	case _, ok := <-signals:
		if !ok {
			t.Fatal("channel closed, want a signal")
		}
	case <-time.After(signalDeadline):
		t.Fatal("no signal")
	}
}

func wantNoSignal(t *testing.T, signals <-chan struct{}) {
	t.Helper()
	select {
	case _, ok := <-signals:
		if !ok {
			t.Fatal("channel closed, want it open and silent")
		}
		t.Fatal("got a signal, want none")
	case <-time.After(silenceWindow):
	}
}

func wantClosed(t *testing.T, signals <-chan struct{}) {
	t.Helper()
	deadline := time.After(signalDeadline)
	for {
		select {
		case _, ok := <-signals:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel still open")
		}
	}
}

func TestWatch_EachWrite_GivesOneSignal(t *testing.T) {
	title := "renamed"
	writes := map[string]func(s *tasks.Store) error{
		"Create": func(s *tasks.Store) error {
			_, err := s.Create(tasks.CreateInput{Title: "new"})
			return err
		},
		"Update": func(s *tasks.Store) error {
			_, err := s.Update("tst-0001", tasks.UpdateInput{Title: &title})
			return err
		},
		"Close": func(s *tasks.Store) error {
			_, err := s.Close("tst-0001", "done")
			return err
		},
		"Reopen": func(s *tasks.Store) error {
			_, err := s.Reopen("tst-0003")
			return err
		},
		"AddComment": func(s *tasks.Store) error {
			_, err := s.AddComment("tst-0001", tasks.Actor{Name: "hans"}, "a note")
			return err
		},
		"EditComment on a closed issue": func(s *tasks.Store) error {
			comments, err := s.Comments("tst-0003")
			if err != nil {
				return err
			}
			_, err = s.EditComment("tst-0003", comments[0].ID, tasks.Actor{Name: "hans"}, "revised")
			return err
		},
		"AddDep": func(s *tasks.Store) error {
			return s.AddDep("tst-0001", "tst-0002")
		},
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := storetest.New(t).
					Issue("tst-0001").
					Issue("tst-0002").
					Closed("tst-0003").
					Comment("tst-0003", "hans", "first note").
					Mem()
				signals := watchStore(t, s)

				if err := write(s); err != nil {
					t.Fatalf("write: %v", err)
				}

				wantSignal(t, signals)
				wantNoSignal(t, signals)
			})
		})
	}
}

func TestWatch_Burst_GivesOneSignal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := storetest.New(t).Mem()
		signals := watchStore(t, s)

		for range 5 {
			if _, err := s.Create(tasks.CreateInput{Title: "one of a burst"}); err != nil {
				t.Fatalf("Create: %v", err)
			}
		}

		wantSignal(t, signals)
		wantNoSignal(t, signals)
	})
}

func TestWatch_UnreceivedSignal_AbsorbsTheNext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := storetest.New(t).Mem()
		signals := watchStore(t, s)

		for range 2 {
			if _, err := s.Create(tasks.CreateInput{Title: "nobody is receiving"}); err != nil {
				t.Fatalf("Create: %v", err)
			}
			time.Sleep(silenceWindow)
		}

		wantSignal(t, signals)
		wantNoSignal(t, signals)
	})
}

func TestWatch_Read_GivesNoSignal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := storetest.New(t).Issue("tst-0001").Mem()
		signals := watchStore(t, s)

		if _, err := s.Get("tst-0001"); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if _, err := s.List(tasks.Filter{}); err != nil {
			t.Fatalf("List: %v", err)
		}

		wantNoSignal(t, signals)
	})
}

func TestWatch_ContextDone_ClosesChannel(t *testing.T) {
	s := storetest.New(t).Mem()
	ctx, cancel := context.WithCancel(t.Context())
	signals, err := s.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	cancel()

	wantClosed(t, signals)
}

func TestWatch_SeamRefuses_ReturnsItsError(t *testing.T) {
	fs := vfs.NewMem()
	s, err := tasks.InitWithVFS("/proj", "tst", fs)
	if err != nil {
		t.Fatalf("InitWithVFS: %v", err)
	}
	refused := errors.New("no watches left")
	fs.FailOn("Watch", s.Dir(), refused)

	_, err = s.Watch(t.Context())

	if !errors.Is(err, refused) {
		t.Fatalf("Watch error = %v, want it to wrap %v", err, refused)
	}
}
