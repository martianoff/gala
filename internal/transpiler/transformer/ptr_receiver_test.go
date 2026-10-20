package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ptrReceiverDecls declares a type with methods on a non-null Ptr receiver,
// a generic one, and a function returning a pointer that may be nil.
const ptrReceiverDecls = `struct Counter(var N int)

func (c Ptr[Counter]) Bump() {
    c.N = c.N + 1
}

func (c Ptr[Counter]) Count() int = c.N

struct Box[T any](var V T)

func (b Ptr[Box[T]]) Get() T = b.V

func (b Ptr[Box[T]]) Map[U any](f func(T) U) U = f(b.V)

func lookup(found bool) *Counter = if (found) &Counter(1) else nil

func findBox() *Box[int] = nil
`

// TestPtrReceiverRejected checks that a method declared on a `Ptr[T]`
// receiver is never selected on a pointer that may be nil (GALA-E0080): not
// called, not taken as a method value, not reached through an interface the
// pointer is converted to, for plain and generic receivers.
func TestPtrReceiverRejected(t *testing.T) {
	trans := newTranspiler()
	tests := []struct {
		name  string
		decls string
		body  string
		msg   string
		hint  string
	}{
		{
			name: "call on a nullable call result",
			body: "lookup(false).Bump()",
			msg:  "cannot call Bump on lookup(false) (*Counter): Bump has a Ptr[Counter] receiver, and a pointer that may be nil cannot fill a Ptr",
			hint: "call it on RequirePtr(lookup(false))",
		},
		{
			name: "call on a nullable var",
			body: "var c *Counter = lookup(true)\n    c.Bump()",
			msg:  "cannot call Bump on c (*Counter)",
		},
		{
			name: "call on a nullable val",
			body: "val c = lookup(true)\n    Println(c.Count())",
			msg:  "cannot call Count on c (*Counter)",
		},
		{
			name:  "call on a nullable field",
			decls: "struct Holder(c *Counter)\n",
			body:  "val h = Holder(lookup(true))\n    h.c.Bump()",
			msg:   "cannot call Bump on h.c (*Counter)",
		},
		{
			name: "method value",
			body: "val f = lookup(true).Count\n    Println(f())",
			msg:  "cannot call Count on lookup(true) (*Counter)",
		},
		{
			name: "generic receiver",
			body: "Println(findBox().Get())",
			msg:  "cannot call Get on findBox() (*Box[int]): Get has a Ptr[Box[int]] receiver",
		},
		{
			name: "generic method lowered to a function",
			body: "Println(findBox().Map((v) => v + 1))",
			msg:  "cannot call Map on findBox() (*Box[int])",
		},
		{
			name:  "nullable receiver of a *T method",
			decls: "func (c *Counter) bumpTwice() {\n    c.Bump()\n    c.Bump()\n}\n",
			body:  "lookup(true).bumpTwice()",
			msg:   "cannot call Bump on c (*Counter)",
			hint:  "declare the receiver `c Ptr[Counter]`",
		},
		{
			name:  "nullable receiver of a *T method passed as a Ptr",
			decls: "func show(c Ptr[Counter]) = Println(c.N)\n\nfunc (c *Counter) show() = show(c)\n",
			body:  "lookup(true).show()",
			msg:   "cannot use c (*Counter) as Ptr[Counter]",
		},
		{
			name:  "alias of a nullable *T receiver",
			decls: "func (c *Counter) viaAlias() {\n    val q = c\n    q.Bump()\n}\n",
			body:  "lookup(true).viaAlias()",
			msg:   "cannot call Bump on q (*Counter)",
		},
		{
			name:  "match through a MatchView on a Ptr receiver",
			decls: "func (c Ptr[Counter]) MatchView() Option[int] = Some(c.N)\n",
			body:  "lookup(true) match {\n        case Some(n) => Println(n)\n        case None() => Println(\"none\")\n    }",
			msg:   "cannot match a *Counter through MatchView: MatchView has a Ptr[Counter] receiver",
		},
		{
			name:  "Ptr receiver compared with nil",
			decls: "func (c Ptr[Counter]) isNil() bool = c == nil\n",
			body:  "Println(lookup(true).isNil())",
			msg:   "compares a Ptr[Counter] with nil",
		},
		{
			name:  "nullable pointer into an interface slot",
			decls: "type Bumper interface {\n    Bump()\n}\n",
			body:  "val b Bumper = lookup(true)\n    b.Bump()",
			msg:   "cannot use lookup(true) (*Counter) as Bumper: its method Bump has a Ptr[Counter] receiver",
			hint:  "pass RequirePtr(lookup(true))",
		},
		{
			name:  "nullable pointer as an interface argument",
			decls: "type Counted interface {\n    Count() int\n}\n\nfunc report(c Counted) = Println(c.Count())\n",
			body:  "report(lookup(true))",
			msg:   "cannot use lookup(true) (*Counter) as Counted",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "package main\n\n" + ptrReceiverDecls + "\n" + tt.decls + "\nfunc main() {\n    " + tt.body + "\n}\n"
			_, err := trans.Transpile(src, "")
			require.Error(t, err)
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr)
			assert.Equal(t, galaerr.CodePtrNullability, semErr.Code, "%v", err)
			assert.Contains(t, semErr.Msg, tt.msg)
			assert.Contains(t, semErr.Hint, tt.hint)
		})
	}
}

