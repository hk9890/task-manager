package tasks

import (
	"strings"
	"testing"
	"time"
)

// L1: buildHookSet is pure (no filesystem), so config validation is unit-tested
// directly against the raw Config. HOOK-SPEC §3.1/§3.2/§3.4.

func TestBuildHookSet_Defaults(t *testing.T) {
	hs, err := buildHookSet(Config{Prefix: "x"})
	if err != nil {
		t.Fatalf("empty config: unexpected error %v", err)
	}
	if hs.timeout != defaultHookTimeout {
		t.Fatalf("default timeout = %v, want %v", hs.timeout, defaultHookTimeout)
	}
	if len(hs.hooks) != 0 {
		t.Fatalf("no hooks configured, got %d", len(hs.hooks))
	}
}

func TestBuildHookSet_Timeout(t *testing.T) {
	cases := []struct {
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{"", defaultHookTimeout, false},
		{"5m", 5 * time.Minute, false},
		{"500ms", 500 * time.Millisecond, false},
		{"0", 0, false},  // disables
		{"0s", 0, false}, // disables
		{"abc", 0, true}, // unparseable
		{"-1s", 0, true}, // negative
		{"10", 0, true},  // missing unit
	}
	for _, c := range cases {
		hs, err := buildHookSet(Config{Prefix: "x", HookTimeout: c.raw})
		if c.wantErr {
			if err == nil {
				t.Errorf("hook_timeout %q: want error, got none", c.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("hook_timeout %q: unexpected error %v", c.raw, err)
			continue
		}
		if hs.timeout != c.want {
			t.Errorf("hook_timeout %q: got %v, want %v", c.raw, hs.timeout, c.want)
		}
	}
}

func TestBuildHookSet_ValidHooks(t *testing.T) {
	hs, err := buildHookSet(Config{
		Prefix: "x",
		Hooks: []Hook{
			{ID: "tests", Event: "pre-close", When: `type == "feature"`, Run: []string{"make", "test"}},
			{Event: "post-close", Run: []string{"./notify.sh"}},
		},
	})
	if err != nil {
		t.Fatalf("valid hooks: unexpected error %v", err)
	}
	if len(hs.hooks) != 2 {
		t.Fatalf("compiled %d hooks, want 2", len(hs.hooks))
	}
	if hs.hooks[0].id != "tests" || hs.hooks[0].event != "pre-close" || hs.hooks[0].when == nil {
		t.Errorf("hook[0] = %+v, want id=tests event=pre-close with a compiled when", hs.hooks[0])
	}
	// id defaulting: "<event>#<index>"
	if hs.hooks[1].id != "post-close#1" || hs.hooks[1].event != "post-close" {
		t.Errorf("hook[1] = %+v, want id=post-close#1 event=post-close", hs.hooks[1])
	}
	if hs.hooks[1].when != nil {
		t.Error("hook[1] has no when clause; predicate must be nil (always)")
	}
}

func TestBuildHookSet_InvalidHooks(t *testing.T) {
	cases := []struct {
		name    string
		hook    Hook
		errWant string
	}{
		{"unknown event", Hook{Event: "pre-delete", Run: []string{"x"}}, "unknown event"},
		{"missing event", Hook{Run: []string{"x"}}, "missing required field event"},
		{"empty run", Hook{Event: "pre-close"}, "non-empty argv"},
		{"blank program", Hook{Event: "pre-close", Run: []string{"   "}}, "non-empty argv"},
		{"bad when", Hook{Event: "pre-close", When: "type ==", Run: []string{"x"}}, "invalid when"},
	}
	for _, c := range cases {
		_, err := buildHookSet(Config{Prefix: "x", Hooks: []Hook{c.hook}})
		if err == nil {
			t.Errorf("%s: want error, got none", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.errWant) {
			t.Errorf("%s: error %q does not contain %q", c.name, err.Error(), c.errWant)
		}
	}
}

func TestHookLabel_InErrorsUsesIDThenEventIndex(t *testing.T) {
	// id present → id
	_, err := buildHookSet(Config{Prefix: "x", Hooks: []Hook{{ID: "my-gate", Event: "bogus", Run: []string{"x"}}}})
	if err == nil || !strings.Contains(err.Error(), "my-gate") {
		t.Fatalf("error should name the hook by id: %v", err)
	}
	// id absent, event present → "<event>#<index>"
	_, err = buildHookSet(Config{Prefix: "x", Hooks: []Hook{{Event: "bogus", Run: []string{"x"}}}})
	if err == nil || !strings.Contains(err.Error(), "bogus#0") {
		t.Fatalf("error should name the hook by event#index: %v", err)
	}
}

func TestHookSet_ForEventPreservesConfigOrder(t *testing.T) {
	hs, err := buildHookSet(Config{
		Prefix: "x",
		Hooks: []Hook{
			{ID: "a", Event: "pre-close", Run: []string{"a"}},
			{ID: "b", Event: "post-close", Run: []string{"b"}},
			{ID: "c", Event: "pre-close", Run: []string{"c"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	pre := hs.forEvent("pre-close")
	if len(pre) != 2 || pre[0].id != "a" || pre[1].id != "c" {
		t.Fatalf("forEvent(pre-close) = %v, want [a c] in config order", pre)
	}
	if got := hs.forEvent("pre-update"); len(got) != 0 {
		t.Fatalf("forEvent(pre-update) = %v, want none", got)
	}
}

// L2-ish: the lazy accessor builds once and surfaces the config error. Reads
// never call hooks(), so a malformed block does not break queries — the
// end-to-end fail-closed wiring lands with the write path (Phase 6).
func TestStoreHooks_LazyBuildAndCache(t *testing.T) {
	s, err := Init(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Hooks = []Hook{{Event: "nope", Run: []string{"x"}}}

	hs1, err1 := s.hooks()
	hs2, err2 := s.hooks()
	if err1 == nil {
		t.Fatal("malformed hooks must surface a config error from hooks()")
	}
	if err1 != err2 || hs1 != hs2 {
		t.Fatal("hooks() must cache its result (build once)")
	}
	// A read still works despite the malformed config.
	if _, err := s.All(); err != nil {
		t.Fatalf("read All() must be unaffected by malformed hooks: %v", err)
	}
}
