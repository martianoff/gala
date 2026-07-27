package analyzer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
)

// analyzeSources writes each named source into a fresh temp module and
// analyzes `main`, with the other files registered as package siblings. It
// returns the error the analyzer produced for the main file (nil on success).
func analyzeSources(t *testing.T, main string, siblings map[string]string) error {
	t.Helper()
	tmp := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "gala.mod"),
		[]byte("module example.com/undef\n\ngala dev\n"), 0644))

	mainFile := filepath.Join(tmp, "main.gala")
	require.NoError(t, os.WriteFile(mainFile, []byte(main), 0644))

	var sibPaths []string
	for name, src := range siblings {
		p := filepath.Join(tmp, name)
		require.NoError(t, os.WriteFile(p, []byte(src), 0644))
		sibPaths = append(sibPaths, p)
	}

	p := transpiler.NewAntlrGalaParser()
	searchPaths := append([]string{tmp}, getStdSearchPath()...)
	batch := analyzer.NewBatchAnalyzer(p, searchPaths, tmp)
	batch.SetPackageFiles(sibPaths)

	tree, err := p.Parse(main)
	require.NoError(t, err, "test source must parse")
	_, err = batch.Analyze(tree, mainFile)
	return err
}

// TestUndefinedSymbol_Reported covers the names that must now be rejected.
// Each case is a program whose only defect is a reference that resolves to
// nothing at all — no import would make it legal, because the name is
// declared nowhere.
func TestUndefinedSymbol_Reported(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		symbol   string
		siblings map[string]string
	}{
		{
			name: "undefined variable in expression",
			src: `package main

func main() {
    Println(x + 1)
}
`,
			symbol: "x",
		},
		{
			name: "undefined function call target",
			src: `package main

func main() {
    Println(computeTotal(3))
}
`,
			symbol: "computeTotal",
		},
		{
			name: "undefined constructor in value position",
			src: `package main

func main() {
    val p = MissingPoint(1, 2)
    Println(p)
}
`,
			symbol: "MissingPoint",
		},
		{
			name: "undefined name inside a lambda body",
			src: `package main

func apply(f func(int) int, v int) int = f(v)

func main() {
    Println(apply((n) => n + missingOffset, 1))
}
`,
			symbol: "missingOffset",
		},
		{
			name: "undefined name in a package-level val initializer",
			src: `package main

val greeting = missingPrefix

func main() {
    Println(greeting)
}
`,
			symbol: "missingPrefix",
		},
		{
			name: "undefined name in a match arm body",
			src: `package main

func classify(v int) string = v match {
    case 0 => "zero"
    case _ => missingLabel
}

func main() {
    Println(classify(1))
}
`,
			symbol: "missingLabel",
		},
		{
			name: "reference to a binding that is out of scope",
			src: `package main

func main() {
    if (true) {
        val inner = 1
        Println(inner)
    } else {
        Println(0)
    }
    Println(inner)
}
`,
			symbol: "inner",
		},
		{
			name: "sibling-file local is not package scope",
			src: `package main

func main() {
    Println(helperLocal)
}
`,
			siblings: map[string]string{
				"other.gala": `package main

func helper() int {
    val helperLocal = 7
    return helperLocal
}
`,
			},
			symbol: "helperLocal",
		},
		{
			// The body of an interpolated string is a single lexer token, so
			// its embedded expressions are not parse-tree children. The shared
			// walker re-parses them, which is the only reason this is caught.
			name: "undefined name inside an interpolated string",
			src: `package main

func main() {
    Println(s"total=$missingTotal")
}
`,
			symbol: "missingTotal",
		},
		{
			// The `${...}` form, with a call and a method chain inside it.
			name: "undefined call target inside an interpolation expression",
			src: `package main

func main() {
    Println(s"labels=${missingLabels(3).Size()}")
}
`,
			symbol: "missingLabels",
		},
		{
			// A format string's `%spec` must not confuse the splitter into
			// hiding the expression before it.
			name: "undefined name inside a format string",
			src: `package main

func main() {
    Println(f"n=${missingCount}%04d")
}
`,
			symbol: "missingCount",
		},
		{
			// A lambda's parameter default is walked, like a function
			// declaration's. Before the walkers were unified this was a
			// documented hole.
			name: "undefined name in a lambda parameter default",
			src: `package main

func apply(f func(int) int) int = f(1)

func main() {
    Println(apply((x int = missingDefault) => x))
}
`,
			symbol: "missingDefault",
		},
		{
			// `for i, v = range xs` ASSIGNS to existing variables, so an
			// undeclared one is a reference, not a binding. The `:=` form
			// binds and is covered by the negative cases.
			name: "undeclared variable in an assigning range clause",
			src: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2, 3)
    for i, v = range xs.ToGoSlice() {
        Println(v)
    }
    Println(i)
}
`,
			symbol: "i",
		},
		{
			// A healthy Go dot-import must not stand the check down for the
			// whole file: `math` contributes its exports, so every other name
			// is still checked.
			name: "go dot-import does not disable the check",
			src: `package main

