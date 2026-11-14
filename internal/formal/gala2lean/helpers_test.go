package gala2lean_test

import (
	"os"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/coreexport"
	"martianoff/gala/internal/formal/coreexport/analyzerhints"
)

// stagedFiles is every staged file under one of prefixes, by its path in the
// repository, with Windows line endings read as \n.
func stagedFiles(t *testing.T, prefixes ...string) map[string]string {
	t.Helper()
	files, err := bazel.ListRunfiles()
	require.NoError(t, err)
	out := map[string]string{}
	for _, f := range files {
		for _, p := range prefixes {
			if fi, err := os.Stat(f.Path); err == nil && !fi.IsDir() && strings.HasPrefix(f.ShortPath, p) {
				data, err := os.ReadFile(f.Path)
				require.NoError(t, err)
				out[f.ShortPath] = strings.ReplaceAll(string(data), "\r\n", "\n")
			}
		}
	}
	return out
}

func export(t *testing.T, file, text string) *coreexport.Program {
	t.Helper()
	p, err := coreexport.Export([]coreexport.Source{{Path: file, Text: text}},
		coreexport.Options{LiteralType: analyzerhints.LiteralType})
	require.NoError(t, err, file)
	return p
}
