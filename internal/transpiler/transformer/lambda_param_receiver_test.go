package transformer_test

import (
	"martianoff/gala/galaerr"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireSemanticError asserts that err is a SemanticError whose message
// contains msg and whose hint contains hint.
func requireSemanticError(t *testing.T, err error, msg string, hint string) {
	t.Helper()
	require.Error(t, err)
	var semErr *galaerr.SemanticError
	require.ErrorAs(t, err, &semErr)
	assert.Contains(t, semErr.Msg, msg)
	assert.Contains(t, semErr.Hint, hint)
}

// TestLambdaParamReassignment checks that a lambda parameter follows the
// parameter rule: it is immutable unless declared `var`, in every lambda
// position, and the error and hint are the ones a function parameter gets.
func TestLambdaParamReassignment(t *testing.T) {
	trans := newTranspiler()

	rejected := []struct {
		name    string
		input   string
		wantMsg string
	}{
		{
			name: "annotated block lambda",
			input: `package main

func main() {
    val f = (x int) int => {
        x = x + 1
        x
    }
    Println(f(1))
}`,
			wantMsg: "cannot assign to immutable variable x",
		},
		{
			name: "parameter typed by the expected function type",
			input: `package main

func main() {
    val f func(int) int = (x) => {
        x = x * 2
        x
    }
    Println(f(1))
}`,
			wantMsg: "cannot assign to immutable variable x",
		},
		{
			name: "lambda passed to a generic function",
			input: `package main

func apply[T any](v T, f func(T) T) T = f(v)

func main() {
    Println(apply(1, (n) => {
        n = n + 1
        n
    }))
}`,
			wantMsg: "cannot assign to immutable variable n",
		},
		{
			name: "compound assignment",
			input: `package main

func main() {
    val f = (x int) int => {
        x += 1
        x
    }
    Println(f(1))
}`,
			wantMsg: "cannot assign to immutable variable x",
		},
		{
			name: "increment",
			input: `package main

func main() {
    val f = (x int) int => {
        x++
        x
    }
    Println(f(1))
}`,
			wantMsg: "cannot increment/decrement immutable variable x",
		},
		{
			name: "outer lambda parameter reassigned by a nested lambda",
			input: `package main

func main() {
    val f = (x int) int => {
        val g = (y int) int => {
            x = y
            y
        }
        g(x)
    }
    Println(f(1))
}`,
			wantMsg: "cannot assign to immutable variable x",
		},
		{
			name: "nested lambda reassigning its own parameter",
			input: `package main

func main() {
    val f = (x int) int => {
        val g = (y int) int => {
            y = y + x
            y
        }
        g(x)
    }
    Println(f(1))
}`,
			wantMsg: "cannot assign to immutable variable y",
		},
		{
			name: "range clause assigning a lambda parameter",
			input: `package main

import . "martianoff/gala/go_interop"

func main() {
    val f = (n int) int => {
        for _, n = range SliceOf(1, 2) {
        }
        n
    }
    Println(f(1))
}`,
			wantMsg: "cannot assign to immutable variable n",
		},
		{
			name: "range clause assigning a function parameter",
			input: `package main

import . "martianoff/gala/go_interop"

func last(n int) int {
    for _, n = range SliceOf(1, 2) {
    }
    n
}`,
			wantMsg: "cannot assign to immutable variable n",
		},
		{
			name: "lambda parameter of a method body",
			input: `package main

struct Acc(Base int)

func (a Acc) Plus() func(int) int = (n int) => {
    n = n + a.Base
    n
}`,
			wantMsg: "cannot assign to immutable variable n",
		},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(tt.input, "")
			requireSemanticError(t, err, tt.wantMsg, "declare it `var ")
		})
	}

	accepted := []struct {
		name  string
		input string
	}{
		{
			name: "var lambda parameter",
			input: `package main

func main() {
    val f = (var x int) int => {
        x = x + 1
        x
    }
    Println(f(1))
}`,
		},
		{
			name: "var lambda parameter typed by the expected function type",
			input: `package main

func apply[T any](v T, f func(T) T) T = f(v)

func main() {
    Println(apply(1, (var n) => {
        n += 1
        n
    }))
}`,
		},
		{
			name: "local copy of a lambda parameter",
			input: `package main

func main() {
    val f = (x int) int => {
        var y = x
        y = y + 1
        y
    }
    Println(f(1))
}`,
		},
		{
			name: "discard in the body of a blank parameter's function or lambda",
			input: `package main

func g() int = 3

func h(_ int) int {
    _ = g()
    1
}

func main() {
    val f = (_ int) int => {
        _ = g()
        1
    }
    Println(f(1) + h(2))
}`,
		},
		{
			name: "expression lambda reading its parameter",
			input: `package main

func main() {
    val f = (x int) => x + 1
    Println(f(1))
}`,
		},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
		})
	}
}

// TestLambdaParamAddressIsConstPtr checks that `&` of a lambda parameter not
// declared `var` is a read-only ConstPtr, as for a function parameter, and that
// a `var` lambda parameter's address stays a plain *T.
func TestLambdaParamAddressIsConstPtr(t *testing.T) {
	trans := newTranspiler()

	t.Run("write through the address", func(t *testing.T) {
		_, err := trans.Transpile(`package main

func main() {
    val f = (n int) int => {
        val p = &n
        *p = 5
        n
    }
    Println(f(1))
}`, "")
		requireSemanticError(t, err, "cannot assign through ConstPtr", "")
	})

	t.Run("read through the address", func(t *testing.T) {
		got, err := trans.Transpile(`package main

func main() {
    val f = (n int) int => {
        val p = &n
        1 + *p
    }
    Println(f(1))
}`, "")
		require.NoError(t, err)
		assert.Contains(t, got, "std.NewConstPtr(&n)")
	})

	t.Run("address of a var lambda parameter", func(t *testing.T) {
		got, err := trans.Transpile(`package main

func main() {
    val f = (var n int) int => {
        val p = &n
        *p = 5
        n
    }
    Println(f(1))
}`, "")
		require.NoError(t, err)
		assert.NotContains(t, got, "NewConstPtr")
	})
}

