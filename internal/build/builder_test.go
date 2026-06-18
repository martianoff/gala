package build

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/mod"
)

// TestTest_NoTestFilesExitsZero verifies that running `gala test` on a project
// with no _test.gala files exits successfully (with a "no test files" notice)
// rather than failing. This matches Go's `go test ./...` behavior.
func TestTest_NoTestFilesExitsZero(t *testing.T) {
	projectDir := t.TempDir()

	// Minimal gala.mod
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "gala.mod"),
		[]byte("module example.com/demo\n\ngala 0.0.0\n"), 0644))

	// A single main.gala file with no tests
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "main.gala"),
		[]byte("package main\n\nfunc main() { Println(\"hi\") }\n"), 0644))

	origHome, hadHome := os.LookupEnv("HOME")
	origUserProfile, hadProfile := os.LookupEnv("USERPROFILE")
	isolated := t.TempDir()
	os.Setenv("HOME", isolated)
	os.Setenv("USERPROFILE", isolated)
	t.Cleanup(func() {
		if hadHome {
			os.Setenv("HOME", origHome)
		} else {
			os.Unsetenv("HOME")
		}
		if hadProfile {
			os.Setenv("USERPROFILE", origUserProfile)
		} else {
			os.Unsetenv("USERPROFILE")
		}
	})

	b, err := NewBuilder(projectDir, "test", false)
	require.NoError(t, err)

	// Capture stdout so the test can assert on the friendly notice.
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	testErr := b.Test(false)

	// Restore stdout and read what was written.
	_ = w.Close()
	os.Stdout = origStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	require.NoError(t, testErr, "Test() must not return an error when there are no _test.gala files")
	require.True(t, strings.Contains(buf.String(), "[no test files]"),
		"expected '[no test files]' notice in stdout, got: %q", buf.String())
}

// TestRenameUserMainInDir_RenamesOnlyListedFiles verifies that the test-binary
// build path renames the user's `func main()` out of the way so the
// synthesized test runner's main() can compile in the same package.
func TestRenameUserMainInDir_RenamesOnlyListedFiles(t *testing.T) {
	dir := t.TempDir()

	userMain := `package main

func main() {
	Println("hi")
}
`
	userLib := `package main

func helper() int { return 1 }
`
	testFile := `package main

func TestX() {}
`

	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.gen.go"), []byte(userMain), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib.gen.go"), []byte(userLib), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main_test.gen.go"), []byte(testFile), 0644))

	sourceNames := map[string]bool{
		"main.gen.go": true,
		"lib.gen.go":  true,
	}

	require.NoError(t, renameUserMainInDir(dir, sourceNames, false))

	mainAfter, err := os.ReadFile(filepath.Join(dir, "main.gen.go"))
	require.NoError(t, err)
	require.False(t, strings.Contains(string(mainAfter), "func main()"),
		"user func main() should have been renamed, got:\n%s", mainAfter)
	require.True(t, strings.Contains(string(mainAfter), "func _galaUserMain()"),
		"renamed function should be named _galaUserMain, got:\n%s", mainAfter)

	libAfter, err := os.ReadFile(filepath.Join(dir, "lib.gen.go"))
	require.NoError(t, err)
	require.Equal(t, userLib, string(libAfter), "lib.gen.go should be unchanged")

	testAfter, err := os.ReadFile(filepath.Join(dir, "main_test.gen.go"))
	require.NoError(t, err)
	require.Equal(t, testFile, string(testAfter), "test file should be unchanged")
}

