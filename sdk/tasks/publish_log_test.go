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

// publish_log_test.go — L2 for the records a publish and an init log
// (MONITORING.md "Publishing"), on the publishrun_test.go fixture: the
// stages, one record per git run and the level it is at, the summary, the
// failure, the wait on a busy store, and a remote's credentials kept out.
package tasks

import (
	"bytes"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
)

// debugLog is a logger at debug whose JSON records records() reads back.
func debugLog() (*bytes.Buffer, Option) {
	var buf bytes.Buffer
	return &buf, WithLogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

// withMsg returns the records whose msg is msg, in order.
func withMsg(recs []map[string]any, msg string) []map[string]any {
	var out []map[string]any
	for _, r := range recs {
		if r["msg"] == msg {
			out = append(out, r)
		}
	}
	return out
}

func TestPublish_LogsEachStageEachGitRunAndASummary(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	buf, logOpt := debugLog()
	if _, err := publishWith(fs, e, runner, logOpt); err != nil {
		t.Fatalf("publishWith: %v", err)
	}
	recs := records(t, buf)

	var stages, gits []string
	for _, r := range recs {
		switch r["msg"] {
		case "publish_stage":
			stages = append(stages, r["stage"].(string))
		case "publish_git":
			gits = append(gits, r["cmd"].(string))
			if r["level"] != "DEBUG" {
				t.Errorf("a git run of a successful publish logged at %v: %v", r["level"], r)
			}
		}
	}
	if want := []string{"config", "lock", "check", "commit", "push", "record"}; !slices.Equal(stages, want) {
		t.Errorf("stages = %q, want %q", stages, want)
	}
	var ran []string
	for _, c := range subcommands(runner.Calls()) {
		ran = append(ran, strings.Fields(c)[0])
	}
	if !slices.Equal(gits, ran) {
		t.Errorf("publish_git records %q, want one per git run %q", gits, ran)
	}

	// The stage is logged before its first git run, so the last record of a
	// run hung in the push names the push.
	push := slices.IndexFunc(recs, func(r map[string]any) bool { return r["stage"] == "push" })
	if push < 0 || recs[push+1]["msg"] != "publish_git" || recs[push+1]["cmd"] != "push" {
		t.Errorf("the push stage is not logged just before the push: %v", recs)
	}

	summary := withMsg(recs, "publish")
	if len(summary) != 1 {
		t.Fatalf("%d publish records, want one: %v", len(summary), recs)
	}
	s := summary[0]
	if s["level"] != "DEBUG" || s["machine"] != "laptop" || s["branch"] != "machine/laptop" ||
		s["committed"] != true || s["pushed"] != true || s["commit"] != sha(1) || s["retries"] != float64(0) {
		t.Errorf("publish record = %v", s)
	}
	for _, field := range []string{"lock_wait_ms", "duration_ms"} {
		if _, ok := s[field]; !ok {
			t.Errorf("publish record lacks %s: %v", field, s)
		}
	}
	if errs := withMsg(recs, "publish_error"); len(errs) != 0 {
		t.Errorf("a successful publish logged %v", errs)
	}
}

func TestInitPublish_LogsItsStagesAndWhatItSetUp(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, false)
	buf, logOpt := debugLog()
	if _, err := initPublishWith("/remote.git", fs, e, runner, logOpt); err != nil {
		t.Fatalf("initPublishWith: %v", err)
	}
	recs := records(t, buf)
	var stages []string
	for _, r := range withMsg(recs, "publish_stage") {
		stages = append(stages, r["stage"].(string))
	}
	if want := []string{"config", "lock", "init"}; !slices.Equal(stages, want) {
		t.Errorf("stages = %q, want %q", stages, want)
	}
	if n, want := len(withMsg(recs, "publish_git")), len(runner.Calls()); n != want {
		t.Errorf("%d publish_git records for %d git runs", n, want)
	}
	done := withMsg(recs, "publish_init")
	if len(done) != 1 || done[0]["machine"] != "laptop" || done[0]["branch"] != "machine/laptop" || done[0]["remote"] != "/remote.git" {
		t.Errorf("publish_init records = %v, want one naming machine, branch and remote", done)
	}
}

