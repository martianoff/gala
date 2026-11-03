package analyzer

import (
	"fmt"
	"martianoff/gala/internal/transpiler"
	"os"
	"path/filepath"
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

// TestHandwrittenInputs: each handwritten input the analysis golden records
// (analysis_golden_test.go) still does what it was written to: the compiled
// file of each of collectionErrorInputs stops the analysis with an error, of
// fileCheckInputs with its code or none, of undefinedInputs with an error
// unless it is clean, and each of valInferenceInputs analyzes. So a golden
// refreshed after a change cannot quietly turn an input into one that no
// longer exercises its check.
func TestHandwrittenInputs(t *testing.T) {
	parseWithGalop(t)
	_, searchPaths := collectionCorpus(t)
	a := newCollectionAnalyzer(t, galaparser.NewAntlrGalaParser(), searchPaths)
	type input struct {
		name  string
		files [][2]string
		// fails says the analysis must stop with an error, of code when it
		// is set.
		fails bool
		code  galaerr.ErrorCode
	}
	var inputs []input
	for _, in := range collectionErrorInputs {
		inputs = append(inputs, input{"collection: " + in.name, in.files, true, ""})
	}
	for _, in := range fileCheckInputs {
		inputs = append(inputs, input{"file checks: " + in.name, in.files, in.code != "", in.code})
	}
	for _, in := range undefinedInputs {
		inputs = append(inputs, input{"undefined: " + in.name, in.files, !in.clean, ""})
	}
	for i, text := range valInferenceInputs {
		inputs = append(inputs, input{fmt.Sprintf("val inference %d", i), [][2]string{{"main.gala", text}}, false, ""})
	}
	for _, in := range inputs {
		dir := t.TempDir()
		var paths []string
		for _, f := range in.files {
			path := filepath.Join(dir, f[0])
			require.NoError(t, os.WriteFile(path, []byte(f[1]), 0o644))
			paths = append(paths, path)
		}
		var packageFiles []string
		if len(paths) > 1 {
			packageFiles = paths[1:]
		}
		an, ok := analyzeForCollection(t, a, paths[0], in.files[0][1], packageFiles)
		if !assert.True(t, ok, "%s: does not parse", in.name) {
			continue
		}
		err, _ := an.result.(error)
		if !in.fails {
			assert.NoError(t, err, "%s", in.name)
			continue
		}
		if !assert.Error(t, err, "%s", in.name) || in.code == "" {
			continue
		}
		var se *galaerr.SemanticError
		if assert.ErrorAs(t, err, &se, "%s", in.name) {
			assert.Equal(t, in.code, se.Code, "%s: %v", in.name, err)
		}
	}
}

// TestLSPBuffers: the LSP analyzer analyzes a buffer that parses cleanly on
// its native tree, and a buffer with syntax errors on the native tree of its
// error-recovered tree (Parsed.RecoveredNative), so completion and hover keep
// answering mid-edit.
func TestLSPBuffers(t *testing.T) {
	parseWithGalop(t)
	_, searchPaths := collectionCorpus(t)
	a, ok := NewGalaAnalyzerForLSP(galaparser.NewAntlrGalaParser(), searchPaths, t.TempDir()).(*galaAnalyzer)
	require.True(t, ok, "NewGalaAnalyzerForLSP gives a *galaAnalyzer")
	a.cache = nil

	path := filepath.Join(t.TempDir(), "main.gala")
	for _, c := range []struct {
		name, src string
		clean     bool
	}{
		{"clean", "package main\n\nimport . \"martianoff/gala/std\"\n\nfunc main() {\n\tval x = Some(1)\n\tPrintln(x)\n}\n", true},
		{"syntax error", "package main\n\nimport . \"martianoff/gala/std\"\n\nfunc helper() int = 1\n\nfunc main() {\n\tval x = Some(1)\n\tPrintln(x\n}\n", false},
	} {
		require.NoError(t, os.WriteFile(path, []byte(c.src), 0o644))
		parsed := a.parser.ParseFile(c.src)
		require.Equal(t, c.clean, parsed.Err() == nil, "%s: whether it parses", c.name)
		require.NotNil(t, parsed.RecoveredNative(), "%s: the parse has a native tree", c.name)
		an := analyzeParsedForCollection(t, a, path, parsed, nil)
		rich, ok := an.result.(*transpiler.RichAST)
		require.True(t, ok, "%s: analyze: %v", c.name, an.result)
		assert.Equal(t, "main", rich.PackageName, c.name)
		assert.Contains(t, rich.Functions, "main", "%s: the buffer's functions are collected", c.name)
	}
}
