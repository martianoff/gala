package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/digestgolden"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/testcorpus"
)

// The goldens of `gala imports --json` and `gala doc` over the frozen corpus
// (internal/testcorpus), written when both read the file on ANTLR's tree and
// on the native tree alike, and the two agreed on the same corpus. Every file
// (or directory) of the corpus must have its line: a missing or stale line
// fails the test. To regenerate one, run its test with
// GALA_UPDATE_IMPORTS_GOLDEN or GALA_UPDATE_DOC_GOLDEN set to the golden's
// path in the source tree:
//
//	bazel test //cmd/gala/commands:commands_native_test \
//	  --test_filter=TestImportsGolden \
//	  --test_env=GALA_UPDATE_IMPORTS_GOLDEN=$PWD/cmd/gala/commands/testdata/imports.golden
const (
	importsGolden = "cmd/gala/commands/testdata/imports.golden"
	docGolden     = "cmd/gala/commands/testdata/doc.golden"
)

// stagedSources is the .gala files of the frozen corpus (internal/testcorpus),
// extracted for t, in name order, and the corpus.
func stagedSources(t *testing.T) ([]string, testcorpus.Corpus) {
	t.Helper()
	// Parse with galop's engine, the CLI's default, whatever the build's or
	// environment's default engine.
	parser.RequestEngine(string(parser.EngineGalop))
	t.Cleanup(func() { parser.RequestEngine("") })
	c := testcorpus.Extract(t)
	return c.GalaFiles(), c
}

// TestImportsGolden extracts every corpus file's imports, as
// `gala imports --json` prints them, and holds them to the golden.
func TestImportsGolden(t *testing.T) {
	paths, c := stagedSources(t)
	g := digestgolden.Open(t, importsGolden, "GALA_UPDATE_IMPORTS_GOLDEN")
	p := parser.NewAntlrGalaParser()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		got := jsonText(t, extractImports(p, "", string(data)))
		g.Check("imports "+c.Rel(path), digestgolden.Digest(string(data)), digestgolden.Digest(got), func() string { return got })
	}
	g.Finish("`gala imports --json` over the corpus; see importsGolden in native_tree_test.go.")
	if !g.Updating() {
		t.Logf("%d of %d files held to the golden", g.Compared, len(paths))
	}
}

// TestDocGolden documents every package directory of the corpus, as
// `gala doc` does, and holds the package surface, as `gala doc` prints it and
// as its JSON, or the error, to the golden.
func TestDocGolden(t *testing.T) {
	paths, c := stagedSources(t)
	dirs := map[string]bool{}
	for _, path := range paths {
		dirs[c.Rel(filepath.Dir(path))] = true
	}
	sorted := make([]string, 0, len(dirs))
	for dir := range dirs {
		sorted = append(sorted, dir)
	}
	sort.Strings(sorted)
	norm := c.Normalizer()
	g := digestgolden.Open(t, docGolden, "GALA_UPDATE_DOC_GOLDEN")
	documented := 0
	for _, rel := range sorted {
		dir := c.Path(rel)
		pkg, err := loadPackageDoc(filepath.Base(dir), dir)
		var got string
		if err != nil {
			got = "error: " + err.Error()
		} else {
			var text bytes.Buffer
			renderPackageDoc(&text, pkg)
			got = jsonText(t, pkg) + "\n" + text.String()
			documented++
		}
		got = norm.Replace(got)
		g.Check("doc "+rel, dirDigest(t, dir), digestgolden.Digest(got), func() string { return got })
	}
	g.Finish("`gala doc` over the corpus; see docGolden in native_tree_test.go.")
	if !g.Updating() {
		t.Logf("%d directories, %d documented, %d held to the golden", len(sorted), documented, g.Compared)
	}
	assert.Greater(t, documented, 5)
}

// dirDigest is the digest of the .gala files of dir, by name and text.
func dirDigest(t *testing.T, dir string) string {
	files, err := filepath.Glob(filepath.Join(dir, "*.gala"))
	require.NoError(t, err)
	sort.Strings(files)
	var b strings.Builder
	for _, f := range files {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		fmt.Fprintf(&b, "%s\x00%d\x00%s", filepath.Base(f), len(data), data)
	}
	return digestgolden.Digest(b.String())
}

func jsonText(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}
