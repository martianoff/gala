package analyzer

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fileCheckInputs are packages, as files, whose compiled file (the first)
// stops the analysis with the error code given, on a read of
// file_checks_native.go, or, with no code, analyzes cleanly. Between them they
// take every branch of those reads: each kind of name GALA-E0055 and
// GALA-E0066 report, a declaration preferred over an earlier use, the uses
// E0055 leaves to other checks, and multibyte text before a position.
var fileCheckInputs = []struct {
	name  string
	code  galaerr.ErrorCode
	files [][2]string
}{
	{"keyword function", galaerr.CodeGoKeywordAsName, [][2]string{{"main.gala", `package main

func go() int = 1
`}}},
	{"keyword use after multibyte text", galaerr.CodeGoKeywordAsName, [][2]string{{"main.gala", `package main

func f() string = "héllo wörld" + select
`}}},
	{"keyword declaration after its use", galaerr.CodeGoKeywordAsName, [][2]string{{"main.gala", `package main

func f() string = "héllo" + chan

val (a, chan) = ("é", 2)
`}}},
	{"keyword in a pattern", galaerr.CodeGoKeywordAsName, [][2]string{{"main.gala", `package main

func f(x int) int = x match {
    case break => 1
}
`}}},
	{"keyword left to other checks, then a bare switch", galaerr.CodeGoKeywordAsName, [][2]string{{"main.gala", `package main

func f() {
    for i := 0; i < 3; i++ {
        break
    }
    switch
}
`}}},
	{"keyword type parameter constraint", galaerr.CodeGoKeywordAsName, [][2]string{{"main.gala", `package main

func g[T goto](x T) T = x
`}}},
	{"keyword type parameter", galaerr.CodeGoKeywordAsName, [][2]string{{"main.gala", `package main

func h[const any]() int = 1
`}}},
	{"keyword struct field", galaerr.CodeGoKeywordAsName, [][2]string{{"main.gala", `package main

struct P(name string, fallthrough int)
`}}},
	{"keyword field of a struct type", galaerr.CodeGoKeywordAsName, [][2]string{{"main.gala", `package main

type P struct {
    name string
    fallthrough int
}

func f(p P) int = p.fallthrough
`}}},
	{"keyword method of an interface", galaerr.CodeGoKeywordAsName, [][2]string{{"main.gala", `package main

type Runner interface {
    go(n int) int
}

func f(r Runner) int = r.go(1)
`}}},
	{"keyword sealed case field", galaerr.CodeGoKeywordAsName, [][2]string{{"main.gala", `package main

sealed type Shape {
    case Circle(chan int)
}
`}}},
	{"keyword receiver", galaerr.CodeGoKeywordAsName, [][2]string{{"main.gala", `package main

struct P(x int)

func (select P) get() int = select.x
`}}},
	{"keyword import alias", galaerr.CodeGoKeywordAsName, [][2]string{{"main.gala", `package main

import defer "strings"

func f() string = defer.ToUpper("é")
`}}},
	{"keyword lambda parameter", galaerr.CodeGoKeywordAsName, [][2]string{{"main.gala", `package main

val f = (a string, goto int) => "é" + a
`}}},
	{"codec name declared", galaerr.CodeGeneratedCodecName, [][2]string{{"main.gala", `package main

val StructMeta_Point = 1
`}}},
	{"codec name used after multibyte text", galaerr.CodeGeneratedCodecName, [][2]string{{"main.gala", `package main

func f() string = "héllo" + _ValueMeta_Point
`}}},
	{"duplicate import", galaerr.CodeDuplicateImport, [][2]string{{"main.gala", `package main

import (
    "strings"
    s "strings"
    "strings"
)

func f() string = strings.ToUpper(s.ToLower("é"))
`}}},
	{"duplicate dot import", galaerr.CodeDuplicateImport, [][2]string{{"main.gala", `package main

import . "strings"
import . "strings"

func f() string = ToUpper("é")
`}}},
	{"import path not well formed", galaerr.CodePackageNotFound, [][2]string{{"main.gala", `package main

import "./util"

func f() int = 1
`}}},
	{"dot-import collision: function", galaerr.CodeDeclarationCollidesWithDotImport, [][2]string{{"main.gala", `package main

import . "martianoff/gala/collection_immutable"

func ArrayOf() int = 1
`}}},
	{"dot-import collision: type", galaerr.CodeDeclarationCollidesWithDotImport, [][2]string{{"main.gala", `package main

import . "martianoff/gala/collection_immutable"

struct List(x string = "é")
`}}},
	{"dot-import collision: sealed variant", galaerr.CodeDeclarationCollidesWithDotImport, [][2]string{{"main.gala", `package main

import . "martianoff/gala/collection_immutable"

sealed type Shape {
    case Circle(r int)
    case HashMap(n int)
}
`}}},
	{"dot-import collision: value", galaerr.CodeDeclarationCollidesWithDotImport, [][2]string{{"main.gala", `package main

import . "martianoff/gala/collection_immutable"

val (a, EmptyArray) = ("é", 2)
`}}},
	{"dot-import collision: opaque type", galaerr.CodeDeclarationCollidesWithDotImport, [][2]string{{"main.gala", `package main

import . "martianoff/gala/collection_immutable"

val greeting = "héllo"

opaque type HashSet int
`}}},
	{"dot-import collision: embed", galaerr.CodeDeclarationCollidesWithDotImport, [][2]string{{"main.gala", `package main

import . "martianoff/gala/collection_immutable"

embed val List = "naïve.txt"
`}}},
	{"dot-import collision: another file's dot import", galaerr.CodeDeclarationCollidesWithDotImport, [][2]string{
		{"lib.gala", `package lib

func ArrayOf() int = 1
`},
		{"other.gala", `package lib

import . "martianoff/gala/collection_immutable"

func size(xs Array[int]) int = xs.Size()
`}}},
	{"clean", "", [][2]string{
		{"lib.gala", `// Package lib is the clean case: naïve, but well formed.
package lib

import (
    "strings"
    s "strings"
    . "martianoff/gala/collection_immutable"
)

struct Point(x int, y int)

sealed type Shape {
    case Circle(r int)
    case Square(side int)
}

type Size int

val (left, right) = (1, 2)
var counter = 0

func (p Point) sum() int = p.x + p.y
func (p *Point) scaled(n int) Point = Point(p.x * n, p.y * n)
func (s Size) twice() Size = s * 2

func names(xs Array[string]) string = strings.ToUpper(s.ToLower(xs.MkString(",")))
`},
		{"more.gala", `package lib

func (s Shape) area() int = 0

func cafeAt(i int) string = "é"
`}}},
}

