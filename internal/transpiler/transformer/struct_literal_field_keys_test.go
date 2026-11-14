package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A struct literal's keys are field names. They must not resolve as values:
// `Pair{Left: 1}` used to come out as `Pair{std.Left: 1}`, because the key was
// lowered like any expression and a bare `Left` is std's Either constructor.
func TestStructLiteralKeyNamedLikeStdSymbol(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "Either and Option constructors",
			src: `struct Pair(Left int, Right int)
struct Opt(Some int, None int)
func main() {
    val p = Pair{Left: 1, Right: 2}
    val o = Opt{Some: 3, None: 4}
    Println(p.Left + o.Some)
}`,
			want: []string{"Left: std.NewImmutable(1)", "Right: std.NewImmutable(2)", "Some: std.NewImmutable(3)", "None: std.NewImmutable(4)"},
		},
		{
			name: "Try constructors in a block-form struct",
			src: `type Res struct {
    Success string
    Failure string
}
func main() {
    val r = Res{Success: "a", Failure: "b"}
    Println(r.Success)
}`,
			want: []string{`Success: std.NewImmutable("a")`, `Failure: std.NewImmutable("b")`},
		},
		{
			name: "std types and functions",
			src: `struct Misc(Option int, Tuple int, FromOption int)
func main() {
    val m = Misc{Option: 1, Tuple: 2, FromOption: 3}
    Println(m.Option)
}`,
			want: []string{"Option: std.NewImmutable(1)", "Tuple: std.NewImmutable(2)", "FromOption: std.NewImmutable(3)"},
		},
		{
			name: "field with no default value is recognised as given",
			src: `struct Holder(Left Either[int, string])
func main() {
    val h = Holder{Left: Left[int, string](7)}
    Println(h.Left)
}`,
			want: []string{"Left: std.NewImmutable("},
		},
		{
			name: "Go struct literal",
			src: `import "image"

func main() {
    val pt = image.Point{X: 1, Y: 2}
    Println(pt.X)
}`,
			want: []string{"image.Point{X: 1, Y: 2}"},
		},
		{
			// os.PathError is an alias of fs.PathError. The package-level vals
			// named like its fields must not be read as the keys.
			name: "Go alias of a struct",
			src: `import "os"

val Op = "read"
val Path = "/tmp/x"

func main() {
    val e = os.PathError{Op: Op, Path: Path}
    Println(e.Op)
}`,
			want: []string{"os.PathError{Op: Op.Get(), Path: Path.Get()}"},
		},
		{
			// A map key is a value: a package-level val is read through Get.
			name: "named Go map keeps expression keys",
			src: `import "text/template"

val name = "up"

func upper(s string) string = s

func main() {
    val fm = template.FuncMap{name: upper}
    Println(fm.Size())
}`,
			want: []string{"template.FuncMap{name.Get(): upper}"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := trans.Transpile("package main\n\n"+tc.src+"\n", "")
			require.NoError(t, err)
			for _, w := range tc.want {
				assert.Contains(t, got, w)
			}
			for _, bad := range []string{"std.Left:", "std.Right:", "std.Some:", "std.None:", "std.Success:", "std.Failure:", "std.Option:", "std.Tuple:", "std.FromOption:"} {
				assert.NotContains(t, got, bad, "a struct literal key must be the bare field name")
			}
		})
	}
}
