package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// receiverMutationDecls declares pointer-receiver methods that change their
// receiver in each way GALA-E0110 knows, and ones that only read it.
const receiverMutationDecls = `
struct Counter(var n int)

func (c Ptr[Counter]) Bump() int {
    c.n = c.n + 1
    c.n
}
func (c Ptr[Counter]) Inc() { c.n++ }
func (c Ptr[Counter]) Reset() { *c = Counter(0) }
func (c Ptr[Counter]) BumpTwice() int {
    c.Bump()
    c.Bump()
}
func (c Ptr[Counter]) Self() Ptr[Counter] = c
func (c Ptr[Counter]) Later() func() int = () => c.Bump()
func (c Ptr[Counter]) Bumper() func() int = c.Bump
func (c Ptr[Counter]) Shown() string = s"${c.Bump()}"
func (c Ptr[Counter]) Zero() { (*c).n = 0 }

func (c Ptr[Counter]) Get() int = c.n
func (c Ptr[Counter]) Label() string = s"n=${c.n} get=${c.Get()}"
func (c Ptr[Counter]) Same(o Ptr[Counter]) bool = c == o
func (c Ptr[Counter]) Depth(k int) int = if (k == 0) c.n else c.Depth(k - 1)
func (c Ptr[Counter]) Snapshot() Counter = *c

func mk() Counter = Counter(5)

struct Holder(var c Counter)

struct Outer(var inner Counter)

func (o Ptr[Outer]) Grow() int = o.inner.Bump()
func (o Ptr[Outer]) Peek() int = o.inner.Get()

struct Node(var next Ptr[Counter])

func (nd Ptr[Node]) BumpNext() int = nd.next.Bump()
func (nd Ptr[Node]) Clear() { nd.next.n = 0 }

struct Box[T any](var v T, var hits int)

func (b *Box[T]) Hit[U any](u U) U {
    b.hits = b.hits + 1
    u
}
func (b *Box[T]) Look[U any](f func(T) U) U = f(b.v)

struct Loud(var n int)

func (l *Loud) String() string {
    l.n = l.n + 1
    s"loud ${l.n}"
}

struct Quiet(var n int)

func (q *Quiet) String() string = s"quiet ${q.n}"
`

// receiverMutationSource is a program with receiverMutationDecls and body
// as main's body. It imports the packages body refers to, and no other: an
// import nothing refers to is GALA-E0120.
func receiverMutationSource(body string) string {
	var imports []string
	for _, path := range []string{"net/url", "strings", "time", "martianoff/gala/go_interop"} {
		if strings.Contains(body, path[strings.LastIndex(path, "/")+1:]+".") {
			imports = append(imports, "    \""+path+"\"\n")
		}
	}
	header := ""
	if len(imports) > 0 {
		header = "\nimport (\n" + strings.Join(imports, "") + ")\n"
	}
	return `package main
` + header + receiverMutationDecls + `
func main() {
    ` + body + `
}
`
}

