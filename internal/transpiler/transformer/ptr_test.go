package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPtrRejected checks that a value that may be nil never fills a `Ptr[T]`
// (GALA-E0080) — nil or a nullable `*T` at every kind of slot, a pointer that
// may be nil nested where a Ptr is expected — and that a Ptr is not compared
// with nil or tested by a typed pattern.
func TestPtrRejected(t *testing.T) {
	trans := newTranspiler()
	tests := []struct {
		name  string
		input string
		msg   string
		hint  string
	}{
		{
			name: "nil into an annotated val",
			input: `package main

func main() {
    val p Ptr[int] = nil
    Println(*p)
}`,
			msg:  "cannot use nil as Ptr[int]: a Ptr is never nil",
			hint: "OptionPtr[T]",
		},
		{
			name: "nil into a var inferred from &x",
			input: `package main

func main() {
    var x = 1
    var p = &x
    p = nil
    Println(p)
}`,
			msg: "cannot use nil as Ptr[int]",
		},
		{
			name: "nil argument",
			input: `package main

func show(p Ptr[int]) = Println(*p)

func main() {
    show(nil)
}`,
			msg: "cannot use nil as Ptr[int]",
		},
		{
			name: "nullable pointer argument",
			input: `package main

func show(p Ptr[int]) = Println(*p)

func find() *int = nil

func main() {
    show(find())
}`,
			msg:  "cannot use find() (*int) as Ptr[int]: a pointer that may be nil cannot fill a Ptr",
			hint: "pass RequirePtr(find())",
		},
		{
			name: "nullable pointer return",
			input: `package main

func wrap(p *int) Ptr[int] = p

func main() {
    var x = 1
    Println(*wrap(&x))
}`,
			msg: "cannot use p (*int) as Ptr[int]",
		},
		{
			name: "nullable pointer constructor field",
			input: `package main

struct Box(p Ptr[int])

func find() *int = nil

func main() {
    val b = Box(find())
    Println(*b.p)
}`,
			msg: "cannot use find() (*int) as Ptr[int]",
		},
		{
			name: "nullable pointer assigned to a Ptr var",
			input: `package main

func find() *int = nil

func main() {
    var x = 1
    var p = &x
    p = find()
    Println(*p)
}`,
			msg: "cannot use find() (*int) as Ptr[int]",
		},
		{
			name: "nullable pointer into Some for an Option of Ptr",
			input: `package main

func find() *int = nil

func main() {
    val ps Option[Ptr[int]] = Some(find())
    Println(ps)
}`,
			msg: "cannot use find() (*int) as Ptr[int]",
		},
		{
			name: "nullable pointer in a multi-value assignment",
			input: `package main

func find() *int = nil

func main() {
    var x = 1
    var p = &x
    var q = &x
    p, q = find(), &x
    Println(*p, *q)
}`,
			msg: "cannot use find() (*int) as Ptr[int]",
		},
		{
			name: "nullable pointer in a self tail call",
			input: `package main

func find() *int = nil

func walk(p Ptr[int], k int) int = if (k == 0) *p else walk(find(), k - 1)

func main() {
    var x = 1
    Println(walk(&x, 2))
}`,
			msg: "cannot use find() (*int) as Ptr[int]",
		},
		{
			name: "nullable pointer in a collection of Ptr",
			input: `package main

import . "martianoff/gala/collection_immutable"

func find() *int = nil

func main() {
    val ps Array[Ptr[int]] = ArrayOf(find())
    Println(ps.Size())
}`,
			msg: "as collection_immutable.Array[Ptr[int]]",
		},
		{
			name: "Ptr compared with nil",
			input: `package main

func main() {
    var x = 1
    val p = &x
    Println(p == nil)
}`,
			msg: "compares a Ptr[int] with nil, but a Ptr is never nil",
		},
		{
			name: "Ptr parameter compared with nil",
			input: `package main

func show(p Ptr[int]) {
    if p != nil {
        Println(*p)
    }
}

func main() {
    var x = 1
    show(&x)
}`,
			msg: "compares a Ptr[int] with nil",
		},
		{
			name: "typed pattern",
			input: `package main

func kind(v any) string = v match {
    case p: Ptr[int] => "ptr"
    case _ => "other"
}

func main() {
    Println(kind(1))
}`,
			msg: "a typed pattern cannot test for Ptr[int]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(tt.input, "")
			require.Error(t, err)
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr)
			assert.Equal(t, galaerr.CodePtrNullability, semErr.Code)
			assert.Contains(t, semErr.Msg, tt.msg)
			assert.Contains(t, semErr.Hint, tt.hint)
		})
	}
}

// TestPtrNoDefaultValue checks that a Ptr has no default value (GALA-E0075):
// an uninitialized var and a left-out field need a pointer written.
func TestPtrNoDefaultValue(t *testing.T) {
	trans := newTranspiler()
	tests := []struct {
		name  string
		input string
		msg   string
	}{
		{
			name: "uninitialized var",
			input: `package main

func main() {
    var p Ptr[int]
    Println(p)
}`,
			msg: "Ptr[int] has no default value: a Ptr is never nil",
		},
		{
			name: "left-out field",
			input: `package main

struct Node(Value int, Next Ptr[Node])

func main() {
    val n = Node{Value: 1}
    Println(n.Value)
}`,
			msg: "Ptr[Node] has no default value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(tt.input, "")
			require.Error(t, err)
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr)
			assert.Equal(t, galaerr.CodeNoDefaultValue, semErr.Code)
			assert.Contains(t, semErr.Msg, tt.msg)
		})
	}
}

