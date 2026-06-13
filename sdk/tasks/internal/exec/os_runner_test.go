//go:build unix

package exec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests exercise the real OS runner by spawning tiny, deterministic
// processes (sh, true, sleep). They are fast (each process is sub-10ms) and
// prove the seam's exit-code, stdin, env, timeout, and spawn-failure mechanics
// that the Fake cannot. HOOK-SPEC §6.1/§7.

func TestOSRunner_AllowExitZero(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "exit 0"}})
	if res.Category != Completed || res.ExitCode != 0 {
		t.Fatalf("got category=%v exit=%d, want Completed/0", res.Category, res.ExitCode)
	}
}

func TestOSRunner_DenyExitCode(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "echo nope >&2; exit 3"}})
	if res.Category != Completed || res.ExitCode != 3 {
		t.Fatalf("got category=%v exit=%d, want Completed/3", res.Category, res.ExitCode)
	}
	if got := strings.TrimSpace(string(res.Stderr)); got != "nope" {
		t.Fatalf("stderr = %q, want %q", got, "nope")
	}
}

func TestOSRunner_CapturesStdout(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "echo hello"}})
	if got := strings.TrimSpace(string(res.Stdout)); got != "hello" {
		t.Fatalf("stdout = %q, want %q", got, "hello")
	}
}

func TestOSRunner_StdinDelivered(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "cat"}, Stdin: []byte("payload-123")})
	if got := string(res.Stdout); got != "payload-123" {
		t.Fatalf("stdout = %q, want stdin echoed back", got)
	}
}

func TestOSRunner_EnvExtrasLayered(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{
		Argv: []string{"sh", "-c", "printf %s \"$TASKMGR_HOOK_EVENT\""},
		Env:  []string{"TASKMGR_HOOK_EVENT=pre-close"},
	})
	if got := string(res.Stdout); got != "pre-close" {
		t.Fatalf("env var not delivered: stdout = %q", got)
	}
}

func TestOSRunner_InheritsParentEnv(t *testing.T) {
	t.Setenv("TASKMGR_OS_RUNNER_PROBE", "inherited")
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "printf %s \"$TASKMGR_OS_RUNNER_PROBE\""}})
	if got := string(res.Stdout); got != "inherited" {
		t.Fatalf("parent env not inherited: stdout = %q", got)
	}
}

func TestOSRunner_WorkingDir(t *testing.T) {
	dir := t.TempDir()
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "pwd"}, Dir: dir})
	// macOS /tmp is a symlink to /private/tmp; compare resolved suffix.
	got := strings.TrimSpace(string(res.Stdout))
	if !strings.HasSuffix(got, filepath.Base(dir)) {
		t.Fatalf("pwd = %q, want a dir ending in %q", got, filepath.Base(dir))
	}
}

func TestOSRunner_SpawnErrorMissingBinary(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"this-binary-does-not-exist-xyzzy"}})
	if res.Category != SpawnError {
		t.Fatalf("got category=%v, want SpawnError", res.Category)
	}
	if res.Err == nil {
		t.Fatal("SpawnError must carry a diagnostic Err")
	}
}

func TestOSRunner_EmptyArgvIsSpawnError(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: nil})
	if res.Category != SpawnError {
		t.Fatalf("got category=%v, want SpawnError", res.Category)
	}
}

func TestOSRunner_Timeout(t *testing.T) {
	r := NewOS()
	start := time.Now()
	res := r.Run(Spec{Argv: []string{"sleep", "30"}, Timeout: 100 * time.Millisecond})
	elapsed := time.Since(start)
	if res.Category != Timeout {
		t.Fatalf("got category=%v, want Timeout", res.Category)
	}
	// Must return promptly after the timeout (well under the 30s sleep, allowing
	// for the SIGTERM->SIGKILL grace).
	if elapsed > KillGrace+5*time.Second {
		t.Fatalf("timeout took %v, expected prompt kill", elapsed)
	}
}

func TestOSRunner_TimeoutZeroDisables(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "exit 0"}, Timeout: 0})
	if res.Category != Completed {
		t.Fatalf("got category=%v, want Completed (no timeout)", res.Category)
	}
}

func TestOSRunner_DurationRecorded(t *testing.T) {
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"sh", "-c", "exit 0"}})
	if res.Duration <= 0 {
		t.Fatalf("duration = %v, want > 0", res.Duration)
	}
}

// Sanity: the seam is the only place that reads os.Environ; confirm it actually
// does by checking PATH is present (so a bare argv like "sh" resolves).
func TestOSRunner_PathResolvesBareCommand(t *testing.T) {
	if os.Getenv("PATH") == "" {
		t.Skip("no PATH in environment")
	}
	r := NewOS()
	res := r.Run(Spec{Argv: []string{"true"}})
	if res.Category != Completed || res.ExitCode != 0 {
		t.Fatalf("bare 'true' did not resolve via PATH: category=%v exit=%d", res.Category, res.ExitCode)
	}
}