// TestMutatingCallOnCopyRejected pins GALA-E0110: a pointer-receiver method
// that can change its receiver, called on a value Go cannot address (a val, a
// field reached through one, a call result), would change a copy, so it is
// rejected. A method that only reads its receiver still runs on the copy, and
// an addressable receiver is called in place.
func TestMutatingCallOnCopyRejected(t *testing.T) {
	trans := newDefaultsTranspiler()

	rejected := []struct {
		name, body, method string
	}{
		{"assignment to a field", "val c = Counter(1)\n    Println(c.Bump())", "Bump"},
		{"increment of a field", "val c = Counter(1)\n    c.Inc()", "Inc"},
		{"assignment through the receiver", "val c = Counter(1)\n    c.Reset()", "Reset"},
		{"call result", "Println(mk().Bump())", "Bump"},
		{"field reached through a val", "val h = Holder(Counter(1))\n    Println(h.c.Bump())", "Bump"},
		{"a mutating method it calls", "val c = Counter(1)\n    Println(c.BumpTwice())", "BumpTwice"},
		{"a mutating method it calls on a field", "val o = Outer(Counter(1))\n    Println(o.Grow())", "Grow"},
		{"the receiver escapes", "val c = Counter(1)\n    Println(c.Self().n)", "Self"},
		{"a lambda captures a mutating call", "val c = Counter(1)\n    Println(c.Later()())", "Later"},
		{"a method value of a mutating method", "val c = Counter(1)\n    Println(c.Bumper()())", "Bumper"},
		{"a mutating call in an interpolation", "val c = Counter(1)\n    Println(c.Shown())", "Shown"},
		{"assignment through a parenthesized dereference", "val c = Counter(1)\n    c.Zero()", "Zero"},
		{"generic method lowered to a function", "val b = Box[int](1, 0)\n    Println(b.Hit(2))", "Hit"},
		{"Go method that writes its receiver", "val tm = time.Now()\n    Println(tm.UnmarshalText(go_interop.ToBytes(\"x\")))", "UnmarshalText"},
	}
	for _, tc := range rejected {
		t.Run("rejected/"+tc.name, func(t *testing.T) {
			_, err := trans.Transpile(receiverMutationSource(tc.body), "receiver_mutation_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0110")
			assert.Contains(t, err.Error(), "cannot call "+tc.method+" on this ")
			assert.Contains(t, err.Error(), "declare it with `var`, or hold a `Ptr`")
		})
	}

	t.Run("rejected/printing a val whose String changes it", func(t *testing.T) {
		_, err := trans.Transpile(receiverMutationSource("val l = Loud(1)\n    Println(l)"), "receiver_mutation_test.gala")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GALA-E0110")
		assert.Contains(t, err.Error(), "cannot print this Loud: its String can change its receiver")
	})

	accepted := []struct {
		name, body, mustContain string
	}{
		{"reads a field", "val c = Counter(1)\n    Println(c.Get())", "std.AddrOfCopy(c).Get()"},
		{"reads in an interpolation and calls a reader", "val c = Counter(1)\n    Println(c.Label())", "std.AddrOfCopy(c).Label()"},
		{"compares the receiver", "val c = Counter(1)\n    var d = Counter(2)\n    Println(c.Same(&d))", "std.AddrOfCopy(c).Same("},
		{"recursive reader", "val c = Counter(1)\n    Println(c.Depth(3))", "std.AddrOfCopy(c).Depth(3)"},
		{"dereferences the receiver", "val c = Counter(1)\n    Println(c.Snapshot().n)", "std.AddrOfCopy(c).Snapshot()"},
		{"reader on a call result", "Println(mk().Get())", "std.AddrOfCopy(mk()).Get()"},
		{"calls a reader on a field", "val o = Outer(Counter(1))\n    Println(o.Peek())", "std.AddrOfCopy(o).Peek()"},
		{"writes through a pointer field", "var t = Counter(1)\n    val nd = Node(&t)\n    Println(nd.BumpNext())\n    nd.Clear()", "std.AddrOfCopy(nd).Clear()"},
		{"generic reader lowered to a function", "val b = Box[int](1, 0)\n    Println(b.Look((v) => v + 1))", "Box_Look(std.AddrOfCopy(b), "},
		{"Go reader url.URL.String", "val u = url.URL(Scheme = \"https\", Host = \"x\")\n    Println(u.String())", "std.AddrOfCopy(u).String()"},
		{"Go reader strings.Reader.Len", "val r = *strings.NewReader(\"abc\")\n    Println(r.Len())", "std.AddrOfCopy(r).Len()"},
		{"printing a val whose String only reads it", "val q = Quiet(1)\n    Println(q)", "std.PtrString(q)"},
		{"mutating method on a var", "var c = Counter(1)\n    Println(c.Bump())", "c.Bump()"},
		{"mutating method on a Ptr val", "val c = &Counter(1)\n    Println(c.Bump())", "c.Bump()"},
		{"mutating method on a var field of a var", "var h = Holder(Counter(1))\n    Println(h.c.Bump())", "h.c.Bump()"},
		{"printing a var whose String changes it", "var l = Loud(1)\n    Println(l)", "std.StringAt(&l)"},
	}
	for _, tc := range accepted {
		t.Run("accepted/"+tc.name, func(t *testing.T) {
			out, err := trans.Transpile(receiverMutationSource(tc.body), "receiver_mutation_test.gala")
			require.NoError(t, err)
			assert.Contains(t, out, tc.mustContain)
		})
	}
}

// TestMutatingCallOnCopyParameter checks that a parameter and a pattern
// binding stay addressable: a mutating method is called on them in place.
func TestMutatingCallOnCopyParameter(t *testing.T) {
	trans := newDefaultsTranspiler()
	src := `package main
` + receiverMutationDecls + `
func bumpParam(c Counter) int = c.Bump()

func bumpBound(o Option[Counter]) int = o match {
    case Some(c) => c.Bump()
    case _ => 0
}

func main() {
    Println(bumpParam(Counter(1)))
    Println(bumpBound(Some(Counter(1))))
}
`
	out, err := trans.Transpile(src, "receiver_mutation_param_test.gala")
	require.NoError(t, err)
	assert.NotContains(t, out, "std.AddrOfCopy(c).Bump()")
}

