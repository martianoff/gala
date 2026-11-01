package transformer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// typeRun is one transpile of a file: its Go, or its error, and the lines of
// the types and sealed type declarations its walk lowered off the native
// tree.
type typeRun struct {
	out       string
	err       error
	lines     map[int]bool
	converted bool
}

// transpileTypes transpiles src as walkPipeline.transpile does, with the
// import search path searchPaths, recording the types lowered off the
// native tree. antlrOnly makes the transformer walk ANTLR's tree whatever
// the parse came with.
func transpileTypes(searchPaths []string, antlrOnly bool, path, src string) typeRun {
	tr := transformer.NewGalaASTTransformer()
	run := typeRun{lines: map[int]bool{}}
	transformer.OnNativeType(tr, func(line int) { run.lines[line] = true })
	if antlrOnly {
		tr = withoutNative{tr}
	}
	p := &handleParser{GalaParser: transpiler.NewAntlrGalaParser()}
	run.out, run.err = transpiler.NewGalaToGoTranspiler(p, analyzer.NewGalaAnalyzer(p, searchPaths), tr, generator.NewGoCodeGenerator()).Transpile(src, path)
	run.converted = p.compiled.Converted()
	return run
}

// markedLines is the lines of src that hold a `/* ü */` marker.
func markedLines(src string) []int {
	var lines []int
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, "/* ü */") {
			lines = append(lines, i+1)
		}
	}
	return lines
}

// typeParamMisuse is the message, which has no code, of a type parameter
// given type arguments.
const typeParamMisuse = "is a type parameter of the enclosing declaration and takes no type arguments"

// typeCases cover each type shape lowerType reads, each place a declaration
// holds a type the native walk reads, the sealed and opaque type
// declarations, and each diagnostic they position, after multibyte text: a
// position read off the wrong tree, or a column counted in bytes, shows as a
// differing error. Every `/* ü */` marks a line where a type or a sealed
// type declaration must be lowered off the native tree, so a case compares
// the native walk with ANTLR's, not ANTLR's with itself.
var typeCases = []struct {
	name, src string
	// want, set on a case that fails, is in its error: the diagnostic code,
	// or the message where the diagnostic has no code.
	want string
}{
	{name: "type shapes", src: `package main

import (
	"strings"
	. "martianoff/gala/collection_immutable"
)

struct Box[T any](v T)

type Shapes struct {
	/* ü */ named int
	/* ü */ qualified strings.Builder
	/* ü */ generic Box[string]
	/* ü */ nested Box[Box[int]]
	/* ü */ triple Tuple[int, string, bool]
	/* ü */ pair Tuple[int, string]
	/* ü */ opt Option[int]
	/* ü */ arr Array[int]
	/* ü */ ptr *Box[int]
	/* ü */ nonNull Ptr[Box[int]]
	/* ü */ slice []int
	/* ü */ m map[string]Box[int]
	/* ü */ pred func(int) bool
	/* ü */ untypedParam func(Option[int]) bool
	/* ü */ variadic func(string, ...int) string
	/* ü */ goResults func(string) (int, error)
	/* ü */ noResult func()
	/* ü */ curried func(int) func(int) int
	/* ü */ wildcard Box[_]
	/* ü */ var sendable Sendable[func() int]
}

/* ü */ type Pred func(Box[int]) bool

/* ü */ type Names Array[string]

struct Pair[A any, B any](first A, var second Option[B])

/* ü */ val typed Option[int] = Some(1)

/* ü */ var counter map[string]int

/* ü */ func (b Box[T]) Get() T = b.v

/* ü */ func (b *Box[T]) Ptr() T = b.v

/* ü */ func (b Ptr[Box[T]]) NonNull() T = b.v

func main() {
	/* ü */ val local Box[int] = Box(1)
	/* ü */ var later Option[string]
	Println(typed, counter, local, later, Pair(1, Some("ü")))
}
`},
	{name: "use annotation", src: `package main

type Handle struct {
	name string
}

func (h Handle) Close() error {
	return nil
}

func open(name string) Handle = Handle(name = name)

func main() {
	/* ü */ use h Handle = open("ü")
	Println(h.name)
}
`},
	{name: "sealed shapes", src: `package main

// Shape is sealed.
/* ü */ sealed type Shape {
	/* ü */ case Circle(radius float64)
	/* ü */ case Rect(w float64, h float64, label Option[string])
	case Empty()
	case Unit
}

/* ü */ sealed type Tree[T any] {
	/* ü */ case Leaf(value T)
	/* ü */ case Node(left Tree[T], right Tree[T])
}

/* ü */ sealed type Level {
	default case Low
	case High(by int)
}

func main() {
	Println(Circle(1.0), Rect(1.0, 2.0, None[string]()), Empty(), Leaf(1), Low(), High(2))
}
`},
	{name: "opaque shapes", src: `package main

/* ü */ opaque type Meters float64

/* ü */ opaque type Id[T any] int64

/* ü */ opaque type Flag bool

func main() {
	Println(Meters(1.5), Id[string](2), Flag(true))
}
`},
	{name: "type parameter given type arguments", want: typeParamMisuse, src: "package main\n\nstruct Box[T any](v T)\n\ntype W[T any] struct {\n\t/* ü */ b T[int]\n}\n"},
	{name: "type parameter in a local val", want: typeParamMisuse, src: "package main\n\nfunc f[T any](x T) {\n\t/* ü */ val y T[int] = x\n}\n"},
	{name: "nullable pointer as a type argument", want: "GALA-E0080", src: `package main

struct Counter(var N int)

func (c Ptr[Counter]) Bump() {
	c.N = c.N + 1
}

type Bumper interface {
	Bump()
}

struct Holder[B Bumper](b B)

type W struct {
	/* ü */ h Holder[*Counter]
}
`},
	// A sealed declaration's checks run before its cases' types are lowered,
	// so these keep the reported token on the declaration's (marked) line.
	{name: "two default cases", want: "GALA-E0074", src: "package main\n\n/* ü */ sealed type S { default case A /* ü */ default case B }\n"},
	{name: "recursive default case", want: "GALA-E0074", src: "package main\n\n/* ü */ sealed type S { default case A(/* ü */ next S) case B }\n"},
	{name: "sealed variant too wide", want: "GALA-E0072", src: "package main\n\n/* ü */ sealed type S { case /* ü */ A(a int, b int, c int, d int, e int, f int, g int, h int, i int, j int, k int) }\n"},
	{name: "self-containing sealed", want: "GALA-E0077", src: "package main\n\n/* ü */ sealed type S {\n\tcase A(i S2)\n}\n\nsealed type S2 {\n\tcase B(s S)\n}\n"},
	{name: "opaque over a type parameter", want: "GALA-E0062", src: "package main\n\n/* ü */ opaque type Id[T any] T\n"},
	{name: "opaque over a struct", want: "GALA-E0062", src: "package main\n\nstruct P(x int)\n\n/* ü */ opaque type O  P\n"},
	{name: "opaque over a generic type", want: "GALA-E0062", src: "package main\n\nstruct Box[T any](v T)\n\n/* ü */ opaque type O Box[  int ]\n"},
}

