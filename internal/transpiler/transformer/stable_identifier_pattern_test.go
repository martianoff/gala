package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// TestStableIdentifierPatternComparesByEquality pins the lowering of a bare
// capitalized identifier that names an in-scope value.
//
// GALA follows Scala here: such an identifier is a stable identifier, and the
// arm tests the subject for equality with that value. Treating it as a fresh
// binding instead turns the arm into a catch-all — every value matches the
// first such arm, the program compiles, exits 0, and answers with the wrong
// arm. The runtime consequence is pinned by examples/stable_identifier_pattern;
// these cases pin the generated shape, where a regression would first appear.
func TestStableIdentifierPatternComparesByEquality(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// wantContains is the equality test the arm must lower to.
		wantContains []string
		// wantAbsent is the binding assignment that must not appear.
		wantAbsent []string
	}{
		{
			name: "package-level val and var",
			src: `package main

type Environment string

val Development Environment = "development"
var Production Environment = "production"

func describe(env Environment) string = env match {
    case Development => "dev"
    case Production  => "prod"
    case _           => "other"
}

func main() {
    Println(describe(Production))
}`,
			wantContains: []string{"== Development.Get()", "== Production"},
			wantAbsent:   []string{"Development := obj", "Production := obj"},
		},
		{
			name: "local val",
			src: `package main

func main() {
    val Limit = 10
    val n = 3
    val r = n match {
        case Limit => "at limit"
        case _     => "elsewhere"
    }
    Println(r)
}`,
			wantContains: []string{"== Limit"},
			wantAbsent:   []string{"Limit := obj"},
		},
		{
			name: "nested inside an extractor",
			src: `package main

val Answer = 42

func main() {
    val o = Some(7)
    val r = o match {
        case Some(Answer) => "the answer"
        case Some(v)      => s"some $v"
        case None()       => "none"
    }
    Println(r)
}`,
			wantContains: []string{"== Answer.Get()"},
			wantAbsent:   []string{"Answer := "},
		},
		{
			name: "nested inside a tuple",
			src: `package main

val Answer = 42

func main() {
    val p = (7, "x")
    val r = p match {
        case (Answer, s) => s"the answer $s"
        case _           => "other"
    }
    Println(r)
}`,
			wantContains: []string{"== Answer.Get()"},
			wantAbsent:   []string{"Answer := "},
		},
		{
			name: "nested inside a struct pattern",
			src: `package main

struct Point(X int, Y int)

val Origin = 0

func main() {
    val p = Point(1, 2)
    val r = p match {
        case Point(Origin, y) => s"on the axis at $y"
        case _                => "other"
    }
    Println(r)
}`,
			wantContains: []string{"== Origin.Get()"},
			wantAbsent:   []string{"Origin := "},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, tc.src)
			require.NoError(t, err)
			for _, want := range tc.wantContains {
				require.Contains(t, out, want,
					"a capitalized in-scope value in pattern position must compare by equality")
			}
			for _, absent := range tc.wantAbsent {
				require.NotContains(t, out, absent,
					"a capitalized in-scope value in pattern position must not bind a fresh variable")
			}
		})
	}
}

// TestStableIdentifierPatternBoundary pins what stays a binding. Only a
// CAPITALIZED identifier naming an in-scope value is a stable identifier; a
// lowercase identifier is always a fresh binding (it may shadow an outer
// value), and a capitalized identifier that names no value binds as before.
func TestStableIdentifierPatternBoundary(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantBinds string
	}{
		{
			name: "lowercase name shadows an in-scope value",
			src: `package main

func main() {
    val limit = 10
    val r = 3 match {
        case limit => s"bound $limit"
    }
    Println(r)
}`,
			wantBinds: "limit := obj",
		},
		{
			name: "capitalized name that is not a value",
			src: `package main

func main() {
    val r = 3 match {
        case Other => s"bound $Other"
        case _     => "other"
    }
    Println(r)
}`,
			wantBinds: "Other := obj",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, tc.src)
			require.NoError(t, err)
			require.Contains(t, out, tc.wantBinds)
		})
	}
}

