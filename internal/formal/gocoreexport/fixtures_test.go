package gocoreexport_test

import (
	"encoding/json"
	"os"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/gocoreexport"
)

// TestGoCoreFixturesFresh checks that each GoCore fixture's document
// (formal/tests/gocore/<name>.json) is what the exporter writes for its source
// (formal/tests/gocore/src/<name>.go), so the fixtures cannot drift from the
// exporter, and that every document has a source: none then holds a standard-
// library package, which the exporter adds only when asked (AddStdlib), never
// for a fixture (formal/README.md). Regenerate them with
// formal/tests/gocore/regen.sh.
func TestGoCoreFixturesFresh(t *testing.T) {
	files, err := bazel.ListRunfiles()
	require.NoError(t, err)
	docs := map[string]string{}
	var srcs []bazel.RunfileEntry
	for _, f := range files {
		switch {
		case path.Dir(f.ShortPath) == "formal/tests/gocore" && strings.HasSuffix(f.ShortPath, ".json"):
			docs[strings.TrimSuffix(path.Base(f.ShortPath), ".json")] = f.Path
		case path.Dir(f.ShortPath) == "formal/tests/gocore/src" && strings.HasSuffix(f.ShortPath, ".go"):
			srcs = append(srcs, f)
		}
	}
	require.NotEmpty(t, srcs, "//formal:gocore_fixtures is not staged")
	for _, s := range srcs {
		name := strings.TrimSuffix(path.Base(s.ShortPath), ".go")
		text, err := os.ReadFile(s.Path)
		require.NoError(t, err)
		prog, err := gocoreexport.Export([]gocoreexport.PackageSource{{Path: "main",
			Files: []gocoreexport.Source{{Name: name + ".go", Text: strings.ReplaceAll(string(text), "\r\n", "\n")}}}})
		require.NoError(t, err)
		got, err := gocoreexport.Marshal(prog)
		require.NoError(t, err)
		docPath, ok := docs[name]
		require.True(t, ok, "%s has no document", name)
		want, err := os.ReadFile(docPath)
		require.NoError(t, err)
		require.Equal(t, decoded(t, want), decoded(t, got),
			"%s.json is stale: run formal/tests/gocore/regen.sh", name)
	}
	// Every document is its source's export, which adds no standard-library
	// package: so none holds Go's own source of one (formal/README.md).
	for name := range docs {
		require.True(t, slices.ContainsFunc(srcs, func(s bazel.RunfileEntry) bool {
			return path.Base(s.ShortPath) == name+".go"
		}), "%s.json has no source in formal/tests/gocore/src", name)
	}
}

// decoded is a GoCore document as JSON values.
func decoded(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	var v map[string]any
	require.NoError(t, json.Unmarshal(doc, &v))
	return v
}
