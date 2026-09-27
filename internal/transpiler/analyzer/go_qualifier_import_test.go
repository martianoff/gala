package analyzer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
)

// TestGoQualifierSharingGalaPackageName covers the explicit-import check
// (GALA-E0025) when a Go package and a GALA package share a name. A type this
// file writes against its own Go import (`*strings.Builder`, `io.Reader`) is a
// Go type, even when a sibling imports GALA's `strings` or `io`. A qualifier the
// file never imports, and a bare name that only a sibling's dot import resolves
// to the GALA package, are still errors.
func TestGoQualifierSharingGalaPackageName(t *testing.T) {
	const goStringsParam = `package repro

import "strings"

func f(sb *strings.Builder) string = sb.String()
`
	const goIOParams = `package repro

import "io"

func copyAll(r io.Reader, w io.Writer) int64 = 0
`
	cases := []struct {
		name    string
		file    string // file under analysis
		sibling string // sibling file in the same package
		// wantErr lists substrings the error must contain; empty means the
		// file must analyze cleanly.
		wantErr []string
	}{
		{
			name: "go strings param, sibling imports gala strings aliased",
			file: goStringsParam,
			sibling: `package repro

import gs "martianoff/gala/strings"

func g() int = gs.S("xy").Length()
`,
		},
		{
			name: "go strings param, sibling imports gala strings unaliased",
			file: goStringsParam,
			sibling: `package repro

import "martianoff/gala/strings"

func g() int = strings.S("xy").Length()
`,
		},
		{
			name: "go strings return type and struct field, sibling dot-imports gala strings",
			file: `package repro

import "strings"

struct Buf(sb *strings.Builder)

func newBuf() *strings.Builder = &strings.Builder{}
`,
			sibling: `package repro

import . "martianoff/gala/strings"

func g() int = S("xy").Length()
`,
		},
		{
			name: "go strings in a sealed variant, sibling imports gala strings",
			file: `package repro

import "strings"

sealed type Out {
    case ToBuilder(sb *strings.Builder)
    case Discard()
}
`,
			sibling: `package repro

import gs "martianoff/gala/strings"

func g() int = gs.S("xy").Length()
`,
		},
		{
			name: "go io params, sibling imports gala io aliased",
			file: goIOParams,
			sibling: `package repro

import gio "martianoff/gala/io"

func g() int = gio.Of(1).UnsafeRun()
`,
		},
		{
			name: "go io params, sibling imports gala io unaliased",
			file: goIOParams,
			sibling: `package repro

import "martianoff/gala/io"

func g() int = io.Of(1).UnsafeRun()
`,
		},
		{
			name: "qualifier this file never imports still fires",
			file: `package repro

func f(s strings.Str) int = 0
`,
			sibling: `package repro

import "martianoff/gala/strings"

func g() int = strings.S("xy").Length()
`,
			wantErr: []string{"GALA-E0025", "undefined: Str", "'strings' is not imported in this file",
				"add an explicit import to this file"},
		},
		{
			name: "bare gala name through a sibling dot import still fires beside a go import of the same name",
			file: `package repro

import "strings"

func f(s Str) string = strings.TrimSpace("x")
`,
			sibling: `package repro

import . "martianoff/gala/strings"

func g() int = S("xy").Length()
`,
			wantErr: []string{"GALA-E0025", "undefined: Str", "'strings' is not imported in this file",
				"`strings` in this file is the Go import \"strings\""},
		},
		{
			// A bare local type sharing the Go member's name is no ambiguity:
			// the GALA package declares no `Builder`.
			name: "local type named like the go member leaves the go type exempt",
			file: `package repro

import "strings"

struct Builder(n int)

func f(sb *strings.Builder, b Builder) int = b.n
`,
			sibling: `package repro

import gs "martianoff/gala/strings"

func g() int = gs.S("xy").Length()
`,
		},
		{
			// With both spellings in one file the metadata cannot tell them
			// apart, so the bare name keeps its diagnostic.
			name: "bare gala name still fires when the same member is also written against the go import",
			file: `package repro

import "strings"

func f(s Str) int = 0

func g(s *strings.Str) int = 0
`,
			sibling: `package repro

import . "martianoff/gala/strings"

func h() int = S("xy").Length()
`,
			wantErr: []string{"GALA-E0025", "undefined: Str"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(tmp, "gala.mod"),
				[]byte("module example.com/repro\n\ngala dev\n"), 0644))
			file := filepath.Join(tmp, "a.gala")
			require.NoError(t, os.WriteFile(file, []byte(tc.file), 0644))
			sibling := filepath.Join(tmp, "b.gala")
			require.NoError(t, os.WriteFile(sibling, []byte(tc.sibling), 0644))

			p := transpiler.NewAntlrGalaParser()
			searchPaths := append([]string{tmp}, getStdSearchPath()...)
			batch := analyzer.NewBatchAnalyzer(p, searchPaths, tmp)
			batch.SetPackageFiles([]string{sibling})

			tree, _, err := p.Parse(tc.file)
			require.NoError(t, err)
			_, err = batch.Analyze(tree, nil, file)
			if len(tc.wantErr) == 0 {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, want := range tc.wantErr {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}