func TestPublish_FailedPushLogsGitsStderrAndTheStage(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.fail = map[string]exec.Result{"push": exec.Deny(128, "fatal: could not read from remote repository")}
	buf, logOpt := debugLog()
	if _, err := publishWith(fs, e, runner, logOpt); err == nil {
		t.Fatal("publish with a failing push succeeded")
	}
	recs := records(t, buf)
	var push map[string]any
	for _, r := range withMsg(recs, "publish_git") {
		if r["cmd"] == "push" {
			push = r
		}
	}
	if push == nil || push["level"] != "WARN" || push["exit"] != float64(128) ||
		push["stderr"] != "fatal: could not read from remote repository" {
		t.Errorf("push record = %v, want warn, exit 128 and git's stderr", push)
	}
	failed := withMsg(recs, "publish_error")
	if len(failed) != 1 || failed[0]["level"] != "ERROR" || failed[0]["stage"] != "push" ||
		!strings.Contains(failed[0]["error"].(string), "could not read from remote repository") {
		t.Errorf("publish_error records = %v, want one at error, stage push", failed)
	}
	if len(withMsg(recs, "publish")) != 0 {
		t.Error("a failed publish logged a summary")
	}
}

// The commit a failed push leaves behind is local only; the record says
// whether this run made it, and which commit it is, so the next run's push is
// not a surprise. The fake's commit clears the staged change, so the second
// run has nothing new to commit.
func TestPublish_FailedPushLogsWhetherThisRunCommitted(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.fail = map[string]exec.Result{"push": exec.Deny(128, "fatal: could not read from remote repository")}
	buf, logOpt := debugLog()
	if _, err := publishWith(fs, e, runner, logOpt); err == nil {
		t.Fatal("publish with a failing push succeeded")
	}
	if _, err := publishWith(fs, e, runner, logOpt); err == nil {
		t.Fatal("second publish with a failing push succeeded")
	}
	failed := withMsg(records(t, buf), "publish_error")
	if len(failed) != 2 {
		t.Fatalf("publish_error records = %v, want two", failed)
	}
	if failed[0]["committed"] != true || failed[0]["head"] != sha(1) {
		t.Errorf("first failure = %v, want committed=true and head the new commit", failed[0])
	}
	if failed[1]["committed"] != false || failed[1]["head"] != sha(1) {
		t.Errorf("second failure = %v, want committed=false and head the commit still unpushed", failed[1])
	}
}

// A failure after this run committed, before the push, still leaves that
// commit local, and says so.
func TestPublish_FailureAfterTheCommitLogsIt(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.fail = map[string]exec.Result{"rev-parse": exec.Deny(128, "fatal: bad object HEAD")}
	buf, logOpt := debugLog()
	if _, err := publishWith(fs, e, runner, logOpt); err == nil {
		t.Fatal("publish with a failing rev-parse succeeded")
	}
	failed := withMsg(records(t, buf), "publish_error")
	if len(failed) != 1 || failed[0]["stage"] != "commit" || failed[0]["committed"] != true || failed[0]["head"] != "" {
		t.Errorf("publish_error records = %v, want one at stage commit, committed=true, head unknown", failed)
	}
}

// Before anything is committed or pushed, the fields would only mislead.
func TestPublish_FailureBeforePushLogsNoCommitFields(t *testing.T) {
	fs, e, runner, g := publishFixture(t, true)
	g.head = ""
	buf, logOpt := debugLog()
	if _, err := publishWith(fs, e, runner, logOpt); !errors.Is(err, ErrPublishBranch) {
		t.Fatalf("detached publish: %v", err)
	}
	failed := withMsg(records(t, buf), "publish_error")
	if len(failed) != 1 || failed[0]["stage"] != "check" {
		t.Fatalf("publish_error records = %v, want one at stage check", failed)
	}
	for _, key := range []string{"committed", "head"} {
		if _, ok := failed[0][key]; ok {
			t.Errorf("a failure before the push carries %s: %v", key, failed[0])
		}
	}
}

