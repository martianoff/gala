package build

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// framingTests are tests whose output has no trailing newline, so the test
// framework's next framing line runs into it: the result line, or a
// subtest's "=== RUN". test2json knows framing only at the start of a line
// or where ^V starts it, so without the framework's ^V markers these tests
// were reported as failed ("the test did not finish", #1375).
const framingTests = `

import "strings"
import . "martianoff/gala/test"

// An OSC 52 clipboard sequence, as a TUI's yank writes it.
func TestYank(t T) T {
    Print("\x1b]52;c;eW91Lg==\x07")
    t
}

// Longer than test2json's 1024-byte line buffer, which splits it.
func TestLongLine(t T) T {
    Print(strings.Repeat("\r[====>     ] 50%", 200))
    t
}

func TestIndented(t T) T {
    Print("  loading...")
    t
}

func TestRunAfterOutput(t T) T {
    Print("x")
    t.Run("sub", (s) => {
        Print("y")
        s
    })
}

func TestFailAfterOutput(t T) T {
    val failed = t.Error("bad")
    Print("z")
    failed
}
`

// TestRunTests_OutputWithoutTrailingNewline runs `gala test` on a
// package-main root, whose tests run as a test binary, and a subpackage,
// whose tests run under `go test`: in both, every test above has its own
// result, and only the failing one fails.
func TestRunTests_OutputWithoutTrailingNewline(t *testing.T) {
	projectDir := newForeignGenGoProject(t, "example.com/framing", map[string]string{
		"main.gala":         "package main\n\nfunc main() {\n    Println(\"main\")\n}\n",
		"main_test.gala":    "package main" + framingTests,
		"sub/sub.gala":      "package sub\n\nfunc Sub() int = 1\n",
		"sub/sub_test.gala": "package sub" + framingTests,
	})
	chdirForTest(t, projectDir)

	b, err := NewBuilder(projectDir, "test", false)
	require.NoError(t, err)
	reportPath := filepath.Join(t.TempDir(), "junit.xml")
	testErr := b.RunTests(TestOptions{JUnitPath: reportPath})
	if testErr != nil && isToolchainEnvError(testErr.Error()) {
		t.Skipf("Go toolchain unavailable/mismatched in this environment: %v", testErr)
	}
	require.Error(t, testErr, "TestFailAfterOutput fails")

	data, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	var doc junitTestSuites
	require.NoError(t, xml.Unmarshal(data, &doc), string(data))
	require.Len(t, doc.Suites, 2, string(data))
	for _, suite := range doc.Suites {
		got := map[string]string{}
		for _, tc := range suite.TestCases {
			outcome, message, _ := outcomeOf(tc)
			got[tc.Name] = outcome + " " + message
		}
		assert.Equal(t, map[string]string{
			"TestYank":               "pass ",
			"TestLongLine":           "pass ",
			"TestIndented":           "pass ",
			"TestRunAfterOutput":     "pass ",
			"TestRunAfterOutput/sub": "pass ",
			"TestFailAfterOutput":    "fail bad",
		}, got, "%s: %s", suite.Name, data)
	}
}
