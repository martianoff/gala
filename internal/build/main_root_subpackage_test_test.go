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
	cases := []struct {
		name    string
		files   map[string]string
		wantErr bool
	}{
		{
			name: "tests only in a subpackage",
			files: map[string]string{
				"internal/a/b/b.gala":      square,
				"internal/a/b/b_test.gala": "package b\n\nimport . \"martianoff/gala/test\"\n\nfunc TestSquare(t T) T = Eq(t, Square(4), 16)\n",
			},
		},
		{
			name: "tests in the root and in a subpackage",
			files: map[string]string{
				"internal/a/b/b.gala":      square,
				"internal/a/b/b_test.gala": "package b\n\nimport . \"martianoff/gala/test\"\n\nfunc TestSquare(t T) T = Eq(t, Square(4), 16)\n",
				"main_test.gala":           "package main\n\nimport . \"martianoff/gala/test\"\n\nfunc TestTwice(t T) T = Eq(t, twice(2), 4)\n",
			},
		},
		{
			name: "a failing subpackage test fails the run",
			files: map[string]string{
				"internal/a/b/b.gala":      square,
				"internal/a/b/b_test.gala": "package b\n\nimport . \"martianoff/gala/test\"\n\nfunc TestSquare(t T) T = Eq(t, Square(4), 17)\n",
				"main_test.gala":           "package main\n\nimport . \"martianoff/gala/test\"\n\nfunc TestTwice(t T) T = Eq(t, twice(2), 4)\n",
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projectDir := t.TempDir()
			files := map[string]string{
				"gala.mod":  "module example.com/nested\n\ngala 0.0.0\n",
				"main.gala": mainSrc,
			}
			for k, v := range tc.files {
				files[k] = v
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
				assert.Error(t, testErr)
				return
			}
			assert.NoError(t, testErr)
		})
	}
}

// TestSplitRootTestFiles: only files directly in the project root are root
// tests.
func TestSplitRootTestFiles(t *testing.T) {
	root := filepath.Join("proj")
	rootTests, subTests := splitRootTestFiles(root, []string{
		filepath.Join(root, "main_test.gala"),
		filepath.Join(root, "internal", "a", "b", "b_test.gala"),
		filepath.Join(root, "cmd", "app", "app_test.gala"),
	})
	assert.Equal(t, []string{filepath.Join(root, "main_test.gala")}, rootTests)
	assert.Len(t, subTests, 2)
}