// Exit 1 from config --get, diff --quiet, rev-parse --verify, symbolic-ref
// --quiet and merge-base --is-ancestor answers a question; a warn for each
// would bury the real failures. A rejected push also exits 1, and is one.
func TestPublish_ExitOneAnswersAreDebugAndFailuresWarn(t *testing.T) {
	buf, logOpt := debugLog()

	fs, e, runner, _ := publishFixture(t, false)
	if _, err := initPublishWith("/remote.git", fs, e, runner, logOpt); err != nil {
		t.Fatalf("initPublishWith: %v", err)
	}
	fs, e, runner, g := publishFixture(t, true)
	if _, err := publishWith(fs, e, runner, logOpt); err != nil {
		t.Fatalf("publishWith: %v", err)
	}
	g.head = ""
	if _, err := publishWith(fs, e, runner, logOpt); !errors.Is(err, ErrPublishBranch) {
		t.Fatalf("detached publish: %v", err)
	}
	g.head, g.staged, g.reject = "refs/heads/machine/laptop", true, true
	if _, err := publishWith(fs, e, runner, logOpt); !errors.Is(err, ErrMachineIDClash) {
		t.Fatalf("rejected publish: %v", err)
	}
	g.staged, g.reject, g.remoteTip, g.side = true, false, sha(99), sha(99)
	if _, err := publishWith(fs, e, runner, logOpt); err == nil {
		t.Fatal("publish after a rewrite succeeded")
	}

	answered := map[string]bool{}
	for _, r := range withMsg(records(t, buf), "publish_git") {
		if r["exit"] != float64(1) {
			continue
		}
		cmd := r["cmd"].(string)
		if cmd == "push" {
			if r["level"] != "WARN" || r["stderr"] == "" {
				t.Errorf("rejected push record = %v, want warn with git's stderr", r)
			}
			continue
		}
		answered[cmd] = true
		if r["level"] != "DEBUG" {
			t.Errorf("an answer logged as a failure: %v", r)
		}
		if _, ok := r["stderr"]; ok {
			t.Errorf("an answer carries stderr: %v", r)
		}
	}
	for _, cmd := range []string{"config", "diff", "rev-parse", "symbolic-ref", "merge-base"} {
		if !answered[cmd] {
			t.Errorf("no exit-1 answer from %s was logged", cmd)
		}
	}
}

// A store held for longer than publishLockWaitWarn is named once, however
// long the wait goes on; the summary carries the whole wait.
func TestPublish_BusyStoreLogsOneLockWaitNamingIt(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	fs.busy["beta"] = true
	at := time.Date(2026, 11, 13, 14, 2, 0, 0, time.UTC)
	buf, logOpt := debugLog()
	p, err := loadPublish(fs, e, runner, logOpt, WithClock(func() time.Time { return at }))
	if err != nil {
		t.Fatal(err)
	}
	waits := 0
	p.backoff = func() {
		waits++
		at = at.Add(time.Second)
		if waits == 8 {
			delete(fs.busy, "beta")
		}
	}
	if _, err := p.publish(); err != nil {
		t.Fatalf("publish: %v", err)
	}
	recs := records(t, buf)
	wait := withMsg(recs, "publish_lock_wait")
	if len(wait) != 1 || wait[0]["level"] != "WARN" || wait[0]["store"] != "beta" || wait[0]["waited_ms"] != float64(5000) {
		t.Errorf("publish_lock_wait records = %v, want one at warn naming beta after 5000ms", wait)
	}
	summary := withMsg(recs, "publish")
	if len(summary) != 1 || summary[0]["retries"] != float64(8) || summary[0]["lock_wait_ms"] != float64(8000) {
		t.Errorf("publish records = %v, want 8 retries and 8000ms of lock wait", summary)
	}
}

func TestPublish_BusyCentralRootLockWaitNamesIt(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	fs.busy["central"] = true
	at := time.Date(2026, 11, 13, 14, 2, 0, 0, time.UTC)
	buf, logOpt := debugLog()
	p, err := loadPublish(fs, e, runner, logOpt, WithClock(func() time.Time { return at }))
	if err != nil {
		t.Fatal(err)
	}
	p.backoff = func() {
		at = at.Add(publishLockWaitWarn)
		delete(fs.busy, "central")
	}
	if _, err := p.publish(); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// The wait is logged by the try after it, which here succeeds.
	if wait := withMsg(records(t, buf), "publish_lock_wait"); len(wait) != 0 {
		t.Errorf("a wait that ended on its first retry was logged: %v", wait)
	}

	fs.busy["central"] = true
	waits := 0
	p.backoff = func() {
		waits++
		at = at.Add(publishLockWaitWarn)
		if waits == 2 {
			delete(fs.busy, "central")
		}
	}
	if _, err := p.publish(); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if wait := withMsg(records(t, buf), "publish_lock_wait"); len(wait) != 1 || wait[0]["store"] != busyCentralRoot {
		t.Errorf("publish_lock_wait records = %v, want one naming %s", wait, busyCentralRoot)
	}
}

