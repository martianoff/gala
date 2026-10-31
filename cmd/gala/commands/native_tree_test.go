package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/parser"
)

// stagedSources is the .gala files staged as the test's data, sorted.
func stagedSources(t *testing.T) []string {
	t.Helper()
	files, err := bazel.ListRunfiles()
	require.NoError(t, err)
	var paths []string
	for _, f := range files {
		if info, err := os.Stat(f.Path); err == nil && info.Mode().IsRegular() && strings.HasSuffix(f.ShortPath, ".gala") {
			paths = append(paths, f.Path)
		}
	}
	sort.Strings(paths)
	require.Greater(t, len(paths), 300, "the corpus is not staged")
	return paths
}

// TestImportsTreesAgree extracts every corpus file's imports, as
// `gala imports --json` prints them, on both trees: from the parse once its
// native tree has converted, which importsOfParse then reads, and from the
// same parse without its native tree.
func TestImportsTreesAgree(t *testing.T) {
	p := parser.NewAntlrGalaParser()
	native, failed := 0, 0
	for _, path := range stagedSources(t) {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		parsed := p.ParseFile(string(data))
		antlrOnly := *parsed
		antlrOnly.Native = nil
		want, onNative := importsOfParse(path, &antlrOnly)
		require.False(t, onNative, path)
		if handle := parsed.CleanNative(); handle != nil {
			_, err := handle.Tree()
			require.NoError(t, err)
			got, onNative := importsOfParse(path, parsed)
			require.True(t, onNative, "%s: read ANTLR's tree", path)
			require.Equal(t, jsonText(t, want), jsonText(t, got), path)
			native++
		} else {
			failed++
		}
	}
	t.Logf("%d files read on the native tree, %d without one (a parse with errors, or one ANTLR's parser made) on ANTLR's", native, failed)
	assert.Greater(t, native, 300)
}

// TestDocTreesAgree documents every package directory of the corpus, as
// `gala doc` does, with the analysis reading the file on its native tree and
// on ANTLR's: the package surface, as `gala doc` prints it and as its JSON,
// must be the same. A directory that does not document (more than one
// package, a file that does not parse) must fail the same way.
func TestDocTreesAgree(t *testing.T) {
	dirs := map[string]bool{}
	for _, path := range stagedSources(t) {
		dirs[filepath.Dir(path)] = true
	}
	sorted := make([]string, 0, len(dirs))
	for dir := range dirs {
		sorted = append(sorted, dir)
	}
	sort.Strings(sorted)
	documented := 0
	for _, dir := range sorted {
		pkgName := filepath.Base(dir)
		var analyzed *parser.Parsed
		got, gotErr := loadPackageDocWith(pkgName, dir, func(p *parser.Parsed) { analyzed = p })
		want, wantErr := loadPackageDocWith(pkgName, dir, func(p *parser.Parsed) { p.Native = nil })
		require.Equal(t, errText(wantErr), errText(gotErr), dir)
		if wantErr != nil {
			continue
		}
		// The analysis converts the native tree exactly when it reads the
		// file there.
		handle := analyzed.CleanNative()
		require.True(t, handle.Converted() && handle.Bridged(), "%s: the analysis read ANTLR's tree", dir)
		require.Equal(t, jsonText(t, want), jsonText(t, got), dir)
		var wantText, gotText bytes.Buffer
		renderPackageDoc(&wantText, want)
		renderPackageDoc(&gotText, got)
		require.Equal(t, wantText.String(), gotText.String(), dir)
		documented++
	}
	t.Logf("%d directories, %d documented on the native tree", len(sorted), documented)
	assert.Greater(t, documented, 5)
}

func jsonText(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