// goSiblingLink is a hand-written .go file of the package: a type embedding
// url.URL, with pointer methods that write it directly, write it through a
// promoted method, and only read it through one.
const goSiblingLink = `
import "net/url"

type Link struct{ url.URL }

func (l *Link) Rename(s string)          { l.Host = s }
func (l *Link) Load(b []byte) error      { return l.UnmarshalBinary(b) }
func (l *Link) Text() string             { return l.String() }

func NewLink() Link { return Link{} }
`

// TestMutatingCallOnCopyGoSibling covers GALA-E0110 for the pointer methods
// a package's own .go file declares, whose effects the analyzer reads from
// that file, following a promoted method into the Go SDK's source.
func TestMutatingCallOnCopyGoSibling(t *testing.T) {
	for _, tc := range []struct{ call, err string }{
		{"l.Rename(\"x\")", "cannot call Rename on this"},
		{"Println(l.Load(go_interop.ToBytes(\"x\")))", "cannot call Load on this"},
		{"Println(l.Text())", ""},
	} {
		t.Run(tc.call, func(t *testing.T) {
			files, galaFile := samePackageModule(".", "package main\n"+goSiblingLink,
				"package main\n\nimport \"martianoff/gala/go_interop\"\n\nfunc main() {\n    val l = NewLink()\n    "+tc.call+"\n    Println(go_interop.ToBytes(\"\").Size())\n}\n")
			out, err := transpileInModule(t, files, galaFile)
			if tc.err == "" {
				require.NoError(t, err)
				assert.Contains(t, out, "std.AddrOfCopy(l).Text()")
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0110")
			assert.Contains(t, err.Error(), tc.err)
		})
	}
}

// printedFieldDecls declares types whose generated String prints a field by
// a pointer-receiver String (Loud's changes its receiver, Quiet's only reads
// it). std.FormatStruct prints such a field through a copy of it.
const printedFieldDecls = `
struct LoudHolder(var l Loud, var k int)

struct LoudDeep(var h LoudHolder)

struct LoudBox[T any](var v T)

struct LoudByPtr(l Ptr[Loud])

struct DeepByPtr(h Ptr[LoudHolder])

struct QuietHolder(var q Quiet)

struct LoudOwn(var l Loud)

func (o LoudOwn) String() string = "own"

sealed type Shout {
    case Says(l Loud)
    case Hush()
}

struct Fault(var n int)

func (f *Fault) String() string {
    f.n = f.n + 1
    "fault"
}

func (f Fault) Error() string = "faulted"

struct FaultHolder(var f Fault)

struct LoudError(var l Loud)

func (e LoudError) Error() string = "loud error"

struct LoudErrorHolder(var e LoudError)

struct Stamp(var n int)

func (s Ptr[Stamp]) String() string {
    s.n = s.n + 1
    "stamp"
}

func (s Stamp) Format(layout string) string = layout

struct StampHolder(var at Stamp)

struct Odd(var n int)

func (o Ptr[Odd]) String() string {
    o.n = o.n + 1
    "odd"
}

func (o Odd) Format(a int, b int) {
    Println(a + b)
}

struct OddHolder(var o Odd)

struct PtrError(var l Loud)

func (e Ptr[PtrError]) Error() string = "ptr error"
`

// TestMutatingFieldPrintRejected pins GALA-E0110 for a field printed through
// a copy: a struct's or sealed case's generated String prints each field by
// std.FormatStruct, which runs a field's pointer-receiver String on a copy of
// the field however the value itself is held. When that String can change its
// receiver the change is lost, so printing the value is rejected at the
// print. A field held by pointer, a String that only reads its receiver, and
// a type with a String of its own print as before.
func TestMutatingFieldPrintRejected(t *testing.T) {
	trans := newDefaultsTranspiler()
	source := func(body string) string {
		return `package main
` + receiverMutationDecls + printedFieldDecls + `
func main() {
    ` + body + `
}
`
	}

	rejected := []struct{ name, body, want string }{
		{"a var by Println", "var h = LoudHolder(Loud(0), 1)\n    Println(h)", "cannot print this LoudHolder: its field l, a Loud, prints by its String on a copy, and that String can change its receiver"},
		{"a val by Print", "val h = LoudHolder(Loud(0), 1)\n    Print(h)", "cannot print this LoudHolder: its field l"},
		{"an interpolation", "var h = LoudHolder(Loud(0), 1)\n    Println(s\"h=$h\")", "cannot print this LoudHolder"},
		{"a formatted value with %q", "var h = LoudHolder(Loud(0), 1)\n    Println(f\"$h%q\")", "cannot print this LoudHolder"},
		{"a nested field", "var d = LoudDeep(LoudHolder(Loud(0), 1))\n    Println(d)", "cannot print this LoudDeep: its field h.l, a Loud"},
		{"a type argument", "var b = LoudBox[Loud](Loud(0))\n    Println(b)", "cannot print this LoudBox: its field v, a Loud"},
		{"a sealed case's field", "val s = Says(Loud(0))\n    Println(s)", "cannot print this Shout: its field l, a Loud"},
		{"a pointer to the value", "var h = LoudHolder(Loud(0), 1)\n    Println(&h)", "cannot print this LoudHolder: its field l"},
		{"a pointer field to a value with such a field", "var h = LoudHolder(Loud(0), 1)\n    val d = DeepByPtr(&h)\n    Println(d)", "cannot print this DeepByPtr: its field h.l, a Loud"},
		{"a Format that is not fmt.Formatter's", "var h = StampHolder(Stamp(0))\n    Println(h)", "cannot print this StampHolder: its field at, a Stamp"},
		{"a two-parameter Format that is not fmt.Formatter's", "var h = OddHolder(Odd(0))\n    Println(h)", "cannot print this OddHolder: its field o, a Odd"},
	}
	for _, tc := range rejected {
		t.Run("rejected/"+tc.name, func(t *testing.T) {
			_, err := trans.Transpile(source(tc.body), "printed_field_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0110")
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), "as a `Ptr[")
		})
	}

	t.Run("rejected/the hint names the nested field", func(t *testing.T) {
		_, err := trans.Transpile(source("var d = LoudDeep(LoudHolder(Loud(0), 1))\n    Println(d)"), "printed_field_test.gala")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "hold field h.l as a `Ptr[Loud]`")
	})

	accepted := []struct{ name, body, mustContain string }{
		{"a field held by pointer", "var l = Loud(0)\n    val b = LoudByPtr(&l)\n    Println(b)", "fmt.Println(b)"},
		{"a field whose String only reads it", "val q = QuietHolder(Quiet(1))\n    Println(q)", "fmt.Println(q)"},
		{"a type with its own String", "val o = LoudOwn(Loud(0))\n    Println(o)", "fmt.Println(o)"},
		{"a field printed by its own String", "var h = LoudHolder(Loud(0), 1)\n    Println(h.l)", "std.StringAt(&h.l)"},
		{"a field printed by its value's Error", "var h = FaultHolder(Fault(0))\n    Println(h)", "fmt.Println(h)"},
		{"a value printed by its own Error", "var e = LoudError(Loud(0))\n    Println(e)", "fmt.Println(e)"},
		{"a nested field printed by its own Error", "var h = LoudErrorHolder(LoudError(Loud(0)))\n    Println(h)", "fmt.Println(h)"},
		{"a pointer printed by its pointer-receiver Error", "var e = PtrError(Loud(0))\n    Println(&e)", "fmt.Println(&e)"},
	}
	for _, tc := range accepted {
		t.Run("accepted/"+tc.name, func(t *testing.T) {
			out, err := trans.Transpile(source(tc.body), "printed_field_test.gala")
			require.NoError(t, err)
			assert.Contains(t, out, tc.mustContain)
		})
	}
}

