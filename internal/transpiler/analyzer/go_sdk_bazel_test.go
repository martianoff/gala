package analyzer

import (
	"os"
	"path/filepath"
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