// TestStableIdentifierPatternScope pins which in-scope names count as values:
// parameters and an enclosing arm's bindings do, a qualified name always
// compares, and a name bound earlier in the same pattern is rejected rather
// than silently turned into an equality test between two parts.
func TestStableIdentifierPatternScope(t *testing.T) {
	cases := []struct {
		name         string
		src          string
		wantContains []string
		wantAbsent   []string
		wantErr      string
	}{
		{
			name: "capitalized parameter",
			src: `package main

func at(n int, Limit int) string = n match {
    case Limit => "at"
    case _     => "not"
}

func main() {
    Println(at(1, 2))
}`,
			wantContains: []string{"== Limit"},
			wantAbsent:   []string{"Limit := obj"},
		},
		{
			name: "binding of an enclosing arm",
			src: `package main

func same(o Option[int], n int) string = o match {
    case Some(K) => n match {
        case K => "same"
        case _ => "differs"
    }
    case _ => "none"
}

func main() {
    Println(same(Some(1), 1))
}`,
			wantContains: []string{"== K"},
		},
		{
			name: "qualified Go constant",
			src: `package main

import "math"

func top(n int) string = n match {
    case math.MaxInt8 => "max"
    case _            => "other"
}

func main() {
    Println(top(127))
}`,
			wantContains: []string{"== math.MaxInt8"},
			wantAbsent:   []string{"math := obj"},
		},
		{
			name: "capitalized name repeated in one pattern",
			src: `package main

func main() {
    val r = (1, 1) match {
        case (X, X) => "same"
        case _      => "differ"
    }
    Println(r)
}`,
			wantErr: "'X' is bound more than once in this pattern",
		},
		{
			name: "lowercase name repeated in one pattern",
			src: `package main

func main() {
    val r = (1, 1) match {
        case (x, x) => "same"
        case _      => "differ"
    }
    Println(r)
}`,
			wantErr: "'x' is bound more than once in this pattern",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, tc.src)
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			for _, want := range tc.wantContains {
				require.Contains(t, out, want)
			}
			for _, absent := range tc.wantAbsent {
				require.NotContains(t, out, absent)
			}
		})
	}
}

// TestStableIdentifierPatternGoConstant covers values declared in a
// hand-written Go file of the same package. A file holding only constants must
// be visible. When an import shares the package's name (GALA's `fs` importing
// Go's `io/fs`, under any alias) the same-package lookup is ambiguous, so the
// name keeps binding rather than comparing against a constant that may belong
// to the import.
func TestStableIdentifierPatternGoConstant(t *testing.T) {
	cases := []struct {
		name    string
		pkg     string
		goSrc   string
		galaSrc string
		want    string
		notWant string
	}{
		{
			name:  "constant-only Go file",
			pkg:   "cfg",
			goSrc: "package cfg\n\nconst Development = \"development\"\n",
			galaSrc: `package cfg

func Describe(env string) string = env match {
    case Development => "dev"
    case _           => "other"
}
`,
			want:    "== Development",
			notWant: "Development := obj",
		},
		{
			name:  "import sharing the package name",
			pkg:   "fs",
			goSrc: "package fs\n\nconst ModeDir = 1\n",
			galaSrc: `package fs

import iofs "io/fs"

func Kind(m int) string = m match {
    case ModeDir => s"bound $ModeDir"
    case _       => "other"
}

func Perm() iofs.FileMode = iofs.ModePerm
`,
			want:    "ModeDir := obj",
			notWant: "== ModeDir",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), tc.pkg)
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "consts.go"), []byte(tc.goSrc), 0o644))
			galaPath := filepath.Join(dir, "lib.gala")
			require.NoError(t, os.WriteFile(galaPath, []byte(tc.galaSrc), 0o644))

			p := transpiler.NewAntlrGalaParser()
			tree, _, err := p.Parse(tc.galaSrc)
			require.NoError(t, err)
			richAST, err := analyzer.NewGalaAnalyzer(p, getStdSearchPath()).Analyze(tree, nil, galaPath)
			require.NoError(t, err)
			fset, file, err := transformer.NewGalaASTTransformer().Transform(richAST)
			require.NoError(t, err)
			out, err := generator.NewGoCodeGenerator().Generate(fset, file)
			require.NoError(t, err)
			checkGeneratedGo(t, out)

			require.Contains(t, out, tc.want)
			require.NotContains(t, out, tc.notWant)
		})
	}
}