// goSiblingTally is a hand-written .go file of the package: a type whose
// pointer-receiver String counts how often it ran, and one whose String only
// reads its receiver.
const goSiblingTally = `
type Tally struct{ n int }

func (t *Tally) String() string { t.n++; return "tally" }

type Mark struct{ n int }

func (m *Mark) String() string { return "mark" }
`

// TestMutatingFieldPrintGoString covers GALA-E0110 for a field of a Go type
// whose String is declared on its pointer receiver: std.FormatStruct runs it
// on a copy of the field as it does a GALA one. Whether it changes its
// receiver is read from the Go source.
func TestMutatingFieldPrintGoString(t *testing.T) {
	for _, tc := range []struct{ field, err string }{
		{"Tally", "cannot print this Wrap: its field t, a Tally, prints by its String on a copy"},
		{"Mark", ""},
	} {
		t.Run(tc.field, func(t *testing.T) {
			files, galaFile := samePackageModule(".", "package main\n"+goSiblingTally,
				"package main\n\nstruct Wrap(var t "+tc.field+")\n\nfunc main() {\n    var w = Wrap("+tc.field+"{})\n    Println(w)\n}\n")
			_, err := transpileInModule(t, files, galaFile)
			if tc.err == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0110")
			assert.Contains(t, err.Error(), tc.err)
		})
	}
}