import . "math"

func main() {
    Println(Sqrt(4.0))
    Println(missingAfterGoDotImport)
}
`,
			symbol: "missingAfterGoDotImport",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := analyzeSources(t, tc.src, tc.siblings)
			require.Error(t, err, "expected the undefined symbol to be rejected")
			assert.Contains(t, err.Error(), "GALA-E0023")
			assert.Contains(t, err.Error(), "undefined: "+tc.symbol)
		})
	}
}

// TestUndefinedSymbol_InterpolationPositionIsTheLiteral pins where the caret
// lands for a name found inside an interpolated string. The fragment is
// re-parsed as its own token stream, so its tokens carry fragment-relative
// positions; reporting one verbatim would put the caret on line 1. The
// reference is attributed to the enclosing literal instead.
func TestUndefinedSymbol_InterpolationPositionIsTheLiteral(t *testing.T) {
	err := analyzeSources(t, `package main

func main() {
    val ok = 1
    Println(ok)
    Println(s"v=$missingInterpName")
}
`, nil)
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "undefined: missingInterpName")
	// The literal is on line 6; line 1 would mean the fragment's own position
	// leaked through.
	assert.Contains(t, msg, "line 6:", "caret must point at the literal's line, got: %s", msg)
}

// TestUndefinedSymbol_NoFalsePositives is the guard rail. Every case here is
// legal GALA whose identifiers all resolve; a firing means the check's scope
// model has a hole, not that the program is wrong.
func TestUndefinedSymbol_NoFalsePositives(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		siblings map[string]string
	}{
		{
			name: "locals, params and shadowing",
			src: `package main

func scale(factor int, base int) int {
    val doubled = base * 2
    var total = doubled + factor
    for i := 0; i < 3; i++ {
        total = total + i
    }
    return total
}

func main() {
    val factor = 2
    Println(scale(factor, 3))
}
`,
		},
		{
			name: "lambda parameters and nested lambda shadowing",
			src: `package main

func twice(f func(int) int, v int) int = f(f(v))

func main() {
    Println(twice((v) => {
        val inner = (v int) => v * 3
        return inner(v)
    }, 2))
}
`,
		},
		{
			name: "forward reference within the same file",
			src: `package main

func main() {
    Println(later(1))
}

func later(v int) int = v + 1
`,
		},
		{
			name: "recursive and mutually recursive functions",
			src: `package main

func isEven(n int) bool = if (n == 0) true else isOdd(n - 1)

func isOdd(n int) bool = if (n == 0) false else isEven(n - 1)

func main() {
    Println(isEven(4))
}
`,
		},
		{
			name: "sibling-file declarations",
			src: `package main

func main() {
    val c = Counter(3)
    Println(bump(c).Value)
}
`,
			siblings: map[string]string{
				"counter.gala": `package main

struct Counter(Value int)