// TestPtrCodecRejected checks that a codec rejects a Ptr field as a pointer
// shape (GALA-E0050).
func TestPtrCodecRejected(t *testing.T) {
	_, err := newTranspiler().Transpile(`package main

import "martianoff/gala/json"

struct Job(Name string, Owner Ptr[int])

func main() {
    val codec = json.Codec[Job](json.AsIs())
    Println(codec)
}`, "")
	require.Error(t, err)
	var semErr *galaerr.SemanticError
	require.ErrorAs(t, err, &semErr)
	assert.Equal(t, galaerr.CodeUnsupportedCodecField, semErr.Code)
	assert.Contains(t, semErr.Msg, "Ptr[int] has a pointer shape")
}

// TestPtrLowering checks that a Ptr is `*T` in generated Go — as a receiver,
// parameter, result, field and type argument — that `&x` and a Ptr fill
// `*T` and Ptr slots, and where the entry nil checks go: exported functions
// and methods, and lambdas filling a function slot with a nullable `*T`
// parameter, but no unexported function.
func TestPtrLowering(t *testing.T) {
	trans := newTranspiler()
	tests := []struct {
		name    string
		input   string
		want    []string
		notWant []string
	}{
		{
			name: "receiver, parameter, field and type argument erase to *T",
			input: `package main

struct Counter(var N int)

struct Holder(c Ptr[Counter])

func (c Ptr[Counter]) bump() {
    c.N = c.N + 1
}

func raw(c *Counter) int = c.N

func main() {
    var c = Counter(0)
    val p = &c
    p.bump()
    val h = Holder(p)
    val o = None[Ptr[Counter]]()
    Println(raw(h.c), o.IsEmpty())
}`,
			want: []string{
				"func (c *Counter) bump()",
				"c std.Immutable[*Counter]",
				"std.None[*Counter]{}.Apply()",
			},
			notWant: []string{"Ptr["},
		},
		{
			name: "exported function checks its Ptr parameters, unexported does not",
			input: `package main

func Show(p Ptr[int], q *int) = Println(*p, q)

func show(p Ptr[int]) = Println(*p)

func main() {
    var x = 1
    Show(&x, nil)
    show(&x)
}`,
			want: []string{
				`if p == nil {
		panic("Show: parameter p is a Ptr[int], but the caller passed nil")
	}`,
			},
			notWant: []string{"if q == nil", "show: parameter p"},
		},
		{
			name: "exported method checks its Ptr parameters",
			input: `package main

struct Box(var n int)

func (b Box) Add(p Ptr[int]) int = b.n + *p

func main() {
    var x = 2
    Println(Box(1).Add(&x))
}`,
			want: []string{`panic("Box.Add: parameter p is a Ptr[int], but the caller passed nil")`},
		},
		{
			name: "lambda with a Ptr parameter filling a *T slot checks it",
			input: `package main

func each(f func(*int), x *int) = f(x)

func main() {
    var x = 1
    each((p Ptr[int]) => Println(*p), &x)
}`,
			want: []string{`panic("lambda: parameter p is a Ptr[int], but the caller passed nil")`},
		},
		{
			name: "lambda filling a Ptr slot needs no check",
			input: `package main

func each(f func(Ptr[int]), x Ptr[int]) = f(x)

func main() {
    var x = 1
    each((p) => Println(*p), &x)
}`,
			notWant: []string{"panic("},
		},
		{
			name: "Ptr fills a *T slot and RequirePtr turns a *T into a Ptr",
			input: `package main

func find(p *int) *int = p

func show(p Ptr[int]) = Println(*p)

func main() {
    var x = 1
    val p = &x
    val q Ptr[int] = RequirePtr(find(p))
    show(q)
    show(OptionPtrOf(find(p)).Get())
}`,
			want: []string{"std.RequirePtr(find(p.Get()))", "show(q.Get())"},
		},
		{
			name: "Ptr in collections, generic structs, sealed cases and generic receivers",
			input: `package main

import . "martianoff/gala/collection_immutable"

struct Box[T any](var v T)

func (b Ptr[Box[T]]) set(v T) {
    b.v = v
}

struct Holder[T any](p Ptr[T])

sealed type Ref {
    case Strong(p Ptr[int])
    case Weak()
}

func deref(r Ref) int = r match {
    case Strong(p) => *p
    case Weak() => 0
}

func main() {
    var x = 1
    var y = 2
    val ps Array[Ptr[int]] = ArrayOf(&x, &y)
    val sum = ps.FoldLeft(0, (acc, p) => acc + *p)
    var b = Box(1)
    val bp = &b
    bp.set(5)
    val h = Holder(&x)
    Println(sum, b.v, *h.p, deref(Strong(&y)))
}`,
			want: []string{
				"func (b *Box[T]) set(v T)",
				"ArrayOf(&x, &y)",
			},
			notWant: []string{"Ptr["},
		},
		{
			name: "var Ptr reassigned from another address",
			input: `package main

func main() {
    var x = 1
    var y = 2
    var p = &x
    p = &y
    Println(*p)
}`,
			want: []string{"p = &y"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			require.NoError(t, err)
			for _, w := range tt.want {
				assert.Contains(t, got, w)
			}
			for _, nw := range tt.notWant {
				assert.NotContains(t, got, nw)
			}
		})
	}
}
