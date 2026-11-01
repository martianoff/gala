package transformer_test

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// updateGoldens rewrites the walk goldens instead of checking them. Run the
// test with `bazel run` and `-- -update`: that writes them into the
// workspace (BUILD_WORKSPACE_DIRECTORY).
var updateGoldens = flag.Bool("update", false, "rewrite the walk goldens in testdata/walk_golden")

// goldenDir is the directory of the walk goldens, relative to the package.
const goldenDir = "testdata/walk_golden"

// goldenSeen counts the goldens each test has checked, so a test that
// transpiles more than one program has a golden for each.
var goldenSeen = struct {
	sync.Mutex
	n map[string]int
}{n: map[string]int{}}

// goldenPath is the golden of the i-th program the test name transpiles.
func goldenPath(name string, i int) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"\|?*`, r) || r < ' ' {
			return '_'
		}
		return r
	}, name)
	if i > 0 {
		name += "#" + strconv.Itoa(i)
	}
	return filepath.Join(goldenDir, filepath.FromSlash(name)+".golden")
}

// goldenText is the Go of a transpile, or its error, with the directory of
// the transpiled file and the import search paths replaced by placeholders,
// so that it reads the same on every machine.
func goldenText(path string, searchPaths []string, out string, err error) string {
	text := out
	if err != nil {
		text = "error: " + err.Error() + "\n"
	}
	replace := func(dir, with string) {
		if dir == "" || dir == "." {
			return
		}
		text = strings.ReplaceAll(text, dir, with)
		text = strings.ReplaceAll(text, filepath.ToSlash(dir), with)
	}
	replace(filepath.Dir(path), "$DIR")
	for _, sp := range searchPaths {
		replace(sp, "$SEARCH")
	}
	// The rest of such a path is written with the machine's separator.
	return placeholderPath.ReplaceAllStringFunc(text, func(p string) string {
		return strings.ReplaceAll(p, `\`, "/")
	})
}

// placeholderPath is a path below a placeholder of goldenText.
var placeholderPath = regexp.MustCompile(`\$(DIR|SEARCH)[\\/][^\s:]*`)

// checkGolden holds a transpile of the program at path, its Go out or its
// error err, to its golden. The goldens were written while the transformer
// still walked ANTLR's tree beside the native one, from transpiles on which
// the two walks agreed.
func checkGolden(tb testing.TB, path string, searchPaths []string, out string, err error) {
	tb.Helper()
	goldenSeen.Lock()
	i := goldenSeen.n[tb.Name()]
	goldenSeen.n[tb.Name()]++
	goldenSeen.Unlock()
	file := goldenPath(tb.Name(), i)
	got := goldenText(path, searchPaths, out, err)
	if *updateGoldens {
		ws := os.Getenv("BUILD_WORKSPACE_DIRECTORY")
		require.NotEmpty(tb, ws, "-update writes into the workspace: run the test with bazel run")
		dest := filepath.Join(ws, "internal", "transpiler", "transformer", file)
		require.NoError(tb, os.MkdirAll(filepath.Dir(dest), 0o755))
		require.NoError(tb, os.WriteFile(dest, []byte(got), 0o644))
		return
	}
	want, readErr := os.ReadFile(file)
	require.NoErrorf(tb, readErr, "no golden %s: run the test with bazel run and -- -update", file)
	if w := strings.ReplaceAll(string(want), "\r\n", "\n"); w != got {
		assert.Failf(tb, "the transpile differs from its golden", "%s: first difference at %s", file, firstLineDiff(got, w))
	}
}