func bump(c Counter) Counter = Counter(c.Value + 1)
`,
			},
		},
		{
			name: "dot-imported collection package",
			src: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val labels = ArrayTabulate(3, (i) => s"item-$i")
    Println(labels.Size())
}
`,
		},
		{
			name: "qualified import and alias",
			src: `package main

import ci "martianoff/gala/collection_immutable"

func main() {
    val labels = ci.ArrayTabulate(2, (i) => i * 2)
    Println(labels.Size())
}
`,
		},
		{
			name: "auto-imported std prelude",
			src: `package main

func head(v int) Option[int] = if (v > 0) Some(v) else None[int]()

func main() {
    val r = head(1) match {
        case Some(v) => v
        case None() => 0
    }
    Println(r)
}
`,
		},
		{
			name: "go interop symbols",
			src: `package main

import . "martianoff/gala/go_interop"

func main() {
    val s = SliceOf(1, 2, 3)
    Println(SliceCap(s))
}
`,
		},
		{
			name: "go stdlib package qualifier",
			src: `package main

import "os"

func main() {
    Println(os.Getenv("HOME"))
}
`,
		},
		{
			name: "pattern bindings and guards",
			src: `package main

sealed type Shape {
    case Circle(R int)
    case Rect(W int, H int)
}

func area(s Shape) int = s match {
    case Circle(r) if r > 0 => r * r * 3
    case Circle(r) => r
    case Rect(w, h) => w * h
}

func main() {
    Println(area(Circle(2)))
}
`,
		},
		{
			name: "method receivers and receiver type parameters",
			src: `package main

struct Box[T any](Value T)

func (b Box[T]) Get() T = b.Value

func main() {
    val b = Box[int](5)
    Println(b.Get())
}
`,
		},
		{
			name: "pointer receiver carries the type parameter",
			src: `package main

type Cell[T any] struct {
    var value T
}

func NewCell[T any](v T) *Cell[T] = &Cell[T](value = v)

func (c *Cell[T]) GetOption() Option[T] = Some[T](c.value)

func main() {
    NewCell(4).GetOption() match {
        case Some(v) => Println(v)
        case None()  => Println("none")
    }
}
`,
		},
		{
			name: "generic method called in its generated function form",
			src: `package main

struct Holder[T any](Value T)

func (h Holder[T]) MapTo[U any](f func(T) U) U = f(h.Value)

func main() {
    val h = Holder[int](3)
    Println(Holder_MapTo[int, string](h, (v) => s"v=$v"))
}
`,
		},
		{
			// Everything an interpolation can legally reference: a parameter,
			// a local, a lambda parameter, a package-level val, a method chain
			// and a call. Re-parsing these bodies is what closed the
			// interpolation escape, so this is where a regression would land.
			name: "interpolated strings reference bindings of every kind",
			src: `package main

import . "martianoff/gala/collection_immutable"

val prefix = "p"

func label(n int) string {
    val local = n * 2
    val xs = ArrayOf(1, 2, 3)
    val each = xs.Map((i) => s"i=$i/${i * 2}")
    return s"$prefix:$n:$local:${each.Size()}:${xs.MkString(",")}"
}

func main() {
    Println(label(1))
    Println(f"padded=${label(2)}%10s")
}
`,
		},
		{
			// `$$` is a literal dollar and must not be parsed as a reference;
			// neither must a bare `$` followed by punctuation.
			name: "interpolation escapes are not references",
			src: `package main

func main() {
    val amount = 5
    Println(s"cost=$$$amount")
}
`,
		},
		{
			// The precise pattern split must still bind capture names, and
			// must resolve constructors that genuinely exist — including a
			// nested extractor and a tuple pattern.
			name: "nested and tuple patterns bind their captures",
			src: `package main

sealed type Tree {
    case Leaf(V int)
    case Node(L Tree, R Tree)
}

func sum(t Tree) int = t match {
    case Leaf(v)             => v
    case Node(Leaf(a), rest) => a + sum(rest)
    case Node(l, r)          => sum(l) + sum(r)
}

func pair() int {
    val (a, b) = Tuple(1, 2)
    return a + b
}

func main() {
    Println(sum(Leaf(1)) + pair())
}
`,
		},
		{
			// A Go dot-import whose exports the analyzer can enumerate keeps
			// the file checked AND resolves its own unqualified names.
			name: "go dot-import resolves its unqualified exports",
			src: `package main

import . "math"

func main() {
    Println(Sqrt(Abs(-4.0)))
}
`,
		},
		{
			name: "use binding",
			src: `package main

import . "martianoff/gala/io"

func main() {
    Println("ready")
}
`,
		},
		{
			name: "named arguments and default parameters",
			src: `package main

struct Server(Host string, Port int)

func listen(host string = "localhost", port int = 8080) string = s"$host:$port"

func main() {
    val srv = Server(Host = "a", Port = 1)
    Println(listen(port = 9000))
    Println(srv.Host)
}
`,
		},
		{
			name: "type parameters and explicit type arguments",
			src: `package main

func identity[T any](v T) T = v

func main() {
    Println(identity[int](3))
}
`,
		},
		{
			name: "range loop and tuple destructuring",
			src: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val pair = Tuple(1, 2)
    val (a, b) = pair
    val xs = ArrayOf(1, 2, 3)
    var sum = a + b
    xs.Foreach((v) => Println(v))
    Println(sum)
}
`,
		},
		{
			name: "go builtin keeps its own diagnostic owner",
			src: `package main

