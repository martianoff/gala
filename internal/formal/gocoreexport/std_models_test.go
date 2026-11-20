package gocoreexport_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	goprinter "go/printer"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/runfiles"
	"github.com/stretchr/testify/require"
)

// stdGoFiles parses the hand-written Go files of GALA's standard library
// (std/*.go), without comments, by base name.
func stdGoFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	files := map[string]*ast.File{}
	for _, loc := range strings.Fields(os.Getenv("STDLIB_SOURCES")) {
		if !strings.HasSuffix(loc, ".go") || !strings.Contains(loc, "/std/") {
			continue
		}
		path, err := runfiles.Rlocation(loc)
		require.NoError(t, err)
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		require.NoError(t, err)
		files[filepath.Base(path)] = f
	}
	require.Contains(t, files, "types.go")
	return files
}

// printed is a declaration as go/printer writes it, without comments.
func printed(t *testing.T, n ast.Node) string {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, goprinter.Fprint(&b, token.NewFileSet(), n))
	return b.String()
}

// TestRecoveredErrorShape: GoCore replaces std's recoveredError by a model
// (libCall's "recoveredError" in formal/Gala/GoCore/Eval.lean) that follows
// this code exactly, with the recorded stack left nil, and panicError's fields
// with it, and its Format. A change here must change the model too; then update
// these texts.
func TestRecoveredErrorShape(t *testing.T) {
	f := stdGoFiles(t)["types.go"]
	var fn, typ, format string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.Name == "recoveredError" {
				fn = printed(t, d)
			}
			if d.Recv != nil && d.Name.Name == "Format" && printed(t, d.Recv.List[0].Type) == "*panicError" {
				format = printed(t, d)
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				if ts, ok := s.(*ast.TypeSpec); ok && ts.Name.Name == "panicError" {
					typ = printed(t, ts)
				}
			}
		}
	}
	require.Equal(t, `func recoveredError(r any) error {
	var err error
	switch e := r.(type) {
	case *panicError:
		return e
	case error:
		err = e
	case string:
		err = errors.New(e)
	default:
		err = fmt.Errorf("panic: %v", r)
	}
	var pcs [maxPanicFrames]uintptr
	n := runtime.Callers(2, pcs[:])
	return &panicError{err: err, pcs: append([]uintptr(nil), pcs[:n]...)}
}`, fn, "std's recoveredError changed: update GoCore's model of it")
	// fmtValue and fmtSpec print a *panicError as its err, with the same verb and flags.
	require.Equal(t, "func (e *panicError) Format(s fmt.State, verb rune) {\n\tfmt.Fprintf(s, fmt.FormatString(s, verb), e.err)\n}",
		format, "std's (*panicError).Format changed: update GoCore's printing of it")
	require.Equal(t, "panicError struct {\n\terr\terror\n\tpcs\t[]uintptr\n}", typ, "std's panicError changed: update GoCore's model of recoveredError")
}

// TestPanicErrorPcsReaders: GoCore's recoveredError leaves a panicError's pcs
// nil, which is right only while nothing GoCore runs reads them. In std, only
// (*panicError).stack does, through runtime.CallersFrames, which GoCore does
// not run (runtime is not in its document), so any path to it ends
// unsupported.
func TestPanicErrorPcsReaders(t *testing.T) {
	stack := ""
	for name, f := range stdGoFiles(t) {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "stack" && fd.Recv != nil &&
				len(fd.Recv.List) == 1 && printed(t, fd.Recv.List[0].Type) == "*panicError" {
				stack = printed(t, fd)
				continue
			}
			ast.Inspect(d, func(n ast.Node) bool {
				if s, ok := n.(*ast.SelectorExpr); ok && s.Sel.Name == "pcs" {
					t.Errorf("std/%s: %s reads .pcs; GoCore's recoveredError leaves it nil", name, printed(t, s))
				}
				return true
			})
		}
	}
	require.Contains(t, stack, "runtime.CallersFrames(e.pcs)",
		"(*panicError).stack no longer reads the pcs through runtime.CallersFrames")
	// std's GALA sources compile into the same Go package, so they could read the
	// unexported field too.
	pcs := regexp.MustCompile(`\.pcs\b`)
	for _, loc := range strings.Fields(os.Getenv("STDLIB_SOURCES")) {
		if !strings.HasSuffix(loc, ".gala") || !strings.Contains(loc, "/std/") {
			continue
		}
		path, err := runfiles.Rlocation(loc)
		require.NoError(t, err)
		text, err := os.ReadFile(path)
		require.NoError(t, err)
		require.False(t, pcs.Match(text), "%s reads .pcs; GoCore's recoveredError leaves it nil", loc)
	}
}