// TestReceiverRebinding checks that a receiver can never be rebound — value,
// pointer, generic, sealed and extractor receivers alike, with or without an
// explicit `val` — while mutation through it stays allowed.
func TestReceiverRebinding(t *testing.T) {
	trans := newTranspiler()

	rejected := []struct {
		name    string
		input   string
		wantMsg string
	}{
		{
			name: "value receiver",
			input: `package main

struct Point(X int, Y int)

func (p Point) Reset() Point {
    p = Point(0, 0)
    p
}`,
			wantMsg: "cannot assign to receiver p",
		},
		{
			name: "explicit val receiver",
			input: `package main

struct Point(X int, Y int)

func (val p Point) Reset() Point {
    p = Point(0, 0)
    p
}`,
			wantMsg: "cannot assign to receiver p",
		},
		{
			name: "pointer receiver",
			input: `package main

struct Point(var X int, var Y int)

func (p *Point) Retarget(var other Point) int {
    p = &other
    p.X
}`,
			wantMsg: "cannot assign to receiver p",
		},
		{
			name: "generic receiver",
			input: `package main

struct Box[T any](V T)

func (b Box[T]) Reset(v T) Box[T] {
    b = Box[T](v)
    b
}`,
			wantMsg: "cannot assign to receiver b",
		},
		{
			name: "sealed type receiver",
			input: `package main

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

func (s Shape) Unit() Shape {
    s = Circle(1.0)
    s
}`,
			wantMsg: "cannot assign to receiver s",
		},
		{
			name: "extractor receiver",
			input: `package main

struct Even()

func (e Even) Unapply(i int) Option[int] {
    e = Even()
    if (i % 2 == 0) Some(i) else None[int]()
}`,
			wantMsg: "cannot assign to receiver e",
		},
		{
			name: "compound assignment",
			input: `package main

type Count int

func (c Count) Next() Count {
    c += 1
    c
}`,
			wantMsg: "cannot assign to receiver c",
		},
		{
			name: "increment",
			input: `package main

type Count int

func (c Count) Next() Count {
    c++
    c
}`,
			wantMsg: "cannot increment/decrement receiver c",
		},
		{
			name: "receiver reassigned inside a lambda",
			input: `package main

struct Point(X int, Y int)

func (p Point) Later() func() Point = () => {
    p = Point(1, 1)
    p
}`,
			wantMsg: "cannot assign to receiver p",
		},
		{
			name: "receiver assigned by a range clause",
			input: `package main

import . "martianoff/gala/go_interop"

type Count int

func (c Count) Last() Count {
    for _, c = range SliceOf(Count(1), Count(2)) {
    }
    c
}`,
			wantMsg: "cannot assign to receiver c",
		},
		{
			name: "receiver rebound by a default lambda",
			input: `package main

struct Point(X int, Y int)

func (p Point) Get(f func() Point = () => {
    p = Point(0, 0)
    p
}) Point = f()

func main() {
    Println(Point(1, 2).Get().X)
}`,
			wantMsg: "cannot assign to receiver p",
		},
		{
			name: "var receiver",
			input: `package main

struct Point(X int, Y int)

func (var p Point) Sum() int = p.X + p.Y`,
			wantMsg: "receiver p cannot be declared `var`",
		},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(tt.input, "")
			requireSemanticError(t, err, tt.wantMsg, "a receiver cannot be rebound")
		})
	}

	accepted := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "var field written through a value receiver",
			input: `package main

struct Counter(var N int)

func (c Counter) Bumped() Counter {
    c.N = c.N + 1
    c
}`,
			want: "c.N = c.N + 1",
		},
		{
			name: "var field written through a pointer receiver",
			input: `package main

struct Counter(var N int)

func (c *Counter) Bump() {
    c.N = c.N + 1
}`,
			want: "c.N = c.N + 1",
		},
		{
			name: "mutating method called through a pointer receiver",
			input: `package main

struct Counter(var N int)

func (c *Counter) Bump() {
    c.N += 1
}

func (c *Counter) Twice() {
    c.Bump()
    c.Bump()
}`,
			want: "c.Bump()",
		},
		{
			name: "receiver copied into a local var",
			input: `package main

struct Point(X int, Y int)

func (p Point) Moved() Point {
    var q = p
    q = Point(q.X + 1, q.Y)
    q
}`,
			want: "func (p Point) Moved() Point",
		},
		{
			name: "address of a value receiver is a ConstPtr",
			input: `package main

struct Point(X int, Y int)

func (p Point) SumVia() int {
    val ptr = &p
    ptr.Deref().X + ptr.Deref().Y
}`,
			want: "std.NewConstPtr(&p)",
		},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			require.NoError(t, err)
			assert.Contains(t, got, tt.want)
		})
	}

	t.Run("write through the address of a value receiver", func(t *testing.T) {
		_, err := trans.Transpile(`package main

struct Point(X int, Y int)

func (p Point) Zeroed() Point {
    val ptr = &p
    *ptr = Point(0, 0)
    p
}`, "")
		requireSemanticError(t, err, "cannot assign through ConstPtr", "")
	})
}