// TestPtrReceiverAllowed checks the receivers that are never nil: an
// addressable var (called through `&c`), a val (through an addressed copy),
// `&x`, a Ptr, RequirePtr, a Ptr receiver calling its own methods, and a
// Ptr filling an interface; and that a `*T` method stays callable on a
// nullable pointer.
func TestPtrReceiverAllowed(t *testing.T) {
	trans := newTranspiler()
	tests := []struct {
		name  string
		decls string
		body  string
	}{
		{name: "addressable var", body: "var c = Counter(0)\n    c.Bump()\n    Println(c.Count())"},
		{name: "val", body: "val c = Counter(0)\n    Println(c.Count())"},
		{name: "address of a var", body: "var c = Counter(0)\n    (&c).Bump()\n    Println(c.N)"},
		{name: "Ptr val", body: "var c = Counter(0)\n    val p = &c\n    p.Bump()\n    Println(p.Count())"},
		{name: "RequirePtr", body: "RequirePtr(lookup(true)).Bump()"},
		{name: "OptionPtr", body: "OptionPtrOf(lookup(true)).ForEach((c) => c.Bump())"},
		{name: "Ptr method value", body: "var c = Counter(0)\n    val f = (&c).Count\n    Println(f())"},
		{name: "generic receiver", body: "var b = Box(1)\n    Println(b.Get(), b.Map((v) => v + 1))"},
		{
			name:  "Ptr receiver calling its own methods",
			decls: "func (c Ptr[Counter]) Twice() {\n    c.Bump()\n    c.Bump()\n}\n",
			body:  "var c = Counter(0)\n    c.Twice()",
		},
		{
			name:  "Ptr receiver passed as a Ptr",
			decls: "func show(c Ptr[Counter]) = Println(c.N)\n\nfunc (c Ptr[Counter]) Show() = show(c)\n",
			body:  "var c = Counter(0)\n    c.Show()",
		},
		{
			name:  "alias of a Ptr receiver",
			decls: "func (c Ptr[Counter]) Twice() {\n    val q = c\n    q.Bump()\n    q.Bump()\n}\n",
			body:  "var c = Counter(0)\n    c.Twice()",
		},
		{
			name:  "match through a MatchView on a Ptr receiver",
			decls: "func (c Ptr[Counter]) MatchView() Option[int] = Some(c.N)\n",
			body:  "var c = Counter(1)\n    (&c) match {\n        case Some(n) => Println(n)\n        case None() => Println(\"none\")\n    }",
		},
		{
			name:  "Ptr into an interface slot",
			decls: "type Bumper interface {\n    Bump()\n}\n",
			body:  "var c = Counter(0)\n    val b Bumper = &c\n    b.Bump()",
		},
		{
			name:  "nullable pointer calling a *T method",
			decls: "func (c *Counter) Peek() int = if (c == nil) 0 else c.N\n",
			body:  "Println(lookup(false).Peek())",
		},
		{
			name:  "nullable pointer into an interface of *T methods",
			decls: "type Peeker interface {\n    Peek() int\n}\n\nfunc (c *Counter) Peek() int = if (c == nil) 0 else c.N\n",
			body:  "val p Peeker = lookup(false)\n    Println(p.Peek())",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "package main\n\n" + ptrReceiverDecls + "\n" + tt.decls + "\nfunc main() {\n    " + tt.body + "\n}\n"
			_, err := trans.Transpile(src, "")
			require.NoError(t, err)
		})
	}
}

