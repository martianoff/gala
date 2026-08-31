package galaerr_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestSyntaxErrorCarriesFilePath pins R1: a syntax error must record which file
// it came from. Without it WithFilePath had nothing to stamp, so `gala build`
// and `gala run` — which, unlike `gala transpile`, pass no fallback source to
// the renderer — printed syntax errors with no `-->` locus at all: no file, no
// line, nothing for a reader or an agent to navigate to.
func TestSyntaxErrorCarriesFilePath(t *testing.T) {
	err := galaerr.NewSyntaxError(7, 4, "no viable alternative at input 'x'")
	require.Empty(t, err.FilePath, "a fresh syntax error has no path until stamped")

	stamped := galaerr.WithFilePath(error(err), "pkg/main.gala")
	require.Equal(t, "pkg/main.gala", err.FilePath,
		"WithFilePath must stamp a SyntaxError, not only a SemanticError")
	require.Same(t, error(err), stamped)
}

// TestRenderRichFramesSyntaxErrorFromItsOwnFilePath is the payoff: once the
// path is on the error, the renderer resolves the source itself and frames the
// snippet without the caller supplying a fallback — which is exactly what the
// build path could not do before.
func TestRenderRichFramesSyntaxErrorFromItsOwnFilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.gala")
	src := "package main\n\nfunc main() {\n    val f = x => x * 2\n}\n"
	require.NoError(t, os.WriteFile(path, []byte(src), 0o600))

	err := galaerr.NewSyntaxError(4, 14, "extraneous input '=>'")
	err.FilePath = path

	// Options deliberately empty, mirroring cmd/gala/commands/buildfail.go.
	out := galaerr.RenderRich(error(err), galaerr.Options{})

	require.Contains(t, out, "-->", "the locus line must be rendered")
	require.Contains(t, out, "main.gala:4:15", "1-based column in the locus")
	require.Contains(t, out, "val f = x => x * 2", "the source line must be framed")
	require.True(t, strings.Contains(out, "^"), "a caret must be drawn")
}
