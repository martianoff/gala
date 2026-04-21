package build

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// TestEnsureGoToolchain_Present verifies the happy path: when `go` is on PATH
// (which it is in CI since we build with Bazel + Go), the check passes with no
// error.
func TestEnsureGoToolchain_Present(t *testing.T) {
	if err := ensureGoToolchain(); err != nil {
		t.Fatalf("expected Go toolchain to be available, got error: %v", err)
	}
}

// TestEnsureGoToolchain_Missing verifies that when `go` is not on PATH the
// check returns a clear, actionable error message that explains the Go
// prerequisite. This is gap #3 from the pre-built release user-journey report:
// previously users saw the raw exec.ErrNotFound message.
func TestEnsureGoToolchain_Missing(t *testing.T) {
	// Save and clear PATH so exec.LookPath("go") cannot find the toolchain.
	origPath, hadPath := os.LookupEnv("PATH")
	t.Cleanup(func() {
		if hadPath {
			os.Setenv("PATH", origPath)
		} else {
			os.Unsetenv("PATH")
		}
	})

	if err := os.Setenv("PATH", ""); err != nil {
		t.Fatalf("clearing PATH: %v", err)
	}

	// On Windows exec.LookPath also consults PATHEXT; clearing PATH alone is
	// enough since LookPath always resolves relative to PATH, but we clear
	// PATHEXT too to eliminate any platform-specific edge cases.
	if runtime.GOOS == "windows" {
		origPathExt, hadPathExt := os.LookupEnv("PATHEXT")
		t.Cleanup(func() {
			if hadPathExt {
				os.Setenv("PATHEXT", origPathExt)
			} else {
				os.Unsetenv("PATHEXT")
			}
		})
		os.Unsetenv("PATHEXT")
	}

	err := ensureGoToolchain()
	if err == nil {
		t.Fatal("expected error when Go toolchain is missing from PATH, got nil")
	}

	msg := err.Error()
	// The error must explicitly mention Go and provide an install pointer so
	// the user knows how to fix the problem without consulting docs.
	wantSubstrings := []string{
		"Go",
		"PATH",
		"https://go.dev/dl/",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q\nfull message: %s", want, msg)
		}
	}
}