// TestPtrReceiverEntryCheck checks that an exported method on a Ptr receiver
// checks its receiver for nil on entry, for Go callers, and an unexported one
// does not; a generic method lowered to a function checks its receiver as
// the receiver, not as a parameter.
func TestPtrReceiverEntryCheck(t *testing.T) {
	out, err := newTranspiler().Transpile(`package main

struct Counter(var N int)

func (c Ptr[Counter]) Bump() {
    c.N = c.N + 1
}

func (c Ptr[Counter]) peek() int = c.N

struct Box[T any](var V T)

func (b Ptr[Box[T]]) Map[U any](f func(T) U) U = f(b.V)

func main() {
    var c = Counter(0)
    c.Bump()
    var b = Box(1)
    Println(c.peek(), b.Map((v) => v + 1))
}`, "")
	require.NoError(t, err)
	assert.Contains(t, out, `panic("Counter.Bump: receiver c is a Ptr[Counter], but the caller passed nil")`)
	assert.Contains(t, out, `panic("Box.Map: receiver b is a Ptr[Box[T]], but the caller passed nil")`)
	assert.NotContains(t, out, "Counter.peek: receiver")
	assert.NotContains(t, out, "parameter b is a")
}

// TestLoweredPointerReceiverAddress checks the receiver argument of a generic
// method on a `*T` or `Ptr[T]` receiver, which is lowered to a function: an
// addressable value is passed by its address, as Go does for a method call; a
// val or a call result through an addressed copy, as a method call on it
// gets; a pointer as it is (#800).
func TestLoweredPointerReceiverAddress(t *testing.T) {
	const decls = `struct Box[T any](var V T)

func (b *Box[T]) Map[U any](f func(T) U) U = f(b.V)

func (b Ptr[Box[T]]) Fold[U any](zero U, f func(U, T) U) U = f(zero, b.V)

func (b *Box[T]) Touch[U any]() Option[U] = None[U]()

func (b Box[T]) Show[U any](f func(T) U) U = f(b.V)

struct Holder(var box Box[int], fixed Box[int])

func newBox() Box[int] = Box(7)
`
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "var, *T", body: "var b = Box(1)\n    Println(b.Map((v) => v + 1))", want: "Box_Map(&b, "},
		{name: "var, Ptr", body: "var b = Box(1)\n    Println(b.Fold(0, (acc, v) => acc + v))", want: "Box_Fold(&b, "},
		{name: "var, no arguments", body: "var b = Box(1)\n    Println(b.Touch[string]())", want: "Box_Touch[string, int](&b)"},
		{name: "var field through a var", body: "var h = Holder(Box(1), Box(2))\n    Println(h.box.Map((v) => v + 1))", want: "Box_Map(&h.box, "},
		{name: "val field through a var", body: "var h = Holder(Box(1), Box(2))\n    Println(h.fixed.Fold(0, (acc, v) => acc + v))", want: "Box_Fold(std.AddrOfCopy(h.fixed.Get()), "},
		{name: "val", body: "val b = Box(1)\n    Println(b.Map((v) => v + 1))", want: "Box_Map(std.AddrOfCopy(b.Get()), "},
		{name: "call result", body: "Println(newBox().Fold(0, (acc, v) => acc + v))", want: "Box_Fold(std.AddrOfCopy(newBox()), "},
		{name: "pointer", body: "var b = Box(1)\n    val p = &b\n    Println(p.Map((v) => v + 1))", want: "Box_Map(p.Get(), "},
		{name: "address written out", body: "var b = Box(1)\n    Println((&b).Map((v) => v + 1))", want: "Box_Map((&b), "},
		{name: "value receiver", body: "var b = Box(1)\n    Println(b.Show((v) => v + 1))", want: "Box_Show(b, "},
	}
	trans := newTranspiler()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := trans.Transpile("package main\n\n"+decls+"\nfunc main() {\n    "+tt.body+"\n}\n", "")
			require.NoError(t, err)
			assert.Contains(t, out, tt.want)
		})
	}
}