// TestTestGenFileName verifies that the helper matches the naming used by
// transpileFilesToDir. Subdirectory layout is preserved so that each GALA
// subpackage lands in its own gen/<sub>/ folder and Go can resolve
// cross-package imports without flattening.
func TestTestGenFileName(t *testing.T) {
	projectDir := filepath.Clean("/p")
	cases := []struct {
		name string
		gala string
		want string
	}{
		{"root file", filepath.Join(projectDir, "main.gala"), "main.gen.go"},
		{"subdir file", filepath.Join(projectDir, "sub", "lib.gala"), filepath.Join("sub", "lib.gen.go")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := testGenFileName(projectDir, tc.gala)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestFindGalaFilesRecursive_IncludesSubdirs verifies that the recursive
// file walker used by `gala build` discovers .gala files in subpackages.
func TestFindGalaFilesRecursive_IncludesSubdirs(t *testing.T) {
	projectDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "main.gala"),
		[]byte("package main\nfunc main() {}"), 0644))

	subDir := filepath.Join(projectDir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "lib.gala"),
		[]byte("package sub\nfunc Hello() string = \"hi\""), 0644))

	// _test.gala must NOT be picked up by build-path recursion.
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "extra_test.gala"),
		[]byte("package sub\nfunc TestX() {}"), 0644))

	// Hidden/build-cache directories must be skipped.
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".gala"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, ".gala", "cached.gala"),
		[]byte("package whatever"), 0644))

	got, err := findGalaFilesRecursive(projectDir)
	require.NoError(t, err)

	sort.Strings(got)
	want := []string{
		filepath.Join(projectDir, "main.gala"),
		filepath.Join(subDir, "lib.gala"),
	}
	sort.Strings(want)
	require.Equal(t, want, got)
}

// TestFindGalaTestFilesRecursive_IncludesSubdirs verifies that the recursive
// test-file walker used by `gala test` discovers _test.gala files in
// subpackages so that a library layout with state/state_test.gala is not
// silently ignored.
func TestFindGalaTestFilesRecursive_IncludesSubdirs(t *testing.T) {
	projectDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "root_test.gala"),
		[]byte("package repro\nfunc TestRoot() {}"), 0644))

	subDir := filepath.Join(projectDir, "state")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "state_test.gala"),
		[]byte("package state\nfunc TestState() {}"), 0644))

	// Non-test files must NOT be returned.
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "state.gala"),
		[]byte("package state\nfunc StateFn() string = \"hi\""), 0644))

	got, err := findGalaTestFilesRecursive(projectDir)
	require.NoError(t, err)

	sort.Strings(got)
	want := []string{
		filepath.Join(projectDir, "root_test.gala"),
		filepath.Join(subDir, "state_test.gala"),
	}
	sort.Strings(want)
	require.Equal(t, want, got)
}

