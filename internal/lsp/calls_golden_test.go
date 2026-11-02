package lsp

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/digestgolden"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/transpiler/analyzer"
)

// callsGolden holds TestCallsGolden's results: a line per clean file of the
// corpus, its path, a digest of its text and a digest of what signature help
// finds in it. It was written from the code on which the call finder on the
// native tree found, at every one of these cursors, the call the finder on
// ANTLR's tree found, and read the dot imports ANTLR's tree gave.
const callsGolden = "internal/lsp/testdata/calls.golden"

// TestCallsGolden finds the call at up to 150 cursors of callCursors in every
// clean file of the corpus (findCallAtOffsetNative), and reads each file's
// dot imports (dotImportPathsOf), and holds them to their digest in
// callsGolden. Refresh the golden with --test_env=GALA_UPDATE_CALLS_GOLDEN set
// to its path in the source tree.
func TestCallsGolden(t *testing.T) {
	texts, paths, root := nativeCorpus(t)
	g := digestgolden.Open(t, callsGolden, "GALA_UPDATE_CALLS_GOLDEN")
	p := parser.NewAntlrGalaParser()
	files, calls, dotting := 0, 0, 0
	for _, path := range paths {
		text := texts[path]
		handle := p.ParseFile(text).CleanNative()
		if handle == nil {
			continue
		}
		tree, err := handle.Tree()
		require.NoError(t, err, path)
		files++
		var b strings.Builder
		for _, off := range spread(callCursors(text), 150) {
			call := findCallAtOffsetNative(tree.Root(), off)
			if call != nil {
				calls++
			}
			fmt.Fprintf(&b, "%d: %+v\n", off, call)
		}
		dots := analyzer.DotImportPathsNative(tree.Root())
		if len(dots) > 0 {
			dotting++
		}
		fmt.Fprintf(&b, "dot imports: %q\n", dots)
		rel, err := filepath.Rel(root, path)
		require.NoError(t, err)
		answers := b.String()
		g.Check(filepath.ToSlash(rel), digestgolden.Digest(text), digestgolden.Digest(answers), func() string { return answers })
	}
	g.Finish("TestCallsGolden: per clean file, a digest of its text and of the calls found at its cursors and its dot imports.")
	t.Logf("%d files, %d cursors in a call, %d with dot imports, %d held to the golden", files, calls, dotting, g.Compared)
	assert.Greater(t, files, 300)
	assert.Greater(t, calls, 10000)
	assert.Greater(t, dotting, 5)
	if !g.Updating() {
		assert.GreaterOrEqual(t, g.Compared, files*9/10, "the golden is stale: refresh it")
	}
}