// ptrBindDecls declares monads for bind/also: Cell's FlatMap and Zip2 have
// Ptr receivers, Pin's Zip2 has a Ptr receiver and takes a Ptr argument, and
// Lazy's FlatMap has a Ptr receiver, its Zip2 returns a pointer that may be
// nil and its Zip3 a Ptr; Tag's Zip2 takes an interface its Ptr implements,
// and Vee's Zip3 variadic Ptrs.
const ptrBindDecls = `struct Cell[T any](V T)

func (c Ptr[Cell[T]]) FlatMap[U any](f func(T) Cell[U]) Cell[U] = f(c.V)

func (c Ptr[Cell[T]]) Zip2[U any](o Cell[U]) Cell[Tuple[T, U]] = Cell(Tuple(c.V, o.V))

func find(found bool) *Cell[int] = if (found) &Cell(1) else nil

struct Pin[T any](V T)

func (p Pin[T]) FlatMap[U any](f func(T) Pin[U]) Pin[U] = f(p.V)

func (p Ptr[Pin[T]]) Zip2[U any](o Ptr[Pin[U]]) Pin[Tuple[T, U]] = Pin(Tuple(p.V, o.V))

func findPin() *Pin[int] = nil

struct Lazy[T any](V T)

func (l Ptr[Lazy[T]]) FlatMap[U any](f func(T) Lazy[U]) Lazy[U] = f(l.V)

func (l Lazy[T]) Zip2[U any](o Lazy[U]) *Lazy[Tuple[T, U]] = nil

func (l Lazy[T]) Zip3[U any, V any](o Lazy[U], p Lazy[V]) Ptr[Lazy[Tuple3[T, U, V]]] = &Lazy(Tuple3(l.V, o.V, p.V))

type Peeker interface {
    Peek() int
}

struct Tag[T any](V T)

func (g Ptr[Tag[T]]) Peek() int = 0

func (g Tag[T]) FlatMap[U any](f func(T) Tag[U]) Tag[U] = f(g.V)

func (g Tag[T]) Zip2[U any](o Peeker) Tag[Tuple[T, int]] = Tag(Tuple(g.V, o.Peek()))

func findTag() *Tag[int] = nil

struct Vee[T any](V T)

func (v Vee[T]) FlatMap[U any](f func(T) Vee[U]) Vee[U] = f(v.V)

func (v Vee[T]) Zip3[U any, W any](o ...Ptr[Vee[T]]) Vee[Tuple3[T, T, T]] = Vee(Tuple3(v.V, v.V, v.V))

func findVee() *Vee[int] = nil
`

