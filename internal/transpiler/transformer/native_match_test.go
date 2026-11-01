package transformer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	galaparser "martianoff/gala/internal/parser"
)

// patternShapes is the matchCases program that has every pattern form the
// native match lowering reads, and must transpile.
const patternShapes = "pattern shapes"

// matchCases cover every pattern form a match reads on the native tree, and
// each diagnostic the pattern lowering positions, after multibyte text too: a
// position read off the wrong tree, or a column counted in bytes, shows as a
// differing error. A case's `/* ü */` marker sits on the line of the case
// clause the diagnostic is about.
var matchCases = []struct{ name, src string }{
	{patternShapes, `package main

import (
	"fmt"
	"strings"

	. "martianoff/gala/collection_immutable"
)

sealed type Shape {
	case Circle(Radius int)
	case Rect(Width int, Height int)
	case Dot()
}

sealed type Color {
	case Red()
	case Green()
	case Blue()
}

struct Person(name string, age int)

struct Box[T any](v T, tag string)

opaque type UserID int64

struct MaybeInt(raw int)

func (m MaybeInt) MatchView() Option[int] = When(m.raw >= 0, m.raw)

struct Even()

func (e Even) Unapply(n int) bool = n % 2 == 0

struct Half()

func (h Half) Unapply(n int) Option[int] = if (n % 2 == 0) Some(n / 2) else None[int]()

val Limit = 10

func literals(x int) string = x match {
	case 0 => "zéro"
	case 1 | 2 | 3 => "small"
	case Limit => "limit"
	case n if n < 0 => "negative"
	case _ => "other"
}

func strs(s string) int = s match {
	case "ä" => 1
	case "b" | "c" => 2
	case other => other.Size()
}

func shapes(s Shape) int = s match {
	case Circle(r) if r > 100 => r
	case Circle(Radius = r) => r * 2
	case Rect(w, Height = h) => w * h
	case Dot => 0
}

func colors(c Color) string = c match {
	case Red | Green => "warm"
	case Blue() => "cold"
}

func tuples(p Tuple[int, string]) string = p match {
	case (0, s) => s
	case (n, "x") if n > 1 => "x"
	case (_, _) => "any"
}

func nested(o Option[Tuple[int, Option[string]]]) string = o match {
	case Some((n, Some(s))) => s"$n $s"
	case Some((_, None())) => "none inside"
	case None() => "none"
}

func people(p Person) string = p match {
	case Person("ann", a) => s"ann $a"
	case Person(n, _) if n != "" => n
	case _ => p.name
}

func boxes(x any) string = x match {
	case Box[int](v, tag) => s"$v $tag"
	case b: Person => b.name
	case n: int => s"int $n"
	case _ => "?"
}

func seqs(a Array[int]) int = a match {
	case Array() => 0
	case Array(only) => only
	case Array(first, second, rest...) => first + second + rest.Size()
	case _ => -1
}

func lists(l List[Option[int]]) int = l match {
	case List(Some(n), _...) => n
	case _ => -1
}

func users(u UserID) string = u match {
	case UserID(0) => "zero"
	case UserID(n) if n > 0 => s"$n"
	case _ => "negative"
}

func views(m MaybeInt) string = m match {
	case Some(n) => s"some $n"
	case None => "none"
}

func extractors(n int) string = n match {
	case Half(h) if h > 10 => "big half"
	case Even() => "even"
	case _ => "odd"
}

func bools(b bool) int = b match {
	case true => 1
	case false => 0
}

func blocks(x Option[int]) int {
	val r = x match {
		case Some(n) => {
			val doubled = n * 2
			if doubled > 10 {
				doubled
			} else {
				n
			}
		}
		case None() => {
			Println("ünï")
			0
		}
	}
	x match {
		case Some(n) => {
			Println(n)
		}
		case _ => Println("none")
	}
	for i := 0; i < 3; i++ {
		i match {
			case 1 => continue
			case 2 => {
				break
			}
			case _ => Println(i)
		}
	}
	r
}

func parens(x int) string = x match {
	case (1 | 2) => "one or two"
	case (3) => "three"
	case _ => "other"
}

func qualified(s string) string = strings.ToUpper(s) match {
	case "A" => fmt.Sprint("a")
	case _ => s
}

func main() {
	Println(literals(1), strs("b"), shapes(Dot()), colors(Red()), tuples((0, "z")))
	Println(nested(Some((1, Some("s")))), people(Person("ann", 3)), boxes(1), seqs(ArrayOf(1, 2, 3)))
	Println(lists(ListOf(Some(1))), users(UserID(2)), views(MaybeInt(1)), extractors(4), bools(true))
	Println(blocks(Some(3)), parens(2), qualified("a"))
}
`},
	{"non-exhaustive", "package main\n\nsealed type S {\n\tcase A()\n\tcase B()\n}\n\nfunc f(s S) int = s match {\n\t/* ü */ case A() => 1\n}\n"},
	{"non-exhaustive guarded", "package main\n\nsealed type S {\n\tcase A()\n\tcase B(n int)\n}\n\nfunc f(s S) int = s match {\n\t/* ü */ case A() => 1\n\tcase B(n) if n > 0 => n\n}\n"},
	{"missing default", "package main\n\nfunc f(x int) int = x match {\n\t/* ü */ case 1 => 1\n}\n"},
	{"multiple defaults", "package main\n\nfunc f(x int) int = x match {\n\tcase _ => 1\n\t/* ü */ case _ => 2\n}\n"},
	{"variant arity", "package main\n\nsealed type S {\n\tcase A(x int, y int)\n\tcase B()\n}\n\nfunc f(s S) int = s match {\n\t/* ü */ case A(x) => x\n\tcase _ => 0\n}\n"},
	{"variant arity in an alternative", "package main\n\nsealed type S {\n\tcase A(x int)\n\tcase B()\n}\n\nfunc f(s S) int = s match {\n\t/* ü */ case B() | A() => 1\n\tcase _ => 0\n}\n"},
	{"named field twice", "package main\n\nsealed type S {\n\tcase R(w int, h int)\n}\n\nfunc f(s S) int = s match {\n\t/* ü */ case R(h = a, h = b) => a + b\n}\n"},
	{"positional after named", "package main\n\nsealed type S {\n\tcase R(w int, h int)\n}\n\nfunc f(s S) int = s match {\n\t/* ü */ case R(h = a, 1) => a\n}\n"},
	{"no such field", "package main\n\nsealed type S {\n\tcase R(w int, h int)\n}\n\nfunc f(s S) int = s match {\n\t/* ü */ case R(d = a) => a\n}\n"},
	{"named field of an extractor", "package main\n\nstruct Half()\n\nfunc (h Half) Unapply(n int) Option[int] = Some(n)\n\nfunc f(x int) int = x match {\n\t/* ü */ case Half(v = a) => a\n\tcase _ => 0\n}\n"},
	{"binding alternative", "package main\n\nfunc f(x Option[int]) int = x match {\n\t/* ü */ case Some(n) | None() => 1\n}\n"},
	{"wildcard alternative", "package main\n\nfunc f(x int) int = x match {\n\t/* ü */ case 1 | _ => 1\n}\n"},
	{"bar mixed with plus", "package main\n\nfunc f(x int) int = x match {\n\t/* ü */ case 1 | 2 + 3 => 1\n\tcase _ => 0\n}\n"},
	{"bar under a comparison", "package main\n\nfunc f(x bool) int = x match {\n\t/* ü */ case 1 | 2 == 3 => 1\n\tcase _ => 0\n}\n"},
	{"rest not last", "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc f(a Array[int]) int = a match {\n\t/* ü */ case Array(rest..., x) => x\n\tcase _ => 0\n}\n"},
	{"rest outside a sequence", "package main\n\nstruct P(a int, b int)\n\nfunc f(p P) int = p match {\n\t/* ü */ case P(x, rest...) => x\n}\n"},
	{"tuple of one too many", "package main\n\nfunc f(p Tuple[int, int]) int = p match {\n\t/* ü */ case (a, b, c, d, e, f, g, h, i, j, k) => a\n\tcase _ => 0\n}\n"},
	{"unused binding", "package main\n\nfunc f(x Option[int]) int = x match {\n\t/* ü */ case Some(n) => 1\n\tcase _ => 0\n}\n"},
	{"bound twice", "package main\n\nfunc f(p Tuple[int, int]) int = p match {\n\t/* ü */ case (a, a) => a\n}\n"},
	{"bare variant with fields", "package main\n\nsealed type S {\n\tcase A(x int)\n\tcase B()\n}\n\nfunc f(s S) int = s match {\n\t/* ü */ case A => 1\n\tcase _ => 0\n}\n"},
	{"no Unapply", "package main\n\nstruct Q()\n\nfunc f(x int) int = x match {\n\t/* ü */ case Nope(v) => v\n\tcase _ => 0\n}\n"},
	{"struct with too many sub-patterns", "package main\n\nstruct P(a int)\n\nfunc f(p P) int = p match {\n\t/* ü */ case P(a, b) => a + b\n}\n"},
	{"opaque arity", "package main\n\nopaque type ID int64\n\nfunc f(i ID) int64 = i match {\n\t/* ü */ case ID(a, b) => a\n}\n"},
	{"opaque typed sub-pattern", "package main\n\nopaque type ID int64\n\nfunc f(i ID) int64 = i match {\n\t/* ü */ case ID(n: int64) => n\n}\n"},
	{"typed pointer pattern", "package main\n\nstruct P(a int)\n\nfunc f(x any) int = x match {\n\t/* ü */ case p: Ptr[P] => 1\n\tcase _ => 0\n}\n"},
	{"interface subject never holds the struct", "package main\n\ntype Named interface {\n\tName() string\n}\n\nstruct P(a int)\n\nfunc f(x Named) int = x match {\n\t/* ü */ case P(a) => a\n\tcase _ => 0\n}\n"},
	{"generic struct without type arguments", "package main\n\nstruct Box[T any](v T)\n\nfunc f(x any) int = x match {\n\t/* ü */ case Box(v) => 1\n\tcase _ => 0\n}\n"},
	{"arm without a value", "package main\n\nfunc f(x int) int = x match {\n\tcase 1 => 1\n\t/* ü */ case _ => {\n\t\tPrintln(\"ü\")\n\t}\n}\n"},
	{"sealed view mixed", "package main\n\nstruct M(raw int)\n\nfunc (m M) MatchView() Option[int] = When(m.raw >= 0, m.raw)\n\nfunc f(m M) int = m match {\n\tcase Some(n) => n\n\t/* ü */ case 3 => 3\n\tcase _ => 0\n}\n"},
	{"bare case with fields on an any subject", "package main\n\nsealed type S {\n\tcase C(r int)\n\tcase D()\n}\n\nfunc f(x any) string = x match {\n\t/* ü */ case C => \"c\"\n\tcase _ => \"other\"\n}\n"},
	{"bare generic case on an any subject", "package main\n\nfunc f(x any) string = x match {\n\t/* ü */ case None => \"none\"\n\tcase _ => \"other\"\n}\n"},
	{"bare case with fields nested in a field of type any", "package main\n\nsealed type S {\n\tcase C(r int)\n\tcase W(inner any)\n}\n\nfunc f(s S) string = s match {\n\t/* ü */ case W(C) => \"wrapped\"\n\tcase _ => \"other\"\n}\n"},
	{"qualified generic case on an any subject", "package main\n\nimport \"martianoff/gala/std\"\n\nfunc f(x any) string = x match {\n\t/* ü */ case std.None => \"none\"\n\tcase _ => \"other\"\n}\n"},
	{"guard that is not a bool", "package main\n\nfunc f(x string) int = x match {\n\t/* ü */ case s if s => 1\n\tcase _ => 0\n}\n"},
	{"type parameter as a pattern","package main\n\nfunc f[T any](x any) int = x match {\n\t/* ü */ case T(v) => 1\n\tcase _ => 0\n}\n"},
}

