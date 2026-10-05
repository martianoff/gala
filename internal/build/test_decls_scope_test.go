package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTest_TestDeclarationsStayInTheirPackageTest covers `gala test` on a
// package whose tests declare a name that a dot-importing package's tests
// declare too. A _test.gala file was transpiled to a .gen.go file, which Go
// compiles into the package itself, so the importer saw the test-only
// declaration and the build failed with "Shared already declared through
// dot-import of package ...".
func TestTest_TestDeclarationsStayInTheirPackageTest(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	cases := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "library root dot-imported by a subpackage",
			files: map[string]string{
				"gala.mod":      "module example.com/leak\n\ngala 0.0.0\n",
				"lib.gala":      "package leak\n\nfunc Answer() int = 42\n",
				"lib_test.gala": "package leak\n\nimport . \"martianoff/gala/test\"\n\nstruct Shared(X int)\n\nfunc TestAnswer(t T) T = Eq(t, Answer(), Shared(X = 42).X)\n",
				"sub/sub.gala":  "package sub\n\nimport . \"example.com/leak\"\n\nfunc Twice() int = Answer() * 2\n",
				"sub/sub_test.gala": "package sub\n\nimport . \"martianoff/gala/test\"\n\nstruct Shared(Y int)\n\n" +
					"func TestTwice(t T) T = Eq(t, Twice(), Shared(Y = 84).Y)\n",
			},
		},
		{
			name: "subpackages of a main root",
			files: map[string]string{
				"gala.mod":  "module example.com/leakmain\n\ngala 0.0.0\n",
				"main.gala": "package main\n\nimport \"example.com/leakmain/b\"\n\nfunc main() {\n    Println(b.Twice())\n}\n",
				"main_test.gala": "package main\n\nimport . \"martianoff/gala/test\"\n\nstruct Shared(Z int)\n\n" +
					"func TestRoot(t T) T = Eq(t, Shared(Z = 1).Z, 1)\n",
				"a/a.gala":      "package a\n\nfunc Answer() int = 42\n",
				"a/a_test.gala": "package a\n\nimport . \"martianoff/gala/test\"\n\nstruct Shared(X int)\n\nfunc TestAnswer(t T) T = Eq(t, Answer(), Shared(X = 42).X)\n",
				"b/b.gala":      "package b\n\nimport . \"example.com/leakmain/a\"\n\nfunc Twice() int = Answer() * 2\n",
				"b/b_test.gala": "package b\n\nimport . \"martianoff/gala/test\"\n\nstruct Shared(Y int)\n\nfunc TestTwice(t T) T = Eq(t, Twice(), Shared(Y = 84).Y)\n",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projectDir := t.TempDir()
			for name, content := range tc.files {
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
			assert.NoError(t, testErr)
		})
	}
}