// TestPtrReceiverBind checks that the FlatMap and ZipN calls a `bind` or
// `also` clause lowers to are checked as the same call written out (#811): a
// clause value that may be nil never becomes the receiver of a method on a
// `Ptr` receiver, nor the argument of a Zip parameter that is a Ptr
// (GALA-E0080), while a value that cannot be nil is bound as before.
func TestPtrReceiverBind(t *testing.T) {
	trans := newTranspiler()
	tests := []struct {
		name   string
		result string // the bind block's result type; Cell[int] when empty
		body   string
		msg    string // the GALA-E0080 message; empty when the block is allowed
		hint   string
	}{
		{
			name: "bind on a nullable call result",
			body: "bind a = find(false)\n    Cell(a + 1)",
			msg:  "cannot call FlatMap on find(false) (*Cell[int]): FlatMap has a Ptr[Cell[int]] receiver, and a pointer that may be nil cannot fill a Ptr",
			hint: "call it on RequirePtr(find(false)), which panics on nil; a pointer that may be nil is kept as OptionPtrOf(find(false))",
		},
		{
			name: "bind on a nullable val",
			body: "val c = find(true)\n    bind a = c\n    Cell(a + 1)",
			msg:  "cannot call FlatMap on c (*Cell[int])",
		},
		{
			name: "second bind of a chain",
			body: "bind a = Cell(1)\n    bind b = find(true)\n    Cell(a + b)",
			msg:  "cannot call FlatMap on find(true) (*Cell[int])",
		},
		{
			name: "bind after a statement in a continuation",
			body: "bind a = Cell(1)\n    val x = a + 1\n    bind b = find(true)\n    Cell(x + b)",
			msg:  "cannot call FlatMap on find(true) (*Cell[int])",
		},
		{
			name: "bind in a lambda inside a bind block",
			body: "bind a = Cell(1)\n    val f = (n int) => {\n        bind b = find(true)\n        Cell(n + b)\n    }\n    f(a)",
			msg:  "cannot call FlatMap on find(true) (*Cell[int])",
		},
		{
			name: "also whose Zip has a Ptr receiver",
			body: "bind a = find(true)\n    also b = Cell(2)\n    Cell(a + b)",
			msg:  "cannot call Zip2 on find(true) (*Cell[int]): Zip2 has a Ptr[Cell[int]] receiver",
		},
		{
			name: "also lowered to FlatMaps",
			body: "bind a = Cell(1)\n    also b = Cell(2)\n    also c = find(true)\n    Cell(a + b + c)",
			msg:  "cannot call FlatMap on find(true) (*Cell[int])",
		},
		{
			name:   "also passed to a Ptr Zip parameter",
			result: "Pin[int]",
			body:   "bind a = Pin(1)\n    also b = findPin()\n    Pin(a + b)",
			msg:    "cannot use findPin() (*Pin[int]) as Ptr[Pin[int]]",
		},
		{
			name:   "also passed to a Zip interface parameter",
			result: "Tag[int]",
			body:   "bind a = Tag(1)\n    also b = findTag()\n    Tag(a + b)",
			msg:    "cannot use findTag() (*Tag[int]) as Peeker: its method Peek has a Ptr[Tag[int]] receiver",
		},
		{
			name:   "also passed to a variadic Ptr Zip parameter",
			result: "Vee[int]",
			body:   "var q = Vee(2)\n    bind a = Vee(1)\n    also b = &q\n    also c = findVee()\n    Vee(a + b + c)",
			msg:    "cannot use findVee() (*Vee[int]) as Ptr[Vee[int]]",
		},
		{
			name:   "FlatMap on a nullable Zip result",
			result: "Lazy[int]",
			body:   "bind a = Lazy(1)\n    also b = Lazy(2)\n    Lazy(a + b)",
			msg:    "cannot bind this group through Zip2: it returns *Lazy[",
			hint:   "declare Zip2 to return Ptr[Lazy[Tuple[T, U]]] or a value",
		},
		{
			name: "first nullable clause reported first",
			body: "bind a = find(false)\n    bind b = find(true)\n    Cell(a + b)",
			msg:  "cannot call FlatMap on find(false) (*Cell[int])",
		},
		{
			name:   "first nullable clause of a Zip reported first",
			result: "Pin[int]",
			body:   "val p = findPin()\n    bind a = p\n    also b = findPin()\n    Pin(a + b)",
			msg:    "cannot call Zip2 on p (*Pin[int])",
		},
		{name: "bind on a Ptr", body: "bind a = RequirePtr(find(true))\n    Cell(a + 1)"},
		{name: "bind on an address", body: "var c = Cell(2)\n    bind a = &c\n    Cell(a + 1)"},
		{name: "bind on a var", body: "var c = Cell(3)\n    bind a = c\n    Cell(a + 1)"},
		{name: "bind on a val", body: "val c = Cell(4)\n    bind a = c\n    bind b = Cell(5)\n    Cell(a + b)"},
		{
			name: "bind on an OptionPtr's element",
			body: "OptionPtrOf(find(true)).Map((p) => {\n        bind a = p\n        Cell(a + 1)\n    }).GetOrElse(Cell(0))",
		},
		{name: "also on a Ptr", body: "bind a = RequirePtr(find(true))\n    also b = Cell(2)\n    Cell(a + b)"},
		{
			name:   "FlatMap on a Ptr Zip result",
			result: "Lazy[int]",
			body:   "bind a = Lazy(1)\n    also b = Lazy(2)\n    also c = Lazy(3)\n    Lazy(a + b + c)",
		},
		{name: "also on values", body: "bind a = Cell(1)\n    also b = Cell(2)\n    Cell(a + b)"},
		{
			name:   "also passing a Ptr to a Ptr Zip parameter",
			result: "Pin[int]",
			body:   "var q = Pin(2)\n    bind a = Pin(1)\n    also b = &q\n    Pin(a + b)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.result
			if result == "" {
				result = "Cell[int]"
			}
			src := "package main\n\n" + ptrBindDecls + "\nfunc run() " + result + " {\n    " + tt.body + "\n}\n\nfunc main() {\n    Println(run().V)\n}\n"
			_, err := trans.Transpile(src, "")
			if tt.msg == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr)
			assert.Equal(t, galaerr.CodePtrNullability, semErr.Code, "%v", err)
			assert.Contains(t, semErr.Msg, tt.msg)
			assert.Contains(t, semErr.Hint, tt.hint)
		})
	}
}