// caseMarkerLine is the line of src's first `/* ü */` marker, and false when
// it has none.
func caseMarkerLine(src string) (int, bool) {
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, "/* ü */") {
			return i + 1, true
		}
	}
	return 0, false
}

// TestNativeMatchCases holds each of matchCases to its golden (checkWalk):
// its Go, or its error, and the match was read on the
// native tree — every case clause of the file on a clean transpile, and the
// case clause a diagnostic is about on an error.
func TestNativeMatchCases(t *testing.T) {
	galaparser.RequestEngine(string(galaparser.EngineGalop))
	t.Cleanup(func() { galaparser.RequestEngine("") })
	_, searchPaths := walkCorpus(t)
	w := &walkPipeline{searchPaths: searchPaths}
	dir := t.TempDir()
	for _, c := range matchCases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(c.name, " ", "_"), "main.gala")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(c.src), 0o644))
			n, _, ok := checkWalk(t, w, path, c.src, true)
			require.True(t, ok, "the case does not parse cleanly with galop")
			require.Truef(t, n.converted, "the analysis did not convert the native tree, so the walk read ANTLR's; error: %v", n.err)
			require.Positive(t, n.cases, "no case clause was read on the native tree; error: %v", n.err)
			if c.name == patternShapes {
				require.NoError(t, n.err, "the case does not transpile")
				return
			}
			require.Error(t, n.err, "the case should fail")
			line, ok := caseMarkerLine(c.src)
			require.True(t, ok, "the case has no marker")
			assert.Truef(t, n.caseLines[line], "the case clause on line %d was not read on the native tree; error: %v", line, n.err)
			t.Logf("%d case clauses read on the native tree; error: %v", n.cases, n.err)
		})
	}
}