// TestNativeTypeCases holds the lowering of types and of sealed and opaque
// type declarations off the native tree to their lowering off ANTLR's, over
// typeCases: the same Go, or the same error, both ways, with every marked
// line's type lowered off the native tree.
func TestNativeTypeCases(t *testing.T) {
	galaparser.RequestEngine(string(galaparser.EngineGalop))
	t.Cleanup(func() { galaparser.RequestEngine("") })
	stdFile, err := bazel.Runfile("std/option.gala")
	require.NoError(t, err, "std sources not staged")
	searchPaths := []string{filepath.Dir(filepath.Dir(stdFile))}
	dir := t.TempDir()
	for _, c := range typeCases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(c.name, " ", "_"), "main.gala")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(c.src), 0o644))
			require.NotNil(t, transpiler.NewAntlrGalaParser().ParseFile(c.src).CleanNative(), "the case does not parse cleanly with galop")
			n, a := transpileTypes(searchPaths, false, path, c.src), transpileTypes(searchPaths, true, path, c.src)
			checkGolden(t, path, searchPaths, n.out, n.err)
			require.True(t, n.converted, "the analysis did not convert the native tree, so the walk read ANTLR's")
			assert.Empty(t, a.lines, "the ANTLR walk lowered types off the native tree")
			if c.want != "" {
				require.ErrorContains(t, n.err, c.want, "the case does not fail with its diagnostic")
			} else {
				require.NoError(t, n.err, "the case does not transpile")
			}
			if n.out != a.out {
				assert.Failf(t, "the walks' Go differs", "the native walk's Go differs from the ANTLR walk's at %s", firstLineDiff(n.out, a.out))
			}
			assert.Equal(t, a.err, n.err, "the native walk's error differs from the ANTLR walk's")
			lines := markedLines(c.src)
			require.NotEmpty(t, lines, "the case marks no line")
			for _, line := range lines {
				assert.Truef(t, n.lines[line], "no type on line %d was lowered off the native tree; error: %v", line, n.err)
			}
			t.Logf("error: %v", n.err)
		})
	}
}