// TestPtrReceiverCrossPackage checks that a Ptr receiver declared in another
// package is enforced at a call site: the receiver's non-nullness is part of
// the method's metadata.
func TestPtrReceiverCrossPackage(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/ptrrecv\n\ngala dev\n")
	write("lib/lib.gala", `package lib

struct Counter(var N int)

func (c Ptr[Counter]) Bump() {
    c.N = c.N + 1
}

func (c *Counter) Peek() int = if (c == nil) 0 else c.N

struct Box[T any](var V T)

func (b Ptr[Box[T]]) Get() T = b.V

func Lookup(found bool) *Counter = if (found) &Counter(1) else nil

func FindBox() *Box[int] = nil
`)
	tests := []struct {
		name, body, msg string
	}{
		{name: "nullable call result", body: "lib.Lookup(false).Bump()", msg: "cannot call Bump on lib.Lookup(false) (*lib.Counter)"},
		{name: "nullable val", body: "val c = lib.Lookup(true)\n    c.Bump()", msg: "cannot call Bump on c (*lib.Counter)"},
		{name: "generic receiver", body: "Println(lib.FindBox().Get())", msg: "cannot call Get on lib.FindBox() (*lib.Box[int])"},
		{name: "addressable var", body: "var c = lib.Counter(0)\n    c.Bump()\n    Println(c.N)"},
		{name: "RequirePtr", body: "RequirePtr(lib.Lookup(true)).Bump()"},
		{name: "*T method on a nullable pointer", body: "Println(lib.Lookup(false).Peek())"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, "package main\n\nimport \"example.com/ptrrecv/lib\"\n\nfunc main() {\n    "+tt.body+"\n}\n")
			if tt.msg == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr)
			assert.Equal(t, galaerr.CodePtrNullability, semErr.Code, "%v", err)
			assert.Contains(t, semErr.Msg, tt.msg)
		})
	}
}

