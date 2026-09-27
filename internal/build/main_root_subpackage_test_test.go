package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTest_MainRootRunsSubpackageTests covers `gala test` in a project whose
// root package is `package main` and whose tests live (also) in a subpackage.
// The root test binary lists only the root's own TestXxx; a subpackage's tests
// run in their own package. Listing them in the root's test_main failed to
// compile with "undefined: TestSquare".
func TestTest_MainRootRunsSubpackageTests(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	const square = "package b\n\nfunc Square(n int) int = n * n\n"
	const mainSrc = `package main

import "example.com/nested/internal/a/b"

func main() {
    Println(b.Square(3))
}

func twice(n int) int = n * 2
`
	const (
		subPass  = "package b\n\nimport . \"martianoff/gala/test\"\n\nfunc TestSquare(t T) T = Eq(t, Square(4), 16)\n"
		subFail  = "package b\n\nimport . \"martianoff/gala/test\"\n\nfunc TestSquare(t T) T = Eq(t, Square(4), 17)\n"
		rootPass = "package main\n\nimport . \"martianoff/gala/test\"\n\nfunc TestTwice(t T) T = Eq(t, twice(2), 4)\n"
		rootFail = "package main\n\nimport . \"martianoff/gala/test\"\n\nfunc TestTwice(t T) T = Eq(t, twice(2), 5)\n"
	)
	cases := []struct {
		name     string
		subTest  string // internal/a/b/b_test.gala
		rootTest string // main_test.gala; "" for none
		wantErr  bool   // the run fails with a test failure, not a build error
	}{
		{name: "tests only in a subpackage", subTest: subPass},
		{name: "tests in the root and in a subpackage", subTest: subPass, rootTest: rootPass},
		{name: "a failing subpackage test fails the run", subTest: subFail, rootTest: rootPass, wantErr: true},
		// The subpackage's test actually runs when the root has none.
		{name: "a failing test only in a subpackage fails the run", subTest: subFail, wantErr: true},
		{name: "a failing root test fails the run when the subpackage passes", subTest: subPass, rootTest: rootFail, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projectDir := t.TempDir()
			files := map[string]string{
				"gala.mod":                 "module example.com/nested\n\ngala 0.0.0\n",
				"main.gala":                mainSrc,
				"internal/a/b/b.gala":      square,
				"internal/a/b/b_test.gala": tc.subTest,
			}
			if tc.rootTest != "" {
				files["main_test.gala"] = tc.rootTest
			}
			for name, content := range files {
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
			testErr := b.Test(false)
			if testErr != nil && isToolchainEnvError(testErr.Error()) {
				t.Skipf("Go toolchain unavailable/mismatched in this environment: %v", testErr)
			}
			if tc.wantErr {
				// A test failure — not the "undefined: TestSquare" build
				// failure a root test_main listing subpackage tests hit.
				require.Error(t, testErr)
				assert.Contains(t, testErr.Error(), "tests failed")
				return
			}
			assert.NoError(t, testErr)
		})
	}
}

// TestSplitRootTestFiles: only files directly in the project root are root
// tests.
func TestSplitRootTestFiles(t *testing.T) {
	root := "proj"
	rootTests, subTests := splitRootTestFiles(root, []string{
		filepath.Join(root, "main_test.gala"),
		filepath.Join(root, "internal", "a", "b", "b_test.gala"),
		filepath.Join(root, "cmd", "app", "app_test.gala"),
	})
	assert.Equal(t, []string{filepath.Join(root, "main_test.gala")}, rootTests)
	assert.Len(t, subTests, 2)
}