// TestRootPackageName_PrefersRootDirFile verifies that classification of the
// project kind (main vs library) looks at a file that actually lives in the
// project root — a subpackage named `main` (unusual, but possible) must not
// flip the whole project into main-package mode.
func TestRootPackageName_PrefersRootDirFile(t *testing.T) {
	projectDir := filepath.Clean("/p")
	sources := []string{
		filepath.Join(projectDir, "sub", "lib.gala"),
		filepath.Join(projectDir, "root.gala"),
	}

	// rootPackageName opens the file to read the package clause, so we can't
	// test against synthetic paths directly. Build a real temp layout.
	tmp := t.TempDir()
	rootFile := filepath.Join(tmp, "root.gala")
	require.NoError(t, os.WriteFile(rootFile, []byte("package repro\n"), 0644))
	subDir := filepath.Join(tmp, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	subFile := filepath.Join(subDir, "lib.gala")
	require.NoError(t, os.WriteFile(subFile, []byte("package sub\n"), 0644))

	// Iteration order matters: put the subpackage first to ensure we still
	// surface the root package name.
	got := rootPackageName([]string{subFile, rootFile}, tmp)
	require.Equal(t, "repro", got, "root package should be preferred over sub packages")

	// Sanity check that the test's projectDir/sources vars compile (kept to
	// satisfy the linter and document intent).
	_ = sources
	_ = projectDir
}

// TestResolveEffectiveDepDir_LocalReplace verifies that a `replace` directive
// pointing to a local path short-circuits the module cache lookup.
func TestResolveEffectiveDepDir_LocalReplace(t *testing.T) {
	projectDir := t.TempDir()
	localDep := filepath.Join(projectDir, "..", "parent")

	galaMod := &mod.File{
		Module: mod.Module{Path: "example.com/sub"},
		Require: []mod.Require{
			{Path: "example.com/parent", Version: "v0.0.0"},
		},
		Replace: []mod.Replace{
			{
				Old: mod.ModuleVersion{Path: "example.com/parent"},
				New: mod.ModuleVersion{Path: "../parent"},
			},
		},
	}

	config := DefaultConfig()
	got := resolveEffectiveDepDir(config, galaMod, projectDir,
		mod.Require{Path: "example.com/parent", Version: "v0.0.0"})

	want := filepath.Clean(localDep)
	require.Equal(t, want, got, "local replace must resolve to the replacement path")
}

// TestResolveEffectiveDepDir_NoMatch falls back to the module cache when no
// replace entry applies.
func TestResolveEffectiveDepDir_NoMatch(t *testing.T) {
	projectDir := t.TempDir()
	galaMod := &mod.File{
		Require: []mod.Require{
			{Path: "example.com/other", Version: "v1.0.0"},
		},
	}
	config := DefaultConfig()
	got := resolveEffectiveDepDir(config, galaMod, projectDir,
		mod.Require{Path: "example.com/other", Version: "v1.0.0"})

	require.Equal(t, config.GalaModulePath("example.com/other", "v1.0.0"), got)
}

// TestResolveEffectiveDepDir_ModuleReplace verifies that replace directives
// retargeting one module version to another (non-local form) redirect the
// lookup to the replacement's cache entry rather than the original.
func TestResolveEffectiveDepDir_ModuleReplace(t *testing.T) {
	galaMod := &mod.File{
		Require: []mod.Require{
			{Path: "example.com/foo", Version: "v1.0.0"},
		},
		Replace: []mod.Replace{
			{
				Old: mod.ModuleVersion{Path: "example.com/foo"},
				New: mod.ModuleVersion{Path: "example.com/fork", Version: "v1.2.3"},
			},
		},
	}
	config := DefaultConfig()
	got := resolveEffectiveDepDir(config, galaMod, "",
		mod.Require{Path: "example.com/foo", Version: "v1.0.0"})

	require.Equal(t, config.GalaModulePath("example.com/fork", "v1.2.3"), got)
}

// TestEnsureDeps_LocalReplaceSkipsFetch verifies the end-to-end behavior of
// the builder's ensureDeps: a local replacement must not trigger a fetch.
func TestEnsureDeps_LocalReplaceSkipsFetch(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "parent")
	require.NoError(t, os.MkdirAll(parent, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(parent, "gala.mod"),
		[]byte("module example.com/parent\n\ngala 0.0.0\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(parent, "lib.gala"),
		[]byte("package parent\nfunc Greeting() string = \"hi\"\n"), 0644))

	sub := filepath.Join(tmp, "sub")
	require.NoError(t, os.MkdirAll(sub, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "gala.mod"),
		[]byte("module example.com/sub\n\ngala 0.0.0\n\nrequire example.com/parent v0.0.0\n\nreplace example.com/parent => ../parent\n"),
		0644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "main.gala"),
		[]byte("package main\nimport \"example.com/parent\"\nfunc main() { Println(parent.Greeting()) }\n"),
		0644))

	origHome, hadHome := os.LookupEnv("HOME")
	origUserProfile, hadProfile := os.LookupEnv("USERPROFILE")
	isolated := t.TempDir()
	os.Setenv("HOME", isolated)
	os.Setenv("USERPROFILE", isolated)
	t.Cleanup(func() {
		if hadHome {
			os.Setenv("HOME", origHome)
		} else {
			os.Unsetenv("HOME")
		}
		if hadProfile {
			os.Setenv("USERPROFILE", origUserProfile)
		} else {
			os.Unsetenv("USERPROFILE")
		}
	})

	b, err := NewBuilder(sub, "test", false)
	require.NoError(t, err)
	require.NoError(t, b.workspace.Ensure())

	require.NoError(t, b.ensureDeps(), "local replace must short-circuit the fetch")

	got := b.effectiveDepDir(mod.Require{Path: "example.com/parent", Version: "v0.0.0"})
	require.Equal(t, filepath.Clean(parent), got)
}

