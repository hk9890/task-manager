package tasks

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/hk9890/task-manager/sdk/tasks/internal/exec"
)

// records parses the captured JSON log lines into maps.
func records(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(buf)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("log line not JSON: %s (%v)", line, err)
		}
		out = append(out, m)
	}
	return out
}

func find(recs []map[string]any, msg string) map[string]any {
	for _, r := range recs {
		if r["msg"] == msg {
			return r
		}
	}
	return nil
}

func TestLogHook_RecordsDecisionAndDuration(t *testing.T) {
	var buf bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	fake := &exec.Fake{Func: func(exec.Spec) exec.Result {
		return exec.Result{Category: exec.Completed, ExitCode: 0, Stdout: []byte("hi"), Duration: 7 * time.Millisecond}
	}}
	s, err := Init(t.TempDir(), "x", WithLogger(lg))
	if err != nil {
		t.Fatal(err)
	}
	s.runner = fake
	s.cfg.Hooks = []Hook{{ID: "g", Event: "pre-create", Run: []string{"g"}}}

	if _, err := s.Create(CreateInput{Title: "t"}); err != nil {
		t.Fatal(err)
	}

	recs := records(t, &buf)
	hook := find(recs, "hook")
	if hook == nil {
		t.Fatal("expected a hook log record")
	}
	if hook["event"] != "pre-create" || hook["hook"] != "g" || hook["decision"] != "allow" {
		t.Errorf("hook record = %v", hook)
	}
	if _, ok := hook["duration_ms"]; !ok {
		t.Error("hook record must carry duration_ms")
	}
	// A committed write is also logged.
	if find(recs, "write") == nil {
		t.Error("expected a write log record")
	}
}

func TestLogHook_DenyLogsAtInfo(t *testing.T) {
	var buf bytes.Buffer
	// Level info: an allow (debug) is filtered out, a deny (info) is kept.
	lg := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	fake := &exec.Fake{Func: func(exec.Spec) exec.Result { return exec.Deny(1, "nope") }}
	s, err := Init(t.TempDir(), "x", WithLogger(lg))
	if err != nil {
		t.Fatal(err)
	}
	s.runner = fake
	s.cfg.Hooks = []Hook{{ID: "gate", Event: "pre-create", Run: []string{"g"}}}

	if _, err := s.Create(CreateInput{Title: "t"}); err == nil {
		t.Fatal("expected denial")
	}
	hook := find(records(t, &buf), "hook")
	if hook == nil || hook["decision"] != "deny" {
		t.Fatalf("expected a deny hook record at info level, got %v", hook)
	}
}

func TestLogger_DefaultIsSilent(t *testing.T) {
	// No WithLogger: the discard logger must not panic and a successful run is
	// silent. (We can't capture discard output; this asserts the path is safe.)
	s, err := Init(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(CreateInput{Title: "t"}); err != nil {
		t.Fatal(err)
	}
}
