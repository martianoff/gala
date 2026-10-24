package build

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTest_EmbedFromTestdata covers `gala test` on packages whose test files
// embed fixtures under testdata/, Go's conventional home for test data. The
// workspace copy of the project tree leaves testdata/ out, so the files an
// embed pattern names have to be copied on their own; without that the run
// failed at setup with "pattern testdata/sample.txt: no matching files found".
//
// The library project also covers a subpackage (Go resolves a pattern against
// the directory of the file declaring it) and a directory pattern, which embeds
// the whole tree below it.
func TestTest_EmbedFromTestdata(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	const (
		testImport = "import . \"martianoff/gala/test\"\n\n"
		subSrc     = "package sub\n\nfunc Name() string = \"sub\"\n"
		subTest    = "package sub\n\n" + testImport + "embed val sample string = \"testdata/sample.txt\"\n\nfunc TestSubSample(t T) T = Eq(t, sample, \"sub fixture\\n\")\n"
		mainSrc    = "package main\n\nfunc main() {\n    Println(\"hi\")\n}\n"
	)
	cases := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "library package",
			files: map[string]string{
				"lib.gala": "package mylib\n\nfunc Hello() string = \"hi\"\n",
				"lib_test.gala": "package mylib\n\n" + testImport +
					"embed val sample string = \"testdata/sample.txt\"\n\n" +
					"embed val fixtures EmbeddedFS = \"testdata\"\n\n" +
					"func TestSample(t T) T = Eq(t, sample, \"hello\\n\")\n\n" +
					"func TestDeep(t T) T = Eq(t, fixtures.ReadString(\"testdata/nested/deep.txt\").GetOrElse(\"\"), \"deep\\n\")\n",
				"testdata/sample.txt":      "hello\n",
				"testdata/nested/deep.txt": "deep\n",
				"sub/sub.gala":             subSrc,
				"sub/sub_test.gala":        subTest,
				"sub/testdata/sample.txt":  "sub fixture\n",
			},
		},
		{
			// A package-main root's own tests are built into a test binary
			// rather than run by `go test`.
			name: "package-main root",
			files: map[string]string{
				"main.gala":           mainSrc,
				"main_test.gala":      "package main\n\n" + testImport + "embed val sample string = \"testdata/sample.txt\"\n\nfunc TestSample(t T) T = Eq(t, sample, \"hello\\n\")\n",
				"testdata/sample.txt": "hello\n",
			},
		},
	}

	// One user state and Go build cache for both runs, so the second one
	// compiles warm.
	isolateUserState(t)
	setEnvForTest(t, "GOCACHE", filepath.Join(t.TempDir(), "gocache"))
	alignGorootWithPathGo(t)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projectDir := t.TempDir()
			writeFixtureFile(t, filepath.Join(projectDir, "gala.mod"), "module example.com/mylib\n\ngala 0.0.0\n")
			for name, content := range tc.files {
				writeFixtureFile(t, filepath.Join(projectDir, filepath.FromSlash(name)), content)
			}
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

// TestBuild_CmdDirEmbedFromTestdata covers `gala build ./cmd/<name>`, which
// transpiles the library and the named main package apart, when each embeds a
// file the copy of their trees into gen/ leaves out: one under testdata/.
func TestBuild_CmdDirEmbedFromTestdata(t *testing.T) {
	projectDir := newForeignGenGoProject(t, "example.com/cmdembed", map[string]string{
		"greet/greet.gala":            "package greet\n\nembed val Greeting string = \"testdata/greeting.txt\"\n",
		"greet/testdata/greeting.txt": "hello",
		"cmd/app/main.gala": "package main\n\nimport \"example.com/cmdembed/greet\"\n\n" +
			"embed val banner string = \"testdata/banner.txt\"\n\n" +
			"func main() {\n    Println(banner)\n    Println(greet.Greeting)\n}\n",
		"cmd/app/testdata/banner.txt": "banner",
	})
	assert.Equal(t, "banner|hello", buildAndRunFrom(t, projectDir, filepath.Join(projectDir, "cmd", "app")))
}