// fileReads is the reads of the compiled file analyze makes through readTree,
// in order.
var fileReads = []string{"go keyword names", "generated codec names", "package clause", "imports", "dot-import collisions", "top-level names"}

// TestFileChecksAgree holds analyze's reads of the compiled file on its
// native tree (file_checks_native.go) to the same reads on ANTLR's: its
// package clause, its imports, its top-level names, and the checks
// GALA-E0055, GALA-E0058 and GALA-E0066. Each GALA source staged as data
// (those TestEnginesAgree parses), each example of the error reference pages
// and each of fileCheckInputs is analyzed as the CLI analyzes a file, and
// every read is made again on the file's ANTLR tree in the same state: both
// must give the same value, an error equal in code, message, hint, position
// and span. Every read of a file with a native tree must have taken it.
// Each input of fileCheckInputs must also stop with its error, or none, and
// analyze exactly as an analyzer on ANTLR's tree alone analyzes it.
func TestFileChecksAgree(t *testing.T) {
	parseWithGalop(t)
	paths, searchPaths := collectionCorpus(t)
	p := galaparser.NewAntlrGalaParser()
	nativeA := newCollectionAnalyzer(t, p, searchPaths)
	antlrA := newCollectionAnalyzer(t, antlrOnly{p}, searchPaths)

	type input struct {
		name    string
		files   [][2]string
		subject bool
		code    galaerr.ErrorCode
	}
	var inputs []input
	for _, path := range paths {
		text, err := os.ReadFile(path)
		require.NoError(t, err)
		inputs = append(inputs, input{name: path, files: [][2]string{{path, string(text)}}})
	}
	for i, files := range docExamples(t) {
		inputs = append(inputs, input{name: fmt.Sprintf("error reference example %d (%s)", i, files[0][0]), files: files})
	}
	for _, in := range fileCheckInputs {
		inputs = append(inputs, input{in.name, in.files, true, in.code})
	}

	files, reads, errs := 0, 0, 0
	for _, in := range inputs {
		paths := []string{in.files[0][0]}
		if !filepath.IsAbs(paths[0]) {
			paths = nil
			dir := t.TempDir()
			for _, f := range in.files {
				path := filepath.Join(dir, f[0])
				require.NoError(t, os.WriteFile(path, []byte(f[1]), 0o644))
				paths = append(paths, path)
			}
		}
		// The subject alone: a package's other files are its siblings.
		var packageFiles []string
		if len(paths) > 1 {
			packageFiles = paths[1:]
		}
		name := in.name + ": " + filepath.Base(paths[0])
		parsed := nativeA.parser.ParseFile(in.files[0][1])
		if parsed.Err() != nil {
			assert.False(t, in.subject, "%s: does not parse: %v", name, parsed.Err())
			continue
		}
		// Each read is compared as it is made: what a read gives may change
		// later (the top-level names take the siblings' in).
		var made []string
		nativeA.onTreeRead = func(r treeRead) {
			if r.filePath != paths[0] {
				return
			}
			made = append(made, r.what)
			assert.True(t, r.native, "%s: the read of the %s took ANTLR's tree", name, r.what)
			assert.Equal(t, r.antlr(), r.got, "%s: the read of the %s differs between the trees", name, r.what)
			if err, ok := r.got.(error); ok && err != nil {
				errs++
			}
		}
		an := analyzeParsedForCollection(t, nativeA, paths[0], parsed, packageFiles)
		nativeA.onTreeRead = nil

		files++
		reads += len(made)
		// The reads come in order, and an analysis that succeeds makes them
		// all; one that fails stops after some.
		_, failed := an.result.(error)
		assert.NotEmpty(t, made, "%s: analyze made no read", name)
		if assert.LessOrEqual(t, len(made), len(fileReads), "%s: reads %v", name, made) {
			assert.Equal(t, fileReads[:len(made)], made, "%s: the reads", name)
		}
		if !failed {
			assert.Len(t, made, len(fileReads), "%s: reads %v", name, made)
		}
		if !in.subject {
			continue
		}
		err, _ := an.result.(error)
		if in.code == "" {
			assert.NoError(t, err, "%s", name)
		} else if assert.Error(t, err, "%s", name) {
			var se *galaerr.SemanticError
			if assert.ErrorAs(t, err, &se, "%s", name) {
				assert.Equal(t, in.code, se.Code, "%s: %v", name, err)
			}
			t.Logf("%s: %v", name, err)
		}
		a, ok := analyzeForCollection(t, antlrA, paths[0], in.files[0][1], packageFiles)
		require.True(t, ok, name)
		if diff := firstDiff(reflect.ValueOf(an.result), reflect.ValueOf(a.result), "", map[visit]bool{}); diff != "" {
			t.Errorf("%s: the analysis on the native tree differs from ANTLR's at %s", name, diff)
		}
	}
	// Every staged source parses with a native tree; floors catch the sources
	// not being staged or the native path going unused.
	require.GreaterOrEqual(t, files, 600, "files read")
	require.GreaterOrEqual(t, errs, 20, "errors found")
	t.Logf("%d files, all read on the native tree: %d reads, %d errors", files, reads, errs)
}