// TestCycleLoweredGoResultsMethod checks that a method whose first Go result
// is its receiver's type with other type arguments is lowered to a function,
// as one with that single result is: Go reports the instantiation cycle on
// the results it returns, not on their GALA value.
func TestCycleLoweredGoResultsMethod(t *testing.T) {
	out, err := newTranspiler().Transpile(`package main

struct Box[T any](V T)

func (b *Box[T]) Split() (Box[Tuple[T, T]], error) = Success(Box((b.V, b.V)))

func main() {
    var b = Box(1)
    Println(b.Split().IsSuccess())
}`, "")
	require.NoError(t, err)
	assert.Contains(t, out, "func Box_Split[T any](b *Box[T]) (Box[std.Tuple[T, T]], error)")
	assert.Contains(t, out, "Box_Split[int](&b)")
}

// TestCycleLoweredMethodCrossPackage checks that a method returning its
// receiver's type with other type arguments, which its package lowers to a
// function to avoid a Go instantiation cycle, is called as that function from
// another package, whether the receiver or the result is a Ptr (or two), a
// *T or a value, or the result is the first of a Go result list (#808). The
// methods are declared in a file of their own, beside the type's.
func TestCycleLoweredMethodCrossPackage(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/cyclerecv\n\ngala dev\n")
	write("box/box.gala", `package box

struct Box[T any](var V T)

func NewBox[T any](v T) Ptr[Box[T]] = &Box(v)
`)
	write("box/wrap.gala", `package box

func (b Ptr[Box[T]]) Wrap() Ptr[Box[Ptr[Box[T]]]] = &Box(b)

func (b *Box[T]) WrapStar() *Box[*Box[T]] = &Box(b)

func (b *Box[T]) Pair() Box[Tuple[T, T]] = Box((b.V, b.V))

func (b *Box[T]) Split() (Box[Tuple[T, T]], error) = Success(Box((b.V, b.V)))

func (b *Box[T]) Deep() Ptr[Ptr[Box[Tuple[T, T]]]] {
    var p = &Box((b.V, b.V))
    &p
}
`)
	tests := []struct {
		name, body, want string
	}{
		{name: "Ptr receiver and result", body: "Println(b.Wrap().V.V)", want: "box.Box_Wrap[int](b.Get())"},
		{name: "*T receiver and result", body: "Println(b.WrapStar().V.V)", want: "box.Box_WrapStar[int](b.Get())"},
		{name: "*T receiver, value result", body: "Println(b.Pair().V.V1)", want: "box.Box_Pair[int](b.Get())"},
		{name: "Go result list", body: "Println(b.Split().IsSuccess())", want: "box.Box_Split[int](b.Get())"},
		{name: "pointer to a pointer", body: "Println(b.Deep())", want: "box.Box_Deep[int](b.Get())"},
		{name: "addressable value", body: "var v = box.Box(2)\n    Println(v.WrapStar().V.V)", want: "box.Box_WrapStar[int](&v)"},
		{name: "val", body: "val w = box.Box(3)\n    Println(w.Pair().V.V1)", want: "box.Box_Pair[int](std.AddrOfCopy(w.Get()))"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := transpileCrossPkg(t, root, "package main\n\nimport \"example.com/cyclerecv/box\"\n\nfunc main() {\n    val b = box.NewBox(1)\n    "+tt.body+"\n}\n")
			require.NoError(t, err)
			assert.Contains(t, out, tt.want)
		})
	}
}
