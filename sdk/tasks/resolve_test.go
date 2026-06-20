package tasks

import (
	"errors"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/hk9890/task-manager/sdk/tasks/internal/env"
	"github.com/hk9890/task-manager/sdk/tasks/internal/vfs"
)

// --- pure helpers (L1) ----------------------------------------------------

func TestExpandHomeAndLexCanon(t *testing.T) {
	const home = "/home/u"
	cases := []struct{ in, base, want string }{
		{"~", "/x", "/home/u"},
		{"~/dev/p", "/x", "/home/u/dev/p"},
		{"/abs/p", "/x", "/abs/p"},
		{"rel/p", "/base", "/base/rel/p"},
		{"~root/p", "/x", "/x/~root/p"}, // only a leading "~" or "~/" expands
		{"/a/b/../c", "/x", "/a/c"},     // Clean
	}
	for _, c := range cases {
		if got := lexCanon(c.in, home, c.base); got != c.want {
			t.Errorf("lexCanon(%q, home, %q) = %q, want %q", c.in, c.base, got, c.want)
		}
	}
}

func TestIsAncestorOrEqual_SegmentBoundary(t *testing.T) {
	yes := [][2]string{{"/a", "/a"}, {"/a", "/a/b"}, {"/a/b", "/a/b/c"}, {"/", "/a"}}
	no := [][2]string{{"/a/project", "/a/projectX"}, {"/a/b", "/a"}, {"/x", "/y"}}
	for _, p := range yes {
		if !isAncestorOrEqual(p[0], p[1]) {
			t.Errorf("isAncestorOrEqual(%q,%q) = false, want true", p[0], p[1])
		}
	}
	for _, p := range no {
		if isAncestorOrEqual(p[0], p[1]) {
			t.Errorf("isAncestorOrEqual(%q,%q) = true, want false", p[0], p[1])
		}
	}
}

func TestLongestAncestorIndex(t *testing.T) {
	paths := []string{"/a", "/a/b", "/other"}
	if got := longestAncestorIndex("/a/b/c", paths); got != 1 {
		t.Errorf("longest = %d, want 1 (/a/b)", got)
	}
	if got := longestAncestorIndex("/a/x", paths); got != 0 {
		t.Errorf("longest = %d, want 0 (/a)", got)
	}
	if got := longestAncestorIndex("/nope", paths); got != -1 {
		t.Errorf("longest = %d, want -1", got)
	}
}

func TestValidStoreName(t *testing.T) {
	ok := []string{"a", "my-project", "Proj_1", "a.b", "x" + string(make([]byte, 0))}
	bad := []string{"", ".", "..", ".hidden", "-leading", "with/slash", "with space"}
	for _, n := range ok {
		if !validStoreName(n) {
			t.Errorf("validStoreName(%q) = false, want true", n)
		}
	}
	for _, n := range bad {
		if validStoreName(n) {
			t.Errorf("validStoreName(%q) = true, want false", n)
		}
	}
	// 64 chars ok, 65 not.
	if !validStoreName(repeat("a", 64)) {
		t.Error("64-char name should be valid")
	}
	if validStoreName(repeat("a", 65)) {
		t.Error("65-char name should be invalid")
	}
}