// TestTranspile_CrossModuleSealedCaseEmitsApply verifies that a consumer
// project consuming a sealed type from a separately declared GALA module
// (via require + local replace in gala.mod) lowers a sealed-case constructor
// call as `Type[T]{}.Apply(...)` rather than as a bare type conversion
// `Type[T](...)` that Go rejects on a zero-field struct.
//
// Layout (all under t.TempDir()):
//
//	parent/
//	  gala.mod          module example.com/qalib
//	  effects.gala      sealed type Effect[T] { case Halt() ... }
//	consumer/
//	  gala.mod          module example.com/qaconsumer
//	                    require example.com/qalib v0.0.0
//	                    replace example.com/qalib => ../parent
//	  main.gala         import . "example.com/qalib"
//	                    func mkHalt() Effect[int] = Halt[int]()
//	                    func main() { val h = mkHalt(); Println(h.String()) }
//
// The bug being guarded: cross-module GALA dependencies served via local
// replace were being misclassified as Go packages because the module-root
// discovery preferred a stray parent go.mod over the project's gala.mod and
// because IsGalaPackage relied on a cache lookup that fails for never-fetched
// modules. As a result the consumer's analyzer dropped the dependency's
// sealed-type Apply method metadata, and the transformer emitted
// `Halt[int]()` which Go rejects with "missing argument in conversion".
func TestTranspile_CrossModuleSealedCaseEmitsApply(t *testing.T) {
	tmp := t.TempDir()

	// Library module with the sealed type.
	libDir := filepath.Join(tmp, "qa_lib")
	require.NoError(t, os.MkdirAll(libDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(libDir, "gala.mod"),
		[]byte("module example.com/qalib\n\ngala 0.0.0\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(libDir, "effects.gala"),
		[]byte("package qalib\n\nsealed type Effect[T any] {\n    case Halt()\n    case Yield(Val T)\n}\n"),
		0644))

	// Consumer module using the lib via local replace.
	consumerDir := filepath.Join(tmp, "qa_consumer")
	require.NoError(t, os.MkdirAll(consumerDir, 0755))
	consumerGalaMod := `module example.com/qaconsumer

gala 0.0.0

require example.com/qalib v0.0.0

replace example.com/qalib => ../qa_lib
`
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "gala.mod"),
		[]byte(consumerGalaMod), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "main.gala"),
		[]byte("package main\n\nimport . \"example.com/qalib\"\n\nfunc mkHalt() Effect[int] = Halt[int]()\n\nfunc main() {\n    val h = mkHalt()\n    Println(h.String())\n}\n"),
		0644))

	// Isolate any per-user gala state to t.TempDir() to avoid clobbering the
	// developer's real ~/.gala cache and to keep the test hermetic.
	origHome, hadHome := os.LookupEnv("HOME")
	origUserProfile, hadProfile := os.LookupEnv("USERPROFILE")
	isolated := t.TempDir()
	os.Setenv("HOME", isolated)
	os.Setenv("USERPROFILE", isolated)
	t.Cleanup(func() {
		if hadHome {
			os.Setenv("HOME", origHome)
		} else {
			os.Unsetenv("HOME")
		}
		if hadProfile {
			os.Setenv("USERPROFILE", origUserProfile)
		} else {
			os.Unsetenv("USERPROFILE")
		}
	})

	// Make cwd the consumer dir so resolver discovery walks up from there.
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(originalWd) })
	require.NoError(t, os.Chdir(consumerDir))

	b, err := NewBuilder(consumerDir, "test", false)
	require.NoError(t, err)
	require.NoError(t, b.workspace.Ensure())

	// transpileDeps populates the dep workspace with effects.gen.go.
	require.NoError(t, b.ensureStdlib())
	require.NoError(t, b.transpileDeps())

	// Transpile the consumer. This is the step that hits the resolver and
	// must classify example.com/qalib as a GALA package via the local
	// replace directive.
	require.NoError(t, b.transpile())

	// Read the generated consumer Go file and assert it lowers the sealed
	// case constructor as `Halt[int]{}.Apply()`, not as a bare conversion.
	mainGen := filepath.Join(b.workspace.GenDir, "main.gen.go")
	data, err := os.ReadFile(mainGen)
	require.NoError(t, err, "consumer main.gen.go must be produced")
	got := string(data)

	require.Contains(t, got, "Halt[int]{}.Apply()",
		"sealed case constructor must lower to {}.Apply() form, got:\n%s", got)
	require.NotContains(t, got, "Halt[int]()",
		"sealed case constructor must not lower to a bare type conversion, got:\n%s", got)
}

