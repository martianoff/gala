package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuild_StackTracesMapToGalaSource_UserAndImportedPackage is the end-to-end
// acceptance test for source-mapped stack traces through the `gala build`
// pipeline (Builder.Build is exactly what the `gala build` CLI command drives).
//
// It builds a two-module project where a panic (divide by zero) ORIGINATES in an
// imported GALA library function and propagates OUT through the user's own code:
//
//	boomlib/boom.gala   BoomInLib(n) => 100 / n      (panics on line 4)
//	boomapp/main.gala   callLib()    => BoomInLib(0)  (user frame, line 6)
//	                    main()       => callLib()      (user frame, line 10)
//
// The single panic's stack trace therefore contains BOTH an imported-library
// frame and user-code frames. The test asserts every frame reports a `.gala`
// source position — the library's boom.gala for the panic origin AND the user's
// main.gala for the callers — and that no generated-Go (`.gen.go`) position
// leaks through.
//
// Two layers of proof:
//  1. Pipeline-level (always runs, CI-safe): after transpilation, the generated
//     .gen.go for BOTH the consumer and the imported library carry `//line
//     <file>.gala:<n>` directives and no leftover `__gala_line_` markers.
//  2. Binary-level (the real proof, when a usable Go toolchain is present): the
//     built executable's panic trace shows the GALA positions. If the Go build
//     cannot run in this environment (no toolchain / hermetic-SDK mismatch /
//     offline toolchain switch), that half is skipped rather than reported as a
//     feature failure.
func TestBuild_StackTracesMapToGalaSource_UserAndImportedPackage(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	tmp := t.TempDir()

	// Imported GALA library. `return 100 / n` is line 4 and panics when n == 0.
	libDir := filepath.Join(tmp, "boomlib")
	require.NoError(t, os.MkdirAll(libDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(libDir, "gala.mod"),
		[]byte("module example.com/boomlib\n\ngala 0.0.0\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(libDir, "boom.gala"),
		[]byte("package boomlib\n\nfunc BoomInLib(n int) int {\n    return 100 / n\n}\n"), 0o644))

	// Consumer main module using the lib via a local replace.
	consumerDir := filepath.Join(tmp, "boomapp")
	require.NoError(t, os.MkdirAll(consumerDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "gala.mod"),
		[]byte("module example.com/boomapp\n\ngala 0.0.0\n\nrequire example.com/boomlib v0.0.0\n\nreplace example.com/boomlib => ../boomlib\n"),
		0o644))
	// main.gala: line 6 is `return BoomInLib(0)`, line 10 is `Println(callLib())`.
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "main.gala"),
		[]byte("package main\n\nimport . \"example.com/boomlib\"\n\nfunc callLib() int {\n    return BoomInLib(0)\n}\n\nfunc main() {\n    Println(callLib())\n}\n"),
		0o644))

	isolateUserState(t)
	// Isolating HOME/USERPROFILE removes the default GOCACHE location
	// (%LocalAppData% on Windows), so point GOCACHE at a throwaway dir — the
	// `go build` inside Builder.Build needs a writable build cache.
	setEnvForTest(t, "GOCACHE", filepath.Join(t.TempDir(), "gocache"))
	chdirForTest(t, consumerDir)

	b, err := NewBuilder(consumerDir, "test", false)
	require.NoError(t, err)
	require.NoError(t, b.workspace.Ensure())

	// --- Layer 1: pipeline-level proof on the generated .gen.go files. ---
	require.NoError(t, b.ensureStdlib())
	require.NoError(t, b.transpileDeps())
	require.NoError(t, b.transpile())

	consumerGen := readFileString(t, filepath.Join(b.workspace.GenDir, "main.gen.go"))
	assert.Contains(t, consumerGen, "//line ",
		"consumer main.gen.go should carry //line directives:\n%s", consumerGen)
	assert.Contains(t, consumerGen, "main.gala:",
		"consumer directives should map to the user's main.gala:\n%s", consumerGen)
	assert.NotContains(t, consumerGen, "__gala_line_",
		"no raw markers should remain in consumer main.gen.go:\n%s", consumerGen)

	libGen := findGenFile(t, b.workspace.Dir, "boom.gen.go")
	assert.Contains(t, libGen, "//line ",
		"imported library boom.gen.go should carry //line directives:\n%s", libGen)
	assert.Contains(t, libGen, "boom.gala:",
		"imported library directives should map to the library's boom.gala:\n%s", libGen)
	assert.NotContains(t, libGen, "__gala_line_",
		"no raw markers should remain in imported library boom.gen.go:\n%s", libGen)

	// --- Layer 2: binary-level proof — build and run through Builder.Build. ---
	binPath, buildErr := b.Build("")
	if buildErr != nil {
		// A Go toolchain/version/offline problem is an environment limitation,
		// not a feature failure — the pipeline-level assertions above already
		// proved the directives are emitted. Skip rather than red.
		if isToolchainEnvError(buildErr.Error()) {
			t.Skipf("skipping binary-level check: Go toolchain unavailable/mismatched in this environment: %v", buildErr)
		}
		t.Fatalf("gala build failed: %v", buildErr)
	}
	require.NotEmpty(t, binPath)

	out, runErr := runBuiltBinary(binPath)
	require.Error(t, runErr, "the program must panic; output:\n%s", out)

	assert.Contains(t, out, "integer divide by zero",
		"expected a divide-by-zero panic; output:\n%s", out)
	assert.Contains(t, out, "boom.gala:4",
		"imported-library frame must map to the library's boom.gala source; output:\n%s", out)
	assert.Contains(t, out, "main.gala:",
		"user-code frame must map to the user's main.gala source; output:\n%s", out)
	assert.NotContains(t, out, ".gen.go",
		"stack trace must not report generated-Go positions; output:\n%s", out)
}

