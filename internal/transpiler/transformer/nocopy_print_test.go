package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoCopyPrintRejected pins GALA-E0053 for printing: Println, Print and
// interpolation call a String declared on a pointer receiver only on a copy
// of a value that cannot be addressed (std.PtrString), as a call of that
// String would. For a value that must not be copied the copy takes its lock
// with it, so the print is rejected like the call. A generated String prints
// its fields through std.FormatStruct, which calls such a field's String on a
// copy of the field however the value itself is held, so printing a value
// with such a field is rejected too. A value that is fine to copy, or one
// printed in place, is untouched.
func TestNoCopyPrintRejected(t *testing.T) {
	trans := newDefaultsTranspiler()

	const decls = `
struct Guarded(var mu sync.Mutex, var n int)

func (g *Guarded) String() string = s"guarded ${g.n}"

struct Waiter(var wg sync.WaitGroup)

func (w *Waiter) String() string = "waiter"

struct Ticks(var n atomic.Int64)

func (c *Ticks) String() string = "ticks"

type noCopy struct {
    var unused bool
}

struct Marked(var nc noCopy)

func (m *Marked) String() string = "marked"

struct Plain(var n int)

func (p *Plain) String() string = s"plain ${p.n}"

struct Holder(g Guarded)

struct Outer(var g Guarded, var k int)

struct Deep(var o Outer)

struct Box[T any](var v T)

sealed type Event {
    case Tick(g Guarded)
    case Idle()
}

struct ByPointer(g *Guarded)

struct OwnString(var g Guarded)

func (o OwnString) String() string = "own"

struct PlainOuter(var p Plain)

struct ViaPointer(o *Outer)

struct GBox[T any](var mu sync.Mutex, var v T)

func (b *GBox[T]) String() string = "gbox"

struct GOuter(var b GBox[int])

func mk() Guarded = Guarded(sync.Mutex{}, 3)
`

	rejected := []struct {
		name, body, want string
	}{
		{"a val by Println", "val g = Guarded(sync.Mutex{}, 1)\n    Println(g)", "cannot print this Guarded: it cannot be addressed here, so its String would run on a copy, and Guarded contains sync.Mutex, which must not be copied"},
		{"a val by Print", "val g = Guarded(sync.Mutex{}, 1)\n    Print(g)", "cannot print this Guarded"},
		{"a call result", "Println(mk())", "cannot print this Guarded"},
		{"a val field of a var", "var h = Holder(mk())\n    Println(h.g)", "cannot print this Guarded"},
		{"an interpolated call result", "Println(s\"value ${mk()}\")", "cannot print this Guarded"},
		{"a formatted val", "val g = mk()\n    Println(f\"$g%s\")", "cannot print this Guarded"},
		{"a WaitGroup holder", "val w = Waiter(sync.WaitGroup{})\n    Println(w)", "Waiter contains sync.WaitGroup"},
		{"an atomic holder", "val c = Ticks(atomic.Int64{})\n    Println(c)", "Ticks contains atomic.Int64"},
		{"a noCopy marker holder", "val m = Marked(noCopy{})\n    Println(m)", "Marked contains noCopy"},
		{"a field of a var printed by FormatStruct", "var o = Outer(mk(), 1)\n    Println(o)", "cannot print this Outer: its field g, a Guarded, prints by its String on a copy, and Guarded contains sync.Mutex"},
		{"a nested field printed by FormatStruct", "var d = Deep(Outer(mk(), 1))\n    Println(d)", "its field o.g, a Guarded"},
		{"a field of an interpolated value", "var o = Outer(mk(), 1)\n    Println(s\"$o\")", "cannot print this Outer"},
		{"a type argument printed by FormatStruct", "var b = Box[Guarded](mk())\n    Println(b)", "cannot print this Box: its field v, a Guarded"},
		{"a sealed case's field", "val e = Tick(mk())\n    Println(e)", "cannot print this Event: its field g, a Guarded"},
		{"a pointer to a value with such a field", "var o = Outer(mk(), 1)\n    Println(&o)", "cannot print this Outer: its field g, a Guarded"},
		{"a pointer field to a value with such a field", "var o = Outer(mk(), 1)\n    val w = ViaPointer(&o)\n    Println(w)", "cannot print this ViaPointer: its field o.g, a Guarded"},
		{"a field printed with %q", "var o = Outer(mk(), 1)\n    Println(f\"$o%q\")", "cannot print this Outer"},
		{"a generic field, named with its type arguments", "var o = GOuter(GBox[int](sync.Mutex{}, 1))\n    Println(o)", "hold the field as a pointer (`*GBox[int]`)"},
	}
	for _, tc := range rejected {
		t.Run("rejected/"+tc.name, func(t *testing.T) {
			_, err := trans.Transpile(noCopySource(decls, tc.body), "nocopy_print_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0053")
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	accepted := []struct {
		name, body, mustContain string
	}{
		{"a var is printed in place", "var g = mk()\n    Println(g)", "std.StringAt(&g)"},
		{"a copyable val is printed through a copy", "val p = Plain(1)\n    Println(p)", "std.PtrString(p)"},
		{"a field held by pointer", "val b = ByPointer(&Guarded(sync.Mutex{}, 1))\n    Println(b)", "fmt.Println(b)"},
		{"a field of a type with its own String", "var o = OwnString(mk())\n    Println(o)", "fmt.Println(o)"},
		{"a copyable field", "val o = PlainOuter(Plain(1))\n    Println(o)", "fmt.Println(o)"},
	}
	for _, tc := range accepted {
		t.Run("accepted/"+tc.name, func(t *testing.T) {
			out, err := trans.Transpile(noCopySource(decls, tc.body), "nocopy_print_test.gala")
			require.NoError(t, err)
			assert.Contains(t, out, tc.mustContain)
		})
	}
}

// TestNoCopyPrintRejectedAtValue checks that GALA-E0053 for a print points at
// the printed value: an argument of Println or Print, or an interpolated
// expression.
func TestNoCopyPrintRejectedAtValue(t *testing.T) {
	const decls = `
struct Guarded(var mu sync.Mutex, var n int)

func (g *Guarded) String() string = s"guarded ${g.n}"

func mk() Guarded = Guarded(sync.Mutex{}, 3)
`
	for _, body := range []string{"Println(1, mk())", "Print(\"x\", mk())", "Println(s\"at ${mk()}\")"} {
		t.Run(body, func(t *testing.T) {
			src := noCopySource(decls, body)
			_, err := newDefaultsTranspiler().Transpile(src, "nocopy_print_test.gala")
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr)
			assert.Equal(t, galaerr.CodeNoCopyReceiverCopied, semErr.Code)
			line := strings.Split(src, "\n")[semErr.Line-1]
			assert.True(t, strings.HasPrefix(line[semErr.Column:], "mk()"), "error at %q", line[semErr.Column:])
		})
	}
}