func TestDerivePrefix(t *testing.T) {
	cases := map[string]string{
		"/home/u/My-Project": "myprojec", // lowercased, non-alnum stripped, truncated to 8
		"/home/u/123abc":     "abc",      // leading digits removed
		"/home/u/999":        "task",     // nothing usable
	}
	for in, want := range cases {
		if got := derivePrefix(in); got != want {
			t.Errorf("derivePrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, s[0])
	}
	return string(out)
}

// --- resolution (L2, hermetic via Mem + env.Fake) -------------------------

const testHome = "/home/u"
const testCentral = "/home/u/.taskmgr"

// fakeEnv returns an env.Fake rooted at testHome with the given extra vars.
func fakeEnv(vars map[string]string) env.Fake {
	return env.Fake{Home: testHome, Vars: vars}
}

// makeStore creates a store at dir (data dir) tracking project root in m.
func makeStore(t *testing.T, m *vfs.Mem, root, dir, prefix string) {
	t.Helper()
	if _, err := initData(root, dir, prefix, m, nil); err != nil {
		t.Fatalf("initData(%s): %v", dir, err)
	}
}

// writeRegistry writes mapping.yaml under croot with the given entries.
func writeRegistry(t *testing.T, m *vfs.Mem, croot string, entries ...registryEntry) {
	t.Helper()
	if err := m.MkdirAll(croot, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", croot, err)
	}
	data, err := yaml.Marshal(registryFile{Version: 1, Stores: entries})
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	if err := m.WriteAtomic(filepath.Join(croot, registryFileName), data, 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
}

func TestResolve_LocalOnly(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/proj", "/proj/.tasks", "prj")

	s, info, err := resolveWith(ResolveOptions{WorkDir: "/proj/src/deep"}, m, fakeEnv(nil), nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if info.Kind != ResolvedLocal {
		t.Errorf("kind = %v, want ResolvedLocal", info.Kind)
	}
	if info.StorePath != "/proj/.tasks" || info.ProjectPath != "/proj" {
		t.Errorf("paths = %q / %q", info.StorePath, info.ProjectPath)
	}
	if s.Prefix() != "prj" {
		t.Errorf("prefix = %q", s.Prefix())
	}
}

func TestResolve_CentralFallback(t *testing.T) {
	m := vfs.NewMem()
	storeDir := filepath.Join(testCentral, storesSubdir, "proj")
	makeStore(t, m, "/proj", storeDir, "prj")
	writeRegistry(t, m, testCentral, registryEntry{Path: "/proj", Store: "proj"})

	s, info, err := resolveWith(ResolveOptions{WorkDir: "/proj/src"}, m, fakeEnv(nil), nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if info.Kind != ResolvedCentral {
		t.Errorf("kind = %v, want ResolvedCentral", info.Kind)
	}
	if info.StorePath != storeDir || info.ProjectPath != "/proj" {
		t.Errorf("paths = %q / %q", info.StorePath, info.ProjectPath)
	}
	// The store must be usable: its root (hook cwd) is the project, not the central dir.
	if s.Root() != "/proj" {
		t.Errorf("central store Root() = %q, want /proj (hook working dir)", s.Root())
	}
}

func TestResolve_LocalWins(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/proj", "/proj/.tasks", "loc")
	storeDir := filepath.Join(testCentral, storesSubdir, "proj")
	makeStore(t, m, "/proj", storeDir, "cen")
	writeRegistry(t, m, testCentral, registryEntry{Path: "/proj", Store: "proj"})

	_, info, err := resolveWith(ResolveOptions{WorkDir: "/proj"}, m, fakeEnv(nil), nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if info.Kind != ResolvedLocal || info.StorePath != "/proj/.tasks" {
		t.Errorf("expected local win, got kind=%v path=%q", info.Kind, info.StorePath)
	}
}

func TestResolve_LongestPrefix(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/a", filepath.Join(testCentral, storesSubdir, "a"), "aa")
	makeStore(t, m, "/a/b", filepath.Join(testCentral, storesSubdir, "ab"), "ab")
	writeRegistry(t, m, testCentral,
		registryEntry{Path: "/a", Store: "a"},
		registryEntry{Path: "/a/b", Store: "ab"},
	)
	_, info, err := resolveWith(ResolveOptions{WorkDir: "/a/b/c"}, m, fakeEnv(nil), nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if info.ProjectPath != "/a/b" {
		t.Errorf("project = %q, want /a/b (longest prefix)", info.ProjectPath)
	}
}

func TestResolve_DanglingSkipped(t *testing.T) {
	m := vfs.NewMem()
	// Registry references a store whose subfolder was never created.
	writeRegistry(t, m, testCentral, registryEntry{Path: "/proj", Store: "ghost"})
	_, _, err := resolveWith(ResolveOptions{WorkDir: "/proj"}, m, fakeEnv(nil), nil)
	if !errors.Is(err, ErrNoStore) {
		t.Errorf("dangling-only registry should yield ErrNoStore, got %v", err)
	}
}

func TestResolve_NoStore(t *testing.T) {
	m := vfs.NewMem()
	_, _, err := resolveWith(ResolveOptions{WorkDir: "/elsewhere"}, m, fakeEnv(nil), nil)
	if !errors.Is(err, ErrNoStore) {
		t.Errorf("err = %v, want ErrNoStore", err)
	}
}

func TestResolve_OverridePath_FlagAndEnv(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/ov", "/ov/store", "ovx")

	// via flag
	_, info, err := resolveWith(ResolveOptions{StorePath: "/ov/store"}, m, fakeEnv(nil), nil)
	if err != nil || info.Kind != ResolvedOverridePath || info.StorePath != "/ov/store" {
		t.Fatalf("flag override: kind=%v path=%q err=%v", info.Kind, info.StorePath, err)
	}
	// via TASKMGR_DIR
	_, info, err = resolveWith(ResolveOptions{}, m, fakeEnv(map[string]string{"TASKMGR_DIR": "/ov/store"}), nil)
	if err != nil || info.Kind != ResolvedOverridePath {
		t.Fatalf("env override: kind=%v err=%v", info.Kind, err)
	}
}

func TestResolve_OverrideName(t *testing.T) {
	m := vfs.NewMem()
	storeDir := filepath.Join(testCentral, storesSubdir, "proj")
	makeStore(t, m, "/proj", storeDir, "prj")
	writeRegistry(t, m, testCentral, registryEntry{Path: "/proj", Store: "proj"})

	_, info, err := resolveWith(ResolveOptions{StoreName: "proj", WorkDir: "/elsewhere"}, m, fakeEnv(nil), nil)
	if err != nil {
		t.Fatalf("resolve by name: %v", err)
	}
	if info.Kind != ResolvedOverrideName || info.StorePath != storeDir {
		t.Errorf("kind=%v path=%q", info.Kind, info.StorePath)
	}

	_, _, err = resolveWith(ResolveOptions{StoreName: "nope"}, m, fakeEnv(nil), nil)
	if !errors.Is(err, ErrStoreNotRegistered) {
		t.Errorf("unknown name err = %v, want ErrStoreNotRegistered", err)
	}
}

func TestResolve_AmbiguousOverride(t *testing.T) {
	m := vfs.NewMem()
	_, _, err := resolveWith(ResolveOptions{StorePath: "/x", StoreName: "y"}, m, fakeEnv(nil), nil)
	if !errors.Is(err, ErrAmbiguousOverride) {
		t.Errorf("err = %v, want ErrAmbiguousOverride", err)
	}
}

func TestStores_Enumerate(t *testing.T) {
	m := vfs.NewMem()
	makeStore(t, m, "/p1", filepath.Join(testCentral, storesSubdir, "p1"), "p1")
	makeStore(t, m, "/p2", filepath.Join(testCentral, storesSubdir, "p2"), "p2")
	writeRegistry(t, m, testCentral,
		registryEntry{Path: "/p1", Store: "p1"},
		registryEntry{Path: "/p2", Store: "p2"},
	)
	got, err := storesWith(m, fakeEnv(nil))
	if err != nil {
		t.Fatalf("stores: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].Store != "p1" || got[0].StorePath != filepath.Join(testCentral, storesSubdir, "p1") {
		t.Errorf("entry[0] = %+v", got[0])
	}
}

func TestInitCentral_CreatesAndRegisters(t *testing.T) {
	m := vfs.NewMem()
	s, err := initCentralWith("/dev/myproj", "myproj", "", m, fakeEnv(nil), nil)
	if err != nil {
		t.Fatalf("initCentral: %v", err)
	}
	wantDir := filepath.Join(testCentral, storesSubdir, "myproj")
	if s.Dir() != wantDir {
		t.Errorf("dir = %q, want %q", s.Dir(), wantDir)
	}
	if s.Prefix() != "myproj" { // derived from project base
		t.Errorf("prefix = %q, want myproj", s.Prefix())
	}
	// It must now resolve by name and by central fallback.
	_, info, err := resolveWith(ResolveOptions{WorkDir: "/dev/myproj"}, m, fakeEnv(nil), nil)
	if err != nil || info.Kind != ResolvedCentral {
		t.Fatalf("post-create resolve: kind=%v err=%v", info.Kind, err)
	}

	// Duplicate name or path is rejected.
	if _, err := initCentralWith("/dev/other", "myproj", "x", m, fakeEnv(nil), nil); !errors.Is(err, ErrStoreExists) {
		t.Errorf("duplicate name err = %v, want ErrStoreExists", err)
	}
	if _, err := initCentralWith("/dev/myproj", "myproj2", "x", m, fakeEnv(nil), nil); err == nil {
		t.Error("duplicate path should be rejected")
	}
	// Invalid store name.
	if _, err := initCentralWith("/dev/x", "bad/name", "x", m, fakeEnv(nil), nil); err == nil {
		t.Error("invalid store name should be rejected")
	}
}

func TestLoadGlobalConfig_DefaultsAndCustomRoot(t *testing.T) {
	m := vfs.NewMem()
	// missing → defaults, central root = home
	cfg, err := loadGlobalConfig(m, testHome)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if centralRoot(cfg, testHome) != testHome {
		t.Errorf("default central root = %q, want %q", centralRoot(cfg, testHome), testHome)
	}
	// custom central_root with ~ expansion
	if err := m.MkdirAll(testHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteAtomic(filepath.Join(testHome, globalConfigName), []byte("version: 1\ncentral_root: ~/stores-root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadGlobalConfig(m, testHome)
	if err != nil {
		t.Fatalf("load custom: %v", err)
	}
	if got := centralRoot(cfg, testHome); got != testHome+"/stores-root" {
		t.Errorf("custom central root = %q", got)
	}
	// corrupt → error (unterminated flow sequence is invalid YAML)
	if err := m.WriteAtomic(filepath.Join(testHome, globalConfigName), []byte("version: [1, 2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadGlobalConfig(m, testHome); err == nil {
		t.Error("corrupt config should error")
	}
}

func TestLoadRegistry_Validation(t *testing.T) {
	m := vfs.NewMem()
	// missing → empty, no error
	if entries, err := loadRegistry(m, testCentral, testHome); err != nil || entries != nil {
		t.Errorf("missing registry: entries=%v err=%v", entries, err)
	}
	// duplicate path
	writeRegistry(t, m, testCentral,
		registryEntry{Path: "/p", Store: "a"},
		registryEntry{Path: "/p", Store: "b"},
	)
	if _, err := loadRegistry(m, testCentral, testHome); err == nil {
		t.Error("duplicate path should error")
	}
	// duplicate store name
	writeRegistry(t, m, testCentral,
		registryEntry{Path: "/p1", Store: "a"},
		registryEntry{Path: "/p2", Store: "a"},
	)
	if _, err := loadRegistry(m, testCentral, testHome); err == nil {
		t.Error("duplicate store name should error")
	}
	// invalid store name
	writeRegistry(t, m, testCentral, registryEntry{Path: "/p", Store: "bad/name"})
	if _, err := loadRegistry(m, testCentral, testHome); err == nil {
		t.Error("invalid store name should error")
	}
	// missing field
	writeRegistry(t, m, testCentral, registryEntry{Path: "", Store: "a"})
	if _, err := loadRegistry(m, testCentral, testHome); err == nil {
		t.Error("missing path should error")
	}
}
