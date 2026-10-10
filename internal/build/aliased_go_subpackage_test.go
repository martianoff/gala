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

// TestBuild_AliasedGoSubpackageKeepsGoResults builds a project whose GALA
// code imports a hand-written Go package of the same module under an alias
// that differs from the package's name. A call through the alias returning
// `(T, error)` is a Try value, as it is through the package's own name: the
// generated Go wraps it in std.GoTry, and the program builds and runs.
func TestBuild_AliasedGoSubpackageKeepsGoResults(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	const moduleName = "example.com/aliasgo"
	projectDir := t.TempDir()
	for name, content := range map[string]string{
		"gala.mod":       "module " + moduleName + "\n\ngala 0.0.0\n",
		"go.mod":         "module " + moduleName + "\n\ngo 1.22\n",
		"store/store.go": "package store\n\nfunc Load() (string, error) { return \"ok\", nil }\n",
		"main.gala": `package main

import st "example.com/aliasgo/store"

func load() string = st.Load().OnFailure((err error) => {}).GetOrElse("failed")

func main() {
    Println(load())
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

	// --- Layer 1: the generated call (hermetic). ---
	require.NoError(t, b.ensureStdlib())
	require.NoError(t, b.transpileDeps())
	require.NoError(t, b.transpile())

	mainGen := readFileString(t, filepath.Join(b.workspace.GenDir, "main.gen.go"))
	assert.Contains(t, mainGen, "std.GoTry(st.Load())",
		"a (T, error) call through the alias must become a Try value:\n%s", mainGen)

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
	assert.Equal(t, "ok", strings.TrimSpace(out))
}