// A store a hung writer holds must end an unattended run, as a hung push
// does: the publish gives up after publishLockWaitMax, holding no store lock, runs
// no git add, and names the busy store and the wait in its error and its
// publish_error record.
func TestPublish_BusyStoreGivesUpAfterTheMaximumWait(t *testing.T) {
	for _, busy := range []string{"beta", "central"} {
		t.Run(busy, func(t *testing.T) {
			fs, e, runner, g := publishFixture(t, true)
			fs.busy[busy] = true
			at := time.Date(2026, 11, 13, 14, 2, 0, 0, time.UTC)
			buf, logOpt := debugLog()
			p, err := loadPublish(fs, e, runner, logOpt, WithClock(func() time.Time { return at }))
			if err != nil {
				t.Fatal(err)
			}
			waits := 0
			p.backoff = func() {
				waits++
				at = at.Add(7 * time.Second)
			}
			_, err = p.publish()
			want := `waiting 2m0s for the write lock of store "beta"`
			if busy == "central" {
				want = "waiting 2m0s for the central-root lock"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("publish with %s held for good: %v, want an error containing %q", busy, err, want)
			}
			if waits != 18 {
				t.Errorf("backed off %d times, want 18 (the first 7s step past 2m)", waits)
			}
			if slices.Contains(*g.log, "git add") {
				t.Errorf("staged although a lock was never taken: %q", *g.log)
			}
			held := map[string]bool{}
			for _, entry := range *g.log {
				if name, ok := strings.CutPrefix(entry, "lock "); ok {
					held[name] = true
				} else if name, ok := strings.CutPrefix(entry, "unlock "); ok {
					delete(held, name)
				}
			}
			if len(held) != 0 {
				t.Errorf("still held %v after giving up: %q", held, *g.log)
			}
			failed := withMsg(records(t, buf), "publish_error")
			if len(failed) != 1 || failed[0]["level"] != "ERROR" || failed[0]["stage"] != stageCommit || !strings.Contains(failed[0]["error"].(string), want) {
				t.Errorf("publish_error records = %v, want one at error, stage %s, naming the busy lock", failed, stageCommit)
			}
		})
	}
}

// Busy is what a timer expects while a manual sync runs, so it is not an
// error an alert should count.
func TestPublish_BusyIsLoggedAtInfo(t *testing.T) {
	fs, e, runner, _ := publishFixture(t, true)
	unlock := holdPublishLock(t, fs, e)
	defer func() { _ = unlock() }()
	buf, logOpt := debugLog()
	if _, err := publishWith(fs, e, runner, logOpt); !errors.Is(err, ErrPublishBusy) {
		t.Fatalf("publish while another runs: %v", err)
	}
	failed := withMsg(records(t, buf), "publish_error")
	if len(failed) != 1 || failed[0]["level"] != "INFO" || failed[0]["stage"] != "lock" {
		t.Errorf("publish_error records = %v, want one at info, stage lock", failed)
	}
}

// A remote URL may carry a token. Neither a record nor an error repeats it.
func TestPublish_KeepsTheRemotesCredentialsOutOfLogsAndErrors(t *testing.T) {
	const remote = "https://me:s3cret@example.com/stores.git"
	fs, e, runner, g := publishFixture(t, false)
	buf, logOpt := debugLog()
	if _, err := initPublishWith(remote, fs, e, runner, logOpt); err != nil {
		t.Fatalf("initPublishWith: %v", err)
	}
	if done := withMsg(records(t, bytes.NewBuffer(buf.Bytes())), "publish_init"); len(done) != 1 || done[0]["remote"] != "https://example.com/stores.git" {
		t.Errorf("publish_init = %v, want the remote without its credentials", done)
	}
	_, changed := initPublishWith("https://me:0ther@example.com/x.git", fs, e, runner, logOpt)

	g.staged = true
	g.fail = map[string]exec.Result{"push": exec.Deny(128, "fatal: unable to access '"+remote+"/': Could not resolve host: example.com")}
	_, pushed := publishWith(fs, e, runner, logOpt)

	for _, err := range []error{changed, pushed} {
		if err == nil || strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "0ther") {
			t.Errorf("error %v, want one without the credentials", err)
		}
	}
	if strings.Contains(buf.String(), "s3cret") || strings.Contains(buf.String(), "0ther") {
		t.Errorf("the log repeats the credentials:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "https://example.com/stores.git") {
		t.Errorf("the log lost the redacted URL:\n%s", buf.String())
	}
}