// isolateUserState points HOME/USERPROFILE at a throwaway dir so the test never
// touches the developer's real ~/.gala cache.
func isolateUserState(t *testing.T) {
	t.Helper()
	isolated := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE"} {
		orig, had := os.LookupEnv(key)
		require.NoError(t, os.Setenv(key, isolated))
		key := key
		t.Cleanup(func() {
			if had {
				os.Setenv(key, orig)
			} else {
				os.Unsetenv(key)
			}
		})
	}
}

// setEnvForTest sets an environment variable for the duration of the test and
// restores the prior value on cleanup.
func setEnvForTest(t *testing.T, key, value string) {
	t.Helper()
	orig, had := os.LookupEnv(key)
	require.NoError(t, os.Setenv(key, value))
	t.Cleanup(func() {
		if had {
			os.Setenv(key, orig)
		} else {
			os.Unsetenv(key)
		}
	})
}

// chdirForTest changes the working directory for the duration of the test so the
// resolver's module discovery walks up from the project dir.
func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "reading %s", path)
	return string(data)
}

// findGenFile locates a generated .gen.go file by base name anywhere under root
// (the transpiled dependency lands in the workspace deps tree).
func findGenFile(t *testing.T, root, base string) string {
	t.Helper()
	var found string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if filepath.Base(path) == base {
			found = path
		}
		return nil
	})
	require.NotEmpty(t, found, "generated file %q not found under %s", base, root)
	return readFileString(t, found)
}

func runBuiltBinary(binPath string) (string, error) {
	cmd := exec.Command(binPath)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// isToolchainEnvError reports whether a build error is an environment/toolchain
// problem (rather than a defect in the generated code): tool/std version
// mismatch, missing toolchain, or an offline toolchain switch.
func isToolchainEnvError(msg string) bool {
	m := strings.ToLower(msg)
	for _, needle := range []string{
		"does not match go tool version",
		"go tool version",
		"downloading",
		"toolchain",
		"cannot find go",
		"executable file not found",
		"no such file or directory",
	} {
		if strings.Contains(m, needle) {
			return true
		}
	}
	return false
}