// TestTranspile_CrossModuleLambdaInfersParamType verifies that a lambda passed
// to a higher-order function imported from another module — reached via a
// require + ABSOLUTE-path replace directive — has its parameter and return
// types inferred from the callee's declared function-typed parameter.
//
// The dependency is recognised as a GALA package only if the replace branch of
// the resolver resolves the absolute path correctly. When an absolute replace
// target was (incorrectly) joined onto the gala.mod directory, the dep was
// demoted to a Go package: its FunctionMetadata (with the `func(int) Res`
// parameter type) was dropped, the expected type never reached the lambda
// literal, and the lambda lowered to `func(x any) any` — which does not satisfy
// `func(int) Res` and fails the downstream Go build.
func TestTranspile_CrossModuleLambdaInfersParamType(t *testing.T) {
	tmp := t.TempDir()

	// Library module with a struct and a higher-order function whose parameter
	// is a concrete function type.
	libDir := filepath.Join(tmp, "lreplib")
	require.NoError(t, os.MkdirAll(libDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(libDir, "gala.mod"),
		[]byte("module example.com/lreplib\n\ngala 0.0.0\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(libDir, "lib.gala"),
		[]byte("package lreplib\n\nstruct Res(V string)\n\nfunc MkRes(s string) Res = Res(s)\n\nfunc Apply(f func(int) Res) Res = f(7)\n"),
		0644))

	// Consumer module using the lib via an ABSOLUTE-path local replace.
	consumerDir := filepath.Join(tmp, "lrepapp")
	require.NoError(t, os.MkdirAll(consumerDir, 0755))
	consumerGalaMod := "module example.com/lrepapp\n\ngala 0.0.0\n\n" +
		"require example.com/lreplib v0.0.0\n\n" +
		"replace example.com/lreplib => " + libDir + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "gala.mod"),
		[]byte(consumerGalaMod), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "main.gala"),
		[]byte("package main\n\nimport . \"example.com/lreplib\"\n\nfunc main() {\n    val r = Apply((x) => MkRes(\"hi\"))\n    Println(r.V)\n}\n"),
		0644))

	// Isolate per-user gala state to keep the test hermetic.
	origHome, hadHome := os.LookupEnv("HOME")
	origUserProfile, hadProfile := os.LookupEnv("USERPROFILE")
	isolated := t.TempDir()
	os.Setenv("HOME", isolated)
	os.Setenv("USERPROFILE", isolated)
	t.Cleanup(func() {
		if hadHome {
			os.Setenv("HOME", origHome)
		} else {
			os.Unsetenv("HOME")
		}
		if hadProfile {
			os.Setenv("USERPROFILE", origUserProfile)
		} else {
			os.Unsetenv("USERPROFILE")
		}
	})

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(originalWd) })
	require.NoError(t, os.Chdir(consumerDir))

	b, err := NewBuilder(consumerDir, "test", false)
	require.NoError(t, err)
	require.NoError(t, b.workspace.Ensure())

	require.NoError(t, b.ensureStdlib())
	require.NoError(t, b.transpileDeps())
	require.NoError(t, b.transpile())

	mainGen := filepath.Join(b.workspace.GenDir, "main.gen.go")
	data, err := os.ReadFile(mainGen)
	require.NoError(t, err, "consumer main.gen.go must be produced")
	got := string(data)

	require.Contains(t, got, "func(x int) Res",
		"lambda must infer its param/return types from the cross-module callee's func(int) Res parameter, got:\n%s", got)
	require.NotContains(t, got, "func(x any) any",
		"lambda must not fall back to func(any) any when the callee is imported from another module, got:\n%s", got)
}
