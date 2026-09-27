package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUntypedConstAdoptsImmutableSlotType covers every site that wraps a value
// in `std.NewImmutable` while knowing the type of the slot it lands in. Go
// infers NewImmutable's type argument from its argument, and an untyped
// constant collapses to its default type (`0` → int, `0.5` → float64), so the
// wrapper must spell the slot's type whenever that default differs — for
// predeclared numeric types, GALA types declared over one, and Go named
// numeric types alike. Otherwise the generated Go fails with "cannot use
// Immutable[int] as Immutable[int64]".
func TestUntypedConstAdoptsImmutableSlotType(t *testing.T) {
	trans := newDefaultsTranspiler()

	const decls = `
type Millis int64
type Ratio float32

struct Nums(i8 int8, i64 int64, u32 uint32, f32 float32, ms Millis, r Ratio)
struct Counter(n int)
struct Box[T any](item T)
`

	cases := []struct {
		name     string
		body     string
		extra    string
		contains []string
		absent   []string
	}{
		{
			name: "Copy override, predeclared numeric fields",
			body: `val base = Nums(0, 0, 0, 0, 0, 0)
    val c = base.Copy(i8 = 1, i64 = 60 * 1000, u32 = 3, f32 = 0.5)
    Println(c.i64)`,
			contains: []string{
				"i8: std.NewImmutable[int8](1)",
				"i64: std.NewImmutable[int64](60 * 1000)",
				"u32: std.NewImmutable[uint32](3)",
				"f32: std.NewImmutable[float32](0.5)",
			},
		},
		{
			name: "Copy override, declared numeric types",
			body: `val base = Nums(0, 0, 0, 0, 0, 0)
    val c = base.Copy(ms = 12, r = 13)
    Println(c.ms)`,
			contains: []string{"ms: std.NewImmutable[Millis](12)", "r: std.NewImmutable[Ratio](13)"},
		},
		{
			name: "Copy override on a generic struct takes the receiver's type argument",
			body: `val b = Box[int64](1)
    Println(b.Copy(item = 2).item)`,
			contains: []string{"item: std.NewImmutable[int64](2)"},
		},
		{
			name: "constructor, declared numeric types, every spelling",
			body: `val a = Nums(1, 2, 3, 4, 12, 13)
    val b = Nums(i8 = 1, i64 = 2, u32 = 3, f32 = 4, ms = 14, r = 0.5)
    val c = Nums{i8: 1, i64: 2, u32: 3, f32: 4, ms: 15, r: 16}
    Println(a.ms + b.ms + c.ms)`,
			contains: []string{
				"std.NewImmutable[Millis](12)",
				"std.NewImmutable[Ratio](13)",
				"std.NewImmutable[Millis](14)",
				"std.NewImmutable[Ratio](0.5)",
				"std.NewImmutable[Millis](15)",
				"std.NewImmutable[Ratio](16)",
			},
		},
		{
			name: "generic struct instantiated with a declared numeric type",
			body: `val b = Box[Millis](17)
    Println(b.item)`,
			contains: []string{"std.NewImmutable[Millis](17)"},
		},
		{
			name:  "field default of a declared numeric type",
			extra: `struct Timeouts(name string, after Millis = 250, ratio Ratio = 0.5)`,
			body: `val d = Timeouts("x")
    Println(d.after)`,
			contains: []string{"std.NewImmutable[Millis](250)", "std.NewImmutable[Ratio](0.5)"},
		},
		{
			name:  "Go named numeric field",
			extra: "import \"time\"\n\nstruct Wait(d time.Duration)",
			body: `val w = Wait(5)
    Println(w.d)`,
			contains: []string{"std.NewImmutable[time.Duration](5)"},
		},
		{
			name: "tuple literal returned as a declared tuple type",
			extra: `func pair() Tuple[int64, float32] = (1, 2)
func triple() Tuple3[int8, Millis, float64] = (1, 2, 3)`,
			body: `Println(pair().V1)
    Println(triple().V2)`,
			contains: []string{
				"std.Tuple[int64, float32]{V1: std.NewImmutable[int64](1), V2: std.NewImmutable[float32](2)}",
				"std.Tuple3[int8, Millis, float64]{V1: std.NewImmutable[int8](1), V2: std.NewImmutable[Millis](2), V3: std.NewImmutable[float64](3)}",
			},
		},
		{
			name:  "tuple literal passed as an argument and stored in a field",
			extra: "struct Span(t Tuple[int64, int64])\n\nfunc take(t Tuple[int64, float32]) int64 = t.V1",
			body: `Println(take((3, 4)))
    val sp = Span((5, 6))
    val named = Span(t = (7, 8))
    Println(sp.t.V1 + named.t.V1)`,
			contains: []string{
				"take(std.Tuple[int64, float32]{V1: std.NewImmutable[int64](3), V2: std.NewImmutable[float32](4)})",
				"std.Tuple[int64, int64]{V1: std.NewImmutable[int64](5), V2: std.NewImmutable[int64](6)}",
				"std.Tuple[int64, int64]{V1: std.NewImmutable[int64](7), V2: std.NewImmutable[int64](8)}",
			},
		},
		{
			// A Go package's untyped constant is untyped in Go exactly as a
			// literal is, and so is constant arithmetic over it.
			name: "Go untyped constants at every site",
			extra: `import (
    "math"
    "time"
)

struct Lim(name string, lo int8 = math.MinInt8)
struct Wait(d time.Duration)
func limits() Tuple[int8, float32] = (math.MinInt8, math.Pi)`,
			body: `val a = Nums(math.MinInt8, math.MaxInt64, math.MaxUint32, math.Pi, math.MaxInt32, math.E)
    val b = a.Copy(i8 = math.MaxInt8 - 1, f32 = math.Pi / 2)
    val l = Lim("x")
    val w = Wait(time.Second)
    Println(b.i8 + l.lo)
    Println(limits().V1)
    Println(w.d)`,
			contains: []string{
				"i8: std.NewImmutable[int8](math.MinInt8)",
				"i64: std.NewImmutable[int64](math.MaxInt64)",
				"u32: std.NewImmutable[uint32](math.MaxUint32)",
				"f32: std.NewImmutable[float32](math.Pi)",
				"ms: std.NewImmutable[Millis](math.MaxInt32)",
				"r: std.NewImmutable[Ratio](math.E)",
				"i8: std.NewImmutable[int8](math.MaxInt8 - 1)",
				"f32: std.NewImmutable[float32](math.Pi / 2)",
				"lo: std.NewImmutable[int8](math.MinInt8)",
				"std.Tuple[int8, float32]{V1: std.NewImmutable[int8](math.MinInt8), V2: std.NewImmutable[float32](math.Pi)}",
				// time.Second is a typed constant: its own type already fits.
				"d: std.NewImmutable(time.Second)",
			},
		},
		{
			// Negative: a field genuinely typed `int`, and a tuple with no
			// expected type, keep the inferred wrapper.
			name: "int slot and untyped tuple keep the inferred form",
			body: `val c = Counter(7)
    val t = (1, 2)
    Println(c.n + t.V1)`,
			contains: []string{"std.NewImmutable(7)", "std.Tuple[int, int]{V1: std.NewImmutable(1), V2: std.NewImmutable(2)}"},
			absent:   []string{"std.NewImmutable[int]("},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\n" + tc.extra + "\n" + decls + "\nfunc main() {\n    " + tc.body + "\n}\n"
			out, err := trans.Transpile(src, "untyped_const_immutable_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
			for _, notWant := range tc.absent {
				assert.NotContains(t, out, notWant)
			}
		})
	}
}

