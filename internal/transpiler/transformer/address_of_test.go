package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAddressOfImmutable checks that `&` of anything immutable is GALA-E0076 —
// a val, a parameter, a receiver, an immutable field, a field reached through
// a val — and that the address of anything writable is a plain Go pointer.
func TestAddressOfImmutable(t *testing.T) {
	trans := newTranspiler()

	rejected := []struct {
		name  string
		input string
		msg   string
		hint  string
	}{
		{
			name: "val",
			input: `package main

func main() {
    val x = 1
    val p = &x
    Println(*p)
}`,
			msg:  "cannot take the address of x: x is a val",
			hint: "declare it `var x`",
		},
		{
			name: "package-level val",
			input: `package main

val Limit = 10

func main() {
    val p = &Limit
    Println(*p)
}`,
			msg: "cannot take the address of Limit: Limit is a val",
		},
		{
			name: "val passed to a Go function taking a pointer",
			input: `package main

import "math/big"

func main() {
    val x = big.NewInt(2)
    val y = *x
    val c = big.NewInt(0)
    c.Add(&y, &y)
    Println(c.String())
}`,
			msg: "cannot take the address of y: y is a val",
		},
		{
			name: "val passed where an interface is expected",
			input: `package main

struct Counter(var N int)

func (c *Counter) Notify(s string) {
    c.N = c.N + 1
}

type Notifier interface {
    Notify(s string)
}

func send(n Notifier) {
    n.Notify("x")
}

func main() {
    val c = Counter(0)
    send(&c)
}`,
			msg: "cannot take the address of c: c is a val",
		},
		{
			name: "field reached through a val",
			input: `package main

struct Obj(var n int)

func main() {
    val o = Obj(1)
    val p = &o.n
    Println(*p)
}`,
			msg:  "cannot take the address of o.n: it is reached through val o",
			hint: "declare it `var o`",
		},
		{
			name: "nested field reached through a val",
			input: `package main

struct Inner(var n int)
struct Outer(var in Inner)

func main() {
    val o = Outer(Inner(1))
    val p = &o.in.n
    Println(*p)
}`,
			msg: "cannot take the address of o.in.n: it is reached through val o",
		},
		{
			name: "immutable field",
			input: `package main

struct Obj(n int)

func main() {
    var o = Obj(1)
    val p = &o.n
    Println(*p)
}`,
			msg:  "cannot take the address of o.n: field n is immutable",
			hint: "declare the field `var n`",
		},
		{
			name: "parameter",
			input: `package main

func f(n int) int {
    val p = &n
    *p
}`,
			msg: "cannot take the address of n: parameter n is not declared `var`",
		},
		{
			name: "receiver",
			input: `package main

struct Obj(var n int)

func (o Obj) Addr() int {
    val p = &o
    p.n
}`,
			msg: "cannot take the address of o: receiver o is immutable",
		},
	}
	for _, tt := range rejected {
		t.Run("rejected/"+tt.name, func(t *testing.T) {
			_, err := trans.Transpile(tt.input, "")
			require.Error(t, err)
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr)
			assert.Equal(t, galaerr.CodeAddressOfImmutable, semErr.Code)
			assert.Contains(t, semErr.Msg, tt.msg)
			assert.Contains(t, semErr.Hint, tt.hint)
		})
	}

	accepted := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "var",
			input: `package main

func main() {
    var x = 1
    val p = &x
    *p = 2
    Println(x)
}`,
			want: "var p = &x",
		},
		{
			name: "composite literal",
			input: `package main

struct Obj(var n int)

func main() {
    val p = &Obj(1)
    Println(p.n)
}`,
			want: "var p = &Obj{n: 1}",
		},
		{
			name: "var field of a var",
			input: `package main

struct Obj(var n int)

func main() {
    var o = Obj(1)
    val p = &o.n
    *p = 2
    Println(o.n)
}`,
			want: "var p = &o.n",
		},
		{
			name: "field of the pointee of a val",
			input: `package main

struct Obj(var n int)

func main() {
    var o = Obj(1)
    val ptr = &o
    val p = &ptr.n
    *p = 2
    Println(o.n)
}`,
			want: "var p = &ptr.n",
		},
		{
			name: "field of a pointer receiver",
			input: `package main

struct Obj(var n int)

func (o *Obj) Field() int {
    val p = &o.n
    *p = 3
    o.n
}`,
			want: "&o.n",
		},
	}
	for _, tt := range accepted {
		t.Run("accepted/"+tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			require.NoError(t, err)
			assert.Contains(t, got, tt.want)
		})
	}
}

// TestFieldWriteThroughVal checks that writing a field reached through a val
// is a GALA error, while a field of a var, of a pointee or of a parameter
// stays writable.
func TestFieldWriteThroughVal(t *testing.T) {
	trans := newTranspiler()
	const decls = `package main

struct Inner(var n int)
struct Outer(var in Inner)
struct Obj(var n int)

`
	rejected := []struct {
		name string
		body string
		msg  string
	}{
		{"assignment", "func main() {\n    val o = Obj(1)\n    o.n = 5\n}", "cannot assign to o.n: it is reached through val o"},
		{"compound assignment", "func main() {\n    val o = Obj(1)\n    o.n += 5\n}", "cannot assign to o.n: it is reached through val o"},
		{"increment", "func main() {\n    val o = Obj(1)\n    o.n++\n}", "cannot increment/decrement o.n: it is reached through val o"},
		{"nested field", "func main() {\n    val o = Outer(Inner(1))\n    o.in.n = 2\n}", "cannot assign to o.in.n: it is reached through val o"},
	}
	for _, tt := range rejected {
		t.Run("rejected/"+tt.name, func(t *testing.T) {
			_, err := trans.Transpile(decls+tt.body, "")
			requireSemanticError(t, err, tt.msg, "declare it `var o`")
		})
	}

	accepted := []struct {
		name string
		body string
	}{
		{"var", "func main() {\n    var o = Obj(1)\n    o.n = 5\n    o.n++\n    Println(o.n)\n}"},
		{"nested var", "func main() {\n    var o = Outer(Inner(1))\n    o.in.n = 2\n    Println(o.in.n)\n}"},
		{"pointee of a val", "func main() {\n    var o = Obj(1)\n    val p = &o\n    p.n = 5\n    Println(o.n)\n}"},
		{"value parameter", "func f(o Obj) int {\n    o.n = 5\n    o.n\n}"},
		{"pointer receiver", "func (o *Obj) Set(v int) {\n    o.n = v\n}"},
	}
	for _, tt := range accepted {
		t.Run("accepted/"+tt.name, func(t *testing.T) {
			_, err := trans.Transpile(decls+tt.body, "")
			require.NoError(t, err)
		})
	}
}

// TestReceiverAddressHint checks that the hint for `&recv` names the
// receiver's type, and says a pointer receiver already is the pointer.
func TestReceiverAddressHint(t *testing.T) {
	trans := newTranspiler()
	const decl = "package main\n\nstruct Obj(var n int)\n\n"

	_, err := trans.Transpile(decl+"func (o Obj) F() int {\n    val p = &o\n    p.n\n}\n", "")
	requireSemanticError(t, err, "cannot take the address of o: receiver o is immutable", "declare a pointer receiver (o *Obj)")

	_, err = trans.Transpile(decl+"func (o *Obj) F() int {\n    val pp = &o\n    (*pp).n\n}\n", "")
	requireSemanticError(t, err, "cannot take the address of o: receiver o is immutable", "o is already a pointer; pass o itself")
}
