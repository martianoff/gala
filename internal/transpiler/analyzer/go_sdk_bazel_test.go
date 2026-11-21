package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Under Bazel, transpilation must resolve GOROOT from the hermetic Go SDK that
// rules_go materializes in the execution root, not from whatever `go` is on the
// host PATH — that keeps type inference on the exact Go version Bazel compiles
// the generated code with. findBazelGoSDK implements that discovery.
func TestFindBazelGoSDKFrom(t *testing.T) {
	// Simulate a Bazel execution root: external/<repo whose name contains
	// "go_sdk">/src/fmt. A sibling go_sdk-named repo without a valid SDK layout
	// (e.g. rules_go's nogo repo) must be ignored.
	root := t.TempDir()
	sdk := filepath.Join(root, "external", "rules_go++go_sdk+main___download_0")
	mustMkdir(t, filepath.Join(sdk, "src", "fmt"))
	mustMkdir(t, filepath.Join(root, "external", "rules_go++go_sdk+io_bazel_rules_nogo"))

	if got := findBazelGoSDKFrom([]string{root}); got != sdk {
		t.Fatalf("findBazelGoSDKFrom(execroot) = %q, want %q", got, sdk)
	}

	// Walking up should also find it from a nested working directory inside the
	// execution root (e.g. a package subdirectory).
	nested := filepath.Join(root, "examples", "deep", "pkg")
	mustMkdir(t, nested)
	if got := findBazelGoSDKFrom([]string{nested}); got != sdk {
		t.Fatalf("findBazelGoSDKFrom(nested) = %q, want %q", got, sdk)
	}

	// Legacy WORKSPACE layout uses the fixed repo name `external/go_sdk`.
	legacyRoot := t.TempDir()
	legacy := filepath.Join(legacyRoot, "external", "go_sdk")
	mustMkdir(t, filepath.Join(legacy, "src", "fmt"))
	if got := findBazelGoSDKFrom([]string{legacyRoot}); got != legacy {
		t.Fatalf("findBazelGoSDKFrom(legacy) = %q, want %q", got, legacy)
	}

	// Outside any Bazel execution root, it must return "" so host-Go discovery
	// (GOROOT env / PATH) applies instead.
	if got := findBazelGoSDKFrom([]string{t.TempDir()}); got != "" {
		t.Fatalf("findBazelGoSDKFrom(non-bazel) = %q, want empty", got)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// gorootFromGoBinary must derive a valid GOROOT even when the `go` on PATH is a
// symlink into the real SDK — as with Homebrew, where /opt/homebrew/bin/go points
// into .../Cellar/go/<ver>/libexec/bin/go. Deriving parent-of-bin from the
// unresolved symlink yields a non-SDK directory; resolving the link recovers the
// real root.
func TestGorootFromGoBinarySymlink(t *testing.T) {
	root := t.TempDir()

	// Build a fake SDK at <root>/sdk with bin/go and src/fmt.
	realRoot := filepath.Join(root, "sdk")
	realBin := filepath.Join(realRoot, "bin")
	mustMkdir(t, realBin)
	mustMkdir(t, filepath.Join(realRoot, "src", "fmt"))
	realGo := filepath.Join(realBin, "go")
	if err := os.WriteFile(realGo, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A PATH-style symlink whose naive parent-of-bin is NOT a Go SDK.
	linkBin := filepath.Join(root, "bin")
	mustMkdir(t, linkBin)
	linkGo := filepath.Join(linkBin, "go")
	if err := os.Symlink(realGo, linkGo); err != nil {
		t.Fatal(err)
	}

	// Sanity: the unresolved guess must be invalid, so the test exercises the
	// symlink-resolution path rather than the trivial first candidate.
	if isGoRoot(filepath.Dir(linkBin)) {
		t.Fatalf("precondition failed: %q unexpectedly looks like a Go SDK", filepath.Dir(linkBin))
	}

	want, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	got := gorootFromGoBinary(linkGo)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != want {
		t.Fatalf("gorootFromGoBinary(symlink) = %q (resolved %q), want %q", got, gotResolved, want)
	}
}

// A go on PATH older than the go line GALA's go.mod files declare does not
// build GALA's code: the go command switches to a newer toolchain (#1322,
// where go1.23.4 on PATH hid strings.SplitSeq from type inference while the
// build compiled with go1.25). switchedGoRoot must then take the root the go
// command reports in a module with that go line, and keep an SDK the go
// command surely builds with.
func TestSwitchedGoRoot(t *testing.T) {
	root := t.TempDir()
	sdk := func(name, version string) string {
		dir := filepath.Join(root, name)
		mustMkdir(t, filepath.Join(dir, "src", "fmt"))
		if version != "" {
			if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte(version+"\ntime 2024-01-01T00:00:00Z\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	old, current := sdk("old", "go1.23.4"), sdk("current", "go"+GeneratedGoVersion+".5")
	switched := sdk("switched", "go"+GeneratedGoVersion+".0")

	var goModSeen string
	goEnv := func(dir string) string {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			t.Fatalf("go env GOROOT ran outside a module: %v", err)
		}
		goModSeen = string(data)
		return switched
	}
	if got := switchedGoRoot(old, "", goEnv); got != switched {
		t.Errorf("switchedGoRoot(go1.23.4) = %q, want the switched toolchain %q", got, switched)
	}
	if want := "\ngo " + GeneratedGoVersion + "\n"; !strings.Contains(goModSeen, want) {
		t.Errorf("go env GOROOT ran with go.mod %q, want one containing %q", goModSeen, want)
	}
	if got := switchedGoRoot(current, "go1.26.0", goEnv); got != switched {
		t.Errorf("switchedGoRoot(%q) with GOTOOLCHAIN=go1.26.0 = %q, want %q", current, got, switched)
	}
	if got := switchedGoRoot(current, "auto", func(string) string {
		t.Error("go env GOROOT ran for an SDK that needs no toolchain switch")
		return switched
	}); got != current {
		t.Errorf("switchedGoRoot(%q) = %q, want it unchanged", current, got)
	}
	// The go command cannot tell (GOTOOLCHAIN=local fails, say): keep the root.
	if got := switchedGoRoot(old, "", func(string) string { return "" }); got != old {
		t.Errorf("switchedGoRoot with no answer = %q, want %q", got, old)
	}
}

// toolchainMaySwitch is false only where the go command surely builds a
// `go 1.25` module with its own toolchain.
func TestToolchainMaySwitch(t *testing.T) {
	newer := "go" + GeneratedGoVersion + ".5"
	cases := []struct {
		host, gotoolchain string
		want              bool
	}{
		{"go1.23.4", "", true},
		{"go1.23.4", "auto", true},
		{"go1.23.4", "local", false},
		{newer, "", false},
		{newer, "auto", false},
		{newer, "path", false},
		{newer, newer, false},
		{newer, newer + "+auto", false},
		{newer, "go1.26.0", true},
		{newer, "go1.26.0+auto", true},
		{"", "", true},
		{"", "local", false},
	}
	for _, c := range cases {
		if got := toolchainMaySwitch(c.host, c.gotoolchain); got != c.want {
			t.Errorf("toolchainMaySwitch(%q, %q) = %t, want %t", c.host, c.gotoolchain, got, c.want)
		}
	}
}

// goToolchain reads GOTOOLCHAIN as the go command does: the environment, else
// the user's go env file, else the SDK's go.env.
func TestGoToolchain(t *testing.T) {
	dir := t.TempDir()
	userFile := filepath.Join(dir, "user.env")
	sdkRoot := filepath.Join(dir, "sdk")
	mustMkdir(t, sdkRoot)
	if err := os.WriteFile(filepath.Join(sdkRoot, "go.env"), []byte("GOPROXY=direct\nGOTOOLCHAIN=auto\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOENV", userFile)
	t.Setenv("GOTOOLCHAIN", "")
	if got := goToolchain(sdkRoot); got != "auto" {
		t.Errorf("SDK go.env only: goToolchain = %q, want auto", got)
	}
	if err := os.WriteFile(userFile, []byte("GOTOOLCHAIN=go1.26.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := goToolchain(sdkRoot); got != "go1.26.0" {
		t.Errorf("user go env file: goToolchain = %q, want go1.26.0", got)
	}
	t.Setenv("GOTOOLCHAIN", "local")
	if got := goToolchain(sdkRoot); got != "local" {
		t.Errorf("environment: goToolchain = %q, want local", got)
	}
}

