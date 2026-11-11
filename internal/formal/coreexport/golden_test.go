package coreexport_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/coreexport"
)

// goldenDirEnv, when set (bazel test --test_env=COREEXPORT_GOLDEN_DIR=<the
// absolute path of testdata>), makes the golden test write its outputs there
// instead of comparing them.
const goldenDirEnv = "COREEXPORT_GOLDEN_DIR"

func TestGolden(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"literals_operators", `package main

func main() {
    Println(1 + 2 * 3 - 4)
    Println(0x1F % 7 == 3 && !false || true)
    Println(-5 + +2, ^1)
    Print("a\tb" + ` + "`raw\\n`" + `)
}
`},
		{"val_var_assign", `package main

val top = 10

func main() {
    val a = 1
    val b int = 2
    var c = a + b
    var d int
    c = c + 1
    c += 2
    d++
    val (x, y) = (c, d)
    Println(x, y)
}
`},
		{"if_blocks", `package main

func sign(n int) string {
    if n < 0 {
        return "negative"
    } else if n == 0 {
        return "zero"
    }
    return "positive"
}

func main() {
    val s = if (sign(3) == "positive") "yes" else { "no" }
    Println(s)
}
`},
		{"functions_recursion", `package main

func fact(n int) int = if (n <= 1) 1 else n * fact(n - 1)

func fib(n int) int {
    if n < 2 {
        return n
    }
    return fib(n - 1) + fib(n - 2)
}

func main() {
    Println(fact(5), fib(10))
}
`},
		{"structs_sealed", `package main

type Point struct {
    X int
    var Y int
}

struct Pair(Left int, Right string)

sealed type Shape {
    case Circle(Radius int)
    case Rect(W int, H int)
    case Empty
}

func main() {
    val p = Point(1, 2)
    Println(p.X + p.Y, Pair(1, "a").Right)
    Println(Circle(3))
}
`},
		{"match_patterns", `package main

sealed type Expr {
    case Num(Value int)
    case Add(L Expr, R Expr)
}

func eval(e Expr) int = e match {
    case Num(v) => v
    case Add(Num(0), r) => eval(r)
    case Add(l, r) => eval(l) + eval(r)
}

func describe(n int, s string) string = (n, s) match {
    case (0, _) => "zero"
    case (-1, s) => s
    case (n, "x") if n > 10 => "big x"
    case (_, s) => {
        val t = s + "!"
        t
    }
}

func main() {
    Println(eval(Add(Num(1), Add(Num(0), Num(2)))))
    Println(describe(-1, "neg"), describe(11, "x"))
    true match {
        case true => Println("t")
        case _ => Println("f")
    }
}
`},
		{"unsupported", `package main

import "fmt"

func id[T any](x T) T = x

func (p Point) Norm() int = p.X

func types(p *Point, m map[string]int, xs []int, h func(int) int, o Option[int], q fmt.Stringer, n int = 1) int {
    x := *p
    if y := 1; y > 0 {
        Println(&x)
    }
    return 0
}

type Point struct {
    X int
}

func main() {
    val f = (x int) => x + 1
    for i := 0; i < 3; i++ {
        Println(i)
    }
    Println(s"$f", f"%d{1}", 1.5, 'c', nil, ())
    val xs = SliceOf(1, 2)
    Println(xs[0], f(x = 1), fmt.Sprint(Point{X: 1}))
    5 match {
        case n: int => Println(n)
        case Some.Value => Println(0)
        case 1 + 2 => Println(3)
    }
}
`},
	}
	dir := os.Getenv(goldenDirEnv)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := coreexport.Source{Path: tt.name + ".gala", Text: tt.src}
			got, err := exportJSON(src)
			require.NoError(t, err)
			require.NoError(t, checkDocument(got, src))
			file := tt.name + ".json"
			if dir != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, file), got, 0o644))
				return
			}
			want, err := os.ReadFile(filepath.Join("testdata", file))
			require.NoError(t, err, "no golden file; rerun with --test_env=%s=<absolute path of testdata>", goldenDirEnv)
			// git may check the golden files out with CRLF line endings.
			want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
			require.Equal(t, string(want), string(got))
		})
	}
}

// TestHintsAreOptional checks that without a LiteralType the export carries
// no hints, and that hints sit only on int literals.
func TestHintsAreOptional(t *testing.T) {
	src := coreexport.Source{Path: "a.gala", Text: "package main\n\nval x = 1\n"}
	p, err := coreexport.Export([]coreexport.Source{src}, coreexport.Options{})
	require.NoError(t, err)
	plain, err := coreexport.Marshal(p)
	require.NoError(t, err)
	require.NotContains(t, string(plain), `"hint"`)

	withHints, err := exportJSON(src)
	require.NoError(t, err)
	require.Contains(t, string(withHints), `"hint": {`)
	require.Contains(t, string(withHints), `"type": "int"`)
}

// TestSourceTextEdges checks the cases where the source text and the
// program's meaning part: a byte order mark, carriage returns in a raw
// string, and an integer literal Go reads as octal.
func TestSourceTextEdges(t *testing.T) {
	src := coreexport.Source{Path: "edges.gala",
		Text: string(rune(0xFEFF)) + "package main\r\n\r\nval s = `a\r\nb`\r\nval o = 0755\r\nval z = 0\r\n"}
	p, err := coreexport.Export([]coreexport.Source{src}, hinted)
	require.NoError(t, err)
	data, err := coreexport.Marshal(p)
	require.NoError(t, err)
	require.NoError(t, checkDocument(data, src))

	decls := p.Files[0].Decls
	require.Equal(t, "a\nb", decls[0].(*coreexport.Val).Values[0].(*coreexport.String).Value)
	require.Equal(t, &coreexport.Unsupported{What: coreexport.UnsupportedLeadingZero,
		Pos: coreexport.Pos{Line: 5, Col: 9, Off: 43, End: 47}}, decls[1].(*coreexport.Val).Values[0])
	require.Equal(t, "0", decls[2].(*coreexport.Val).Values[0].(*coreexport.Int).Text)
}

// TestSyntaxErrorIsAnError checks that a file that does not parse is an
// error, not a document.
func TestSyntaxErrorIsAnError(t *testing.T) {
	_, err := coreexport.Export([]coreexport.Source{{Path: "bad.gala", Text: "package main\n\nfunc (\n"}}, coreexport.Options{})
	require.Error(t, err)
}

// TestMultipleFiles checks that a document holds each file, in order.
func TestMultipleFiles(t *testing.T) {
	a := coreexport.Source{Path: "a.gala", Text: "package main\n\nval a = 1\n"}
	b := coreexport.Source{Path: "b.gala", Text: "package main\n\nfunc main() {\n    Println(a)\n}\n"}
	p, err := coreexport.Export([]coreexport.Source{a, b}, hinted)
	require.NoError(t, err)
	data, err := coreexport.Marshal(p)
	require.NoError(t, err)
	require.NoError(t, checkDocument(data, a, b))
	require.Len(t, p.Files, 2)
	require.Equal(t, "a.gala", p.Files[0].Path)
	require.Equal(t, "b.gala", p.Files[1].Path)
}