func main() {
    val xs = "hello"
    Println(xs.Size())
}
`,
		},
		{
			name: "package-level val and embed-style declarations",
			src: `package main

val defaultName = "gala"
var counter = 0

func main() {
    counter = counter + 1
    Println(s"$defaultName $counter")
}
`,
		},
		{
			name: "local function declaration used before its definition",
			src: `package main

func main() {
    func helper(v int) int = v * 2
    Println(helper(2))
}
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := analyzeSources(t, tc.src, tc.siblings)
			if err != nil && strings.Contains(err.Error(), "GALA-E0023") {
				t.Fatalf("undefined-symbol check fired on legal code: %v", err)
			}
		})
	}
}

// analyzeInModule writes a whole temp module — `files` maps a module-relative
// path to its contents — and analyzes `mainRel`. It is the hermetic variant of
// analyzeSources, used where the test needs to control which packages exist on
// the search path.
func analyzeInModule(t *testing.T, mainRel string, files map[string]string) error {
	t.Helper()
	tmp := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "gala.mod"),
		[]byte("module example.com/hints\n\ngala dev\n"), 0644))
	for rel, src := range files {
		p := filepath.Join(tmp, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0755))
		require.NoError(t, os.WriteFile(p, []byte(src), 0644))
	}
	p := transpiler.NewAntlrGalaParser()
	batch := analyzer.NewBatchAnalyzer(p, append([]string{tmp}, getStdSearchPath()...), tmp)
	mainSrc := files[mainRel]
	tree, err := p.Parse(mainSrc)
	require.NoError(t, err, "test source must parse")
	_, err = batch.Analyze(tree, filepath.Join(tmp, filepath.FromSlash(mainRel)))
	return err
}

// TestUndefinedSymbol_HintNamesTheMissingImport checks the diagnostic's most
// valuable half: when exactly one package on the search paths declares the
// unresolved name, the hint spells out both import forms verbatim.
func TestUndefinedSymbol_HintNamesTheMissingImport(t *testing.T) {
	err := analyzeInModule(t, "main.gala", map[string]string{
		"labels/labels.gala": `package labels

func MakeLabel(n int) string = s"item-$n"
`,
		"main.gala": `package main

func main() {
    Println(MakeLabel(3))
}
`,
	})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "GALA-E0023")
	assert.Contains(t, msg, "undefined: MakeLabel")
	assert.Contains(t, msg, "import . \"example.com/hints/labels\"")
	assert.Contains(t, msg, "labels.MakeLabel")
}

// TestUndefinedSymbol_HintListsEveryCandidateImport covers the ambiguous
// shape: when several packages declare the name, the hint must offer all of
// them rather than silently picking one. (This is the real-world
// `ArrayTabulate` situation — both collection_immutable and collection_mutable
// declare it.)
func TestUndefinedSymbol_HintListsEveryCandidateImport(t *testing.T) {
	err := analyzeInModule(t, "main.gala", map[string]string{
		"immutable/coll.gala": `package immutable

func Tabulate(n int) int = n
`,
		"mutable/coll.gala": `package mutable

func Tabulate(n int) int = n + 1
`,
		"main.gala": `package main

func main() {
    Println(Tabulate(3))
}
`,
	})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "GALA-E0023")
	assert.Contains(t, msg, "undefined: Tabulate")
	assert.Contains(t, msg, "\"example.com/hints/immutable\"")
	assert.Contains(t, msg, "\"example.com/hints/mutable\"")
}

