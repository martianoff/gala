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

// TestBuild_GoSubpackageTypeReachedThroughInference builds a project whose
// GALA code uses a type from a hand-written Go package inside the same module,
// where the type reaches the GALA code only through inference: a match whose
// arms call the Go constructor, so the generated Go has to name *box.Box
// itself.
//
// The generated Go must import that package by its module import path (which
// the build then redirects into the workspace gen/ tree). It used to import it
// by the directory the analyzer type-checked — unparseable Go on Windows
// (`import "C:\Users\…\box"`), and "not a package path" to `go` elsewhere.
func TestBuild_GoSubpackageTypeReachedThroughInference(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	const moduleName = "example.com/gosubpkg"
	projectDir := t.TempDir()
	for name, content := range map[string]string{
		"gala.mod":   "module " + moduleName + "\n\ngala 0.0.0\n",
		"go.mod":     "module " + moduleName + "\n\ngo 1.22\n",
		"box/box.go": "package box\n\ntype Box struct{ Size int }\n\nfunc New(size int) *Box { return &Box{Size: size} }\n",
		"main.gala": `package main

import "example.com/gosubpkg/box"

func open(o Option[int]) *box.Box {
    val b = o match {
        case Some(n) => box.New(n)
        case None() => box.New(0)
    }
    return b
}

func main() {
    Println(s"size: ${open(Some(3)).Size}")
}
`,
	} {
		p := filepath.Join(projectDir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}

	isolateUserState(t)
	setEnvForTest(t, "GOCACHE", filepath.Join(t.TempDir(), "gocache"))
	alignGorootWithPathGo(t)
	chdirForTest(t, projectDir)

	b, err := NewBuilder(projectDir, "test", false)
	require.NoError(t, err)
	require.NoError(t, b.workspace.Ensure())

	// --- Layer 1: the generated import (hermetic). ---
	require.NoError(t, b.ensureStdlib())
	require.NoError(t, b.transpileDeps())
	require.NoError(t, b.transpile())

	mainGen := readFileString(t, filepath.Join(b.workspace.GenDir, "main.gen.go"))
	assert.Contains(t, mainGen, "\"gala-build-workspace/gen/box\"",
		"the Go subpackage must be imported by its module path, redirected into gen/:\n%s", mainGen)
	assert.NotContains(t, mainGen, filepath.ToSlash(projectDir)+"/box\"",
		"the Go subpackage must not be imported by its directory:\n%s", mainGen)
	assert.NotContains(t, mainGen, filepath.Join(projectDir, "box"),
		"the Go subpackage must not be imported by its directory:\n%s", mainGen)

	// --- Layer 2: the whole `gala build` pipeline. ---
	binPath, buildErr := b.Build("")
	if buildErr != nil {
		if isToolchainEnvError(buildErr.Error()) {
			t.Skipf("skipping end-to-end check: Go toolchain unavailable/mismatched in this environment: %v", buildErr)
		}
		t.Fatalf("gala build failed: %v", buildErr)
	}
	out, runErr := runBuiltBinary(binPath)
	require.NoError(t, runErr, "built binary failed to run; output:\n%s", out)
	assert.Equal(t, "size: 3", strings.TrimSpace(out))
}
