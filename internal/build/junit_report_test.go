package build

import (
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunTests_WritesJUnitReport runs `gala test --junit` (RunTests is what
// the command drives) on a package-main root whose own tests run as a test
// binary, with a subpackage whose tests run under `go test`. Both routes end
// up in the one report, under the project's import paths.
func TestRunTests_WritesJUnitReport(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	projectDir := t.TempDir()
	files := map[string]string{
		"gala.mod": "module example.com/junitapp\n\ngala 0.0.0\n",
		"main.gala": `package main

import "example.com/junitapp/internal/calc"

func main() {
    Println(calc.Square(3))
}

func twice(n int) int = n * 2
`,
		"main_test.gala": `package main

import . "martianoff/gala/test"

func TestTwice(t T) T = Eq(t, twice(2), 4)

func TestTwiceWrong(t T) T = Eq(t, twice(2), 5)

func TestLater(t T) T = t.Skip()

func TestTable(t T) T {
    val first = t.Run("small", (s) => Eq(s, twice(1), 2))
    return first.Run("large", (s) => Eq(s, twice(100), 200))
}
`,
		"internal/calc/calc.gala": "package calc\n\nfunc Square(n int) int = n * n\n",
		"internal/calc/calc_test.gala": `package calc

import . "martianoff/gala/test"

func TestSquare(t T) T = Eq(t, Square(4), 16)
`,
	}
	for name, content := range files {
		writeFixtureFile(t, filepath.Join(projectDir, filepath.FromSlash(name)), content)
	}

	isolateUserState(t)
	setEnvForTest(t, "GOCACHE", filepath.Join(t.TempDir(), "gocache"))
	alignGorootWithPathGo(t)
	chdirForTest(t, projectDir)

	b, err := NewBuilder(projectDir, "test", false)
	require.NoError(t, err)
	reportPath := filepath.Join(t.TempDir(), "reports", "junit.xml")
	testErr := b.RunTests(TestOptions{JUnitPath: reportPath})
	if testErr != nil && isToolchainEnvError(testErr.Error()) {
		t.Skipf("Go toolchain unavailable/mismatched in this environment: %v", testErr)
	}
	require.Error(t, testErr)
	assert.Contains(t, testErr.Error(), "tests failed")

	data, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	var doc junitTestSuites
	require.NoError(t, xml.Unmarshal(data, &doc), string(data))

	assert.Equal(t, [4]int{7, 1, 0, 1}, [4]int{doc.Tests, doc.Failures, doc.Errors, doc.Skipped}, string(data))
	require.Len(t, doc.Suites, 2, string(data))

	root := doc.Suites[0]
	assert.Equal(t, "example.com/junitapp", root.Name)
	names := map[string]junitTestCase{}
	for _, tc := range root.TestCases {
		names[tc.Name] = tc
	}
	assert.Len(t, names, 6)
	for _, name := range []string{"TestTwice", "TestTable", "TestTable/small", "TestTable/large"} {
		tc, ok := names[name]
		if assert.True(t, ok, name) {
			assert.Nil(t, tc.Failure, name)
			assert.Nil(t, tc.Skipped, name)
		}
	}
	require.NotNil(t, names["TestTwiceWrong"].Failure)
	assert.Equal(t, "expected 5, got 4", names["TestTwiceWrong"].Failure.Message)
	assert.NotNil(t, names["TestLater"].Skipped)

	sub := doc.Suites[1]
	assert.Equal(t, "example.com/junitapp/internal/calc", sub.Name)
	require.Len(t, sub.TestCases, 1)
	assert.Equal(t, "TestSquare", sub.TestCases[0].Name)
	assert.Nil(t, sub.TestCases[0].Failure)
}