// TestUndefinedSymbol_HintFindsGoDeclaredPackage pins that the hint's
// declaration scan also covers a GALA package whose exports are hand-written
// Go (go_interop's shape) — the import a user forgets just as often.
func TestUndefinedSymbol_HintFindsGoDeclaredPackage(t *testing.T) {
	err := analyzeInModule(t, "main.gala", map[string]string{
		"interop/types.go": `package interop

// SliceLike is a hand-written Go export of a GALA-importable package.
func SliceLike(n int) []int { return make([]int, n) }
`,
		"main.gala": `package main

func main() {
    Println(SliceLike(3))
}
`,
	})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "GALA-E0023")
	assert.Contains(t, msg, "undefined: SliceLike")
	assert.Contains(t, msg, "\"example.com/hints/interop\"")
}

// TestUndefinedSymbol_NoHintWhenNothingDeclaresIt covers the fallback text for
// a name no package on the search paths supplies.
func TestUndefinedSymbol_NoHintWhenNothingDeclaresIt(t *testing.T) {
	err := analyzeSources(t, `package main

func main() {
    Println(x + 1)
}
`, nil)
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "GALA-E0023")
	assert.Contains(t, msg, "undefined: x")
	assert.Contains(t, msg, "check the spelling")
}

// TestUndefinedSymbol_ImportedPackageValResolves covers the export kind the
// merged metadata does not model: a package-level `val` in a dot-imported
// package. Its name reaches the symbol table only through the check's
// declaration scan, so a regression there surfaces as a false positive here.
func TestUndefinedSymbol_ImportedPackageValResolves(t *testing.T) {
	err := analyzeInModule(t, "main.gala", map[string]string{
		"conf/conf.gala": `package conf

val DefaultHost = "localhost"

func Describe() string = DefaultHost
`,
		"main.gala": `package main

import . "example.com/hints/conf"

func main() {
    Println(DefaultHost)
}
`,
	})
	if err != nil && strings.Contains(err.Error(), "GALA-E0023") {
		t.Fatalf("a dot-imported package-level val must resolve: %v", err)
	}
}

// TestUndefinedSymbol_StandsDownOnUnloadableImport pins the safety valve: a
// file whose import could not be analyzed is not checked at all, because the
// symbol table is missing that package's exports through no fault of the
// author's.
func TestUndefinedSymbol_StandsDownOnUnloadableImport(t *testing.T) {
	src := `package main

import . "example.com/undef/nowhere"

func main() {
    Println(SomethingFromNowhere())
}
`
	err := analyzeSources(t, src, nil)
	if err != nil {
		assert.NotContains(t, err.Error(), "GALA-E0023",
			"an unloadable import must suppress the undefined-symbol check")
	}
}

// TestCrossPackageImportCheckStillFires guards the neighbouring contract: the
// explicit-import check (GALA-E0025) keeps rejecting a signature type that
// only a sibling file imported. The undefined-symbol check is deliberately
// permissive about which package a name came from so these two codes do not
// overlap.
func TestCrossPackageImportCheckStillFires(t *testing.T) {
	tmp := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "gala.mod"),
		[]byte("module example.com/undef\n\ngala dev\n"), 0644))

	importer := filepath.Join(tmp, "importer.gala")
	require.NoError(t, os.WriteFile(importer, []byte(
		"package lib\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc seed() Array[int] = ArrayOf(1)\n"), 0644))

	user := filepath.Join(tmp, "user.gala")
	userSrc := "package lib\n\nfunc widen(a Array[int]) Array[int] = a\n"
	require.NoError(t, os.WriteFile(user, []byte(userSrc), 0644))

	p := transpiler.NewAntlrGalaParser()
	searchPaths := append([]string{tmp}, getStdSearchPath()...)
	batch := analyzer.NewBatchAnalyzer(p, searchPaths, tmp)
	batch.SetPackageFiles([]string{importer})

	tree, err := p.Parse(userSrc)
	require.NoError(t, err)
	_, err = batch.Analyze(tree, user)
	require.Error(t, err, "a sibling's import must not satisfy this file's use of Array")
	assert.Contains(t, err.Error(), "GALA-E0025")
}
