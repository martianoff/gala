package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// compiledPackageRE matches the import path of a package that `go build -x`
// compiles (the compile tool's `-p <path> ... -pack` invocation). An assembler
// run also takes -p, but never -pack.
var compiledPackageRE = regexp.MustCompile(` -p (\S+) .*-pack`)

// TestBuildAfterTest_ReusesGoBuildCache runs `gala test` and then `gala build`
// on one project with one GOCACHE. The two commands work in different
// workspace directories. cmd/go keys a compiled package by its directory unless
// -trimpath is set, so without it the build recompiled every package the test
// run had already compiled.
//
// core is compiled identically by both commands: util imports it, and util's
// tests import util. The build must take core from the cache. util itself is
// compiled again (the test run compiled only its test variant), and so is main,
// which the test run never built.
//
// The panic happens in core, the package the build reused, and must still be
// reported at its .gala source line.
func TestBuildAfterTest_ReusesGoBuildCache(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	projectDir := t.TempDir()
	files := map[string]string{
		"gala.mod": "module example.com/reuse\n\ngala 0.0.0\n",
		// Line 4 divides by zero when n == 0.
		"core/core.gala":      "package core\n\nfunc Inverse(n int) int {\n    return 100 / n\n}\n",
		"util/util.gala":      "package util\n\nimport \"example.com/reuse/core\"\n\nfunc Scale(n int) int = core.Inverse(n) * 2\n",
		"util/util_test.gala": "package util\n\nimport . \"martianoff/gala/test\"\n\nfunc TestScale(t T) T = Eq(t, Scale(50), 4)\n",
		"main.gala":           "package main\n\nimport \"example.com/reuse/util\"\n\nfunc main() {\n    Println(util.Scale(0))\n}\n",
	}
	for name, content := range files {
		writeFixtureFile(t, filepath.Join(projectDir, filepath.FromSlash(name)), content)
	}

	isolateUserState(t)
	setEnvForTest(t, "GOCACHE", filepath.Join(t.TempDir(), "gocache"))
	alignGorootWithPathGo(t)
	chdirForTest(t, projectDir)

	tb, err := NewBuilderForMode(projectDir, "test", false, ModeTest)
	require.NoError(t, err)
	if err := tb.Test(false); err != nil {
		if isToolchainEnvError(err.Error()) {
			t.Skipf("Go toolchain unavailable/mismatched in this environment: %v", err)
		}
		t.Fatalf("gala test failed: %v", err)
	}

	bb, err := NewBuilderForMode(projectDir, "test", false, ModeBuild)
	require.NoError(t, err)
	require.NotEqual(t, tb.Workspace().Dir, bb.Workspace().Dir,
		"the test only proves something if the two commands use different workspaces")

	// The build's `go build -x` trace goes to the driver's stderr. -x is added
	// to the GOFLAGS the test run saw, so the two runs differ only in it.
	setEnvForTest(t, "GOFLAGS", strings.TrimSpace(os.Getenv("GOFLAGS")+" -x"))
	tracePath := filepath.Join(t.TempDir(), "go-build-x.txt")
	binPath, err := func() (string, error) {
		traceFile, err := os.Create(tracePath)
		require.NoError(t, err)
		defer traceFile.Close()
		origStderr := os.Stderr
		os.Stderr = traceFile
		defer func() { os.Stderr = origStderr }()
		return bb.Build("")
	}()
	if err != nil && isToolchainEnvError(err.Error()) {
		t.Skipf("Go toolchain unavailable/mismatched in this environment: %v", err)
	}
	trace := readFileString(t, tracePath)
	require.NoError(t, err, "gala build failed; go output:\n%s", trace)

	var compiled []string
	for _, m := range compiledPackageRE.FindAllStringSubmatch(trace, -1) {
		compiled = append(compiled, m[1])
	}
	require.Contains(t, compiled, "main",
		"the build must compile main, which the test run never built; is the -x trace captured?")
	for _, pkg := range compiled {
		assert.Regexp(t, `^main$|/gen/util$`, pkg,
			"gala build recompiled %s, which gala test had already compiled", pkg)
	}

	out, runErr := runBuiltBinary(binPath)
	require.Error(t, runErr, "the program must panic; output:\n%s", out)
	assert.Contains(t, out, "integer divide by zero", "output:\n%s", out)
	assert.Contains(t, out, "example.com/reuse/core/core.gala:4",
		"the reused package's frame must name its .gala source; output:\n%s", out)
	assert.Contains(t, out, "example.com/reuse/main.gala:6",
		"the user's frame must name main.gala; output:\n%s", out)
	assert.NotContains(t, out, ".gen.go", "output:\n%s", out)
}

// TestWorkspaceGoCmd_TrimpathAheadOfUserGOFLAGS checks that the driver's go
// commands get -trimpath before the user's GOFLAGS, so a user's
// -trimpath=false, read later, still turns it off.
func TestWorkspaceGoCmd_TrimpathAheadOfUserGOFLAGS(t *testing.T) {
	setEnvForTest(t, "GOFLAGS", "-p=2 -trimpath=false")
	b := &Builder{config: &Config{GoPkgDir: t.TempDir()}, workspace: &Workspace{Dir: t.TempDir()}}

	cmd := b.workspaceGoCmd("build", "./gen/...")

	assert.Equal(t, []string{"go", "build", "./gen/..."}, cmd.Args)
	assert.Equal(t, "GOFLAGS=-trimpath -p=2 -trimpath=false", cmd.Env[len(cmd.Env)-1])
}