// TestLSPBuffersStayOnANTLR holds the LSP analyzer to ANTLR's tree: a buffer
// parsed by galop comes with a native-tree handle, but analyzing it as the
// language server does neither collects its declarations nor makes any other
// read on the native tree, so a keystroke pays for no conversion.
func TestLSPBuffersStayOnANTLR(t *testing.T) {
	parseWithGalop(t)
	_, searchPaths := collectionCorpus(t)
	a, ok := NewGalaAnalyzerForLSP(galaparser.NewAntlrGalaParser(), searchPaths, t.TempDir()).(*galaAnalyzer)
	require.True(t, ok, "NewGalaAnalyzerForLSP gives a *galaAnalyzer")
	a.cache = nil

	path := filepath.Join(t.TempDir(), "main.gala")
	src := "package main\n\nimport . \"martianoff/gala/std\"\n\nfunc main() {\n\tval x = Some(1)\n\tPrintln(x)\n}\n"
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))
	parsed := a.parser.ParseFile(src)
	require.NoError(t, parsed.Err())
	require.NotNil(t, parsed.Native, "a galop parse without syntax errors comes with a handle")

	var reads []treeRead
	a.onTreeRead = func(r treeRead) {
		if r.filePath == path {
			reads = append(reads, r)
		}
	}
	an := analyzeParsedForCollection(t, a, path, parsed, nil)
	a.onTreeRead = nil

	require.Equal(t, path, an.trace.filePath, "the buffer's declarations are collected")
	assert.True(t, an.trace.hasHandle, "the buffer's parse came with a handle")
	assert.False(t, an.trace.native, "the LSP analyzer collected the buffer on its native tree")
	require.NotEmpty(t, reads, "the buffer's file checks report their reads")
	for _, r := range reads {
		assert.False(t, r.native, "the LSP analyzer made the read of the %s on the native tree", r.what)
	}
}
