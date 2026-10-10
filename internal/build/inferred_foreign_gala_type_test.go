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

// TestBuild_InferredGalaTypeOfAnotherPackage builds a project whose GALA code
// reaches a GALA struct of another package only through inference: a
// package-level map comes from an unexported hand-written Go function, and an
// unannotated lambda consumes its values. The lambda parameter must be typed
// by that struct, qualified, with its package imported, not by a type
// parameter of the method it is passed to.
func TestBuild_InferredGalaTypeOfAnotherPackage(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	const moduleName = "example.com/inferredforeign"
	projectDir := t.TempDir()
	for name, content := range map[string]string{
		"gala.mod":     "module " + moduleName + "\n\ngala 0.0.0\n",
		"go.mod":       "module " + moduleName + "\n\ngo 1.22\n",
		"sub/sub.gala": "package sub\n\nstruct Command(var Name string)\n",
		"helper.go": `package main

import "example.com/inferredforeign/sub"

func newMap() map[string]sub.Command {
	return map[string]sub.Command{"x": sub.Command{Name: "run"}}
}
`,
		"main.gala": `package main

import "martianoff/gala/go_interop"

val commands = newMap()

func main() {
    go_interop.OptionFromMap(commands, "x").ForEach((c) => Println(c.Name))
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

	// --- Layer 1: the lambda parameter type (hermetic). ---
	require.NoError(t, b.ensureStdlib())
	require.NoError(t, b.transpileDeps())
	require.NoError(t, b.transpile())

	mainGen := readFileString(t, filepath.Join(b.workspace.GenDir, "main.gen.go"))
	assert.Contains(t, mainGen, "func(c sub.Command)",
		"the lambda parameter must be the inferred struct, qualified:\n%s", mainGen)
	assert.NotContains(t, mainGen, "func(c T)",
		"a type parameter of ForEach must not reach the generated Go:\n%s", mainGen)

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
	assert.Equal(t, "run", strings.TrimSpace(out))
}
