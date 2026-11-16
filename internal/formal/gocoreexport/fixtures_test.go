package gocoreexport_test

import (
	"encoding/json"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/gocoreexport"
)

// TestGoCoreFixturesFresh checks that each GoCore fixture's document
// (formal/tests/gocore/<name>.json) is what the exporter writes for its source
// (formal/tests/gocore/src/<name>.go), so the fixtures cannot drift from the
// exporter. Regenerate them with formal/tests/gocore/regen.sh.
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
		// The standard library's runnable files, which regen.sh adds from the
		// pinned toolchain's GOROOT (gocoreexport.AddStdlib), are not read here:
		// the document is compared without them.
		require.Equal(t, withoutStdlib(t, want), withoutStdlib(t, got),
			"%s.json is stale: run formal/tests/gocore/regen.sh", name)
	}
}

// withoutStdlib is a GoCore document, as JSON values, without the packages of
// gocoreexport.RunnableStdlib.
func withoutStdlib(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	var v map[string]any
	require.NoError(t, json.Unmarshal(doc, &v))
	pkgs, _ := v["packages"].([]any)
	kept := []any{}
	for _, p := range pkgs {
		if m, ok := p.(map[string]any); ok && gocoreexport.RunnableStdlib[m["path"].(string)] != nil {
			continue
		}
		kept = append(kept, p)
	}
	v["packages"] = kept
	return v
}