// TestUntypedConstDefaultForDeclaredNumericParam covers the analyzer's literal
// check on parameter defaults: a numeric literal is a valid default for a
// parameter whose type is declared over a numeric type, exactly as Go converts
// it, wherever that type is declared. A literal of the wrong kind is still
// rejected.
func TestUntypedConstDefaultForDeclaredNumericParam(t *testing.T) {
	trans := newDefaultsTranspiler()

	t.Run("declared numeric types accept numeric literals", func(t *testing.T) {
		src := `package main

import "time"

func wait(ms Millis = 12, r Ratio = 0.5, d time.Duration = 5) string = s"$ms $r $d"

type Millis int64
type Ratio float32

func main() {
    Println(wait())
}
`
		out, err := trans.Transpile(src, "untyped_const_default_test.gala")
		require.NoError(t, err)
		body := out[strings.Index(out, "func main()"):]
		assert.Contains(t, body, "wait(12, 0.5, 5)")
	})

	t.Run("string literal for a declared numeric type is rejected", func(t *testing.T) {
		src := `package main

type Millis int64

func wait(ms Millis = "soon") string = s"$ms"

func main() {
    Println(wait())
}
`
		_, err := trans.Transpile(src, "untyped_const_default_test.gala")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `default for parameter "ms" has type string, expected Millis`)
	})
}
