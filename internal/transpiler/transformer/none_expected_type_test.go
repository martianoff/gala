package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A bare `None()` passed as an argument takes its type from the parameter it
// fills, whether the callee is generic or not. A parameter type that does not
// mention the callee's type parameters (`Option[Drag]`) is known before any
// type argument is; one that does (`Option[T]`) is known once T is fixed by
// the explicit type arguments or by the other arguments.
func TestNoneArgumentOfGenericCall(t *testing.T) {
	const prelude = `package main

struct Drag(X int)

func take[T any](d Option[Drag], v T) T = v

func take2[T any](d Option[T], v T) T = d.GetOrElse(v)

struct Box(N int)

func (b Box) Take[T any](d Option[Drag], v T) T = v

func (b Box) Take2[T any](d Option[T], v T) T = d.GetOrElse(v)

struct Cell[T any](V T)

func (c Cell[T]) Put(d Option[Drag], v T) T = v

func (c Cell[T]) Or(d Option[T]) T = d.GetOrElse(c.V)
`
	tests := []struct {
		name        string
		body        string
		contains    []string
		notContains []string
	}{
		{
			name:     "explicit type arguments, concrete parameter",
			body:     "func main() { Println(take[int](None(), 1)) }\n",
			contains: []string{"take[int](std.None[Drag]{}.Apply(), 1)"},
		},
		{
			name:     "inferred type arguments, concrete parameter",
			body:     "func main() { Println(take(None(), 1)) }\n",
			contains: []string{"std.None[Drag]{}.Apply()"},
		},
		{
			name:     "explicit type arguments, parameter mentions T",
			body:     "func main() { Println(take2[int](None(), 1)) }\n",
			contains: []string{"take2[int](std.None[int]{}.Apply(), 1)"},
		},
		{
			name:     "T fixed by another argument",
			body:     "func main() { Println(take2(None(), \"x\")) }\n",
			contains: []string{"std.None[string]{}.Apply()"},
		},
		{
			name:     "named arguments",
			body:     "func main() { Println(take(v = 1, d = None())) }\n",
			contains: []string{"std.None[Drag]{}.Apply()"},
		},
		{
			name:     "named arguments, parameter mentions T",
			body:     "func main() { Println(take2(v = 2.5, d = None())) }\n",
			contains: []string{"std.None[float64]{}.Apply()"},
		},
		{
			name:     "generic method, concrete parameter",
			body:     "func main() { Println(Box(1).Take(None(), 1)) }\n",
			contains: []string{"std.None[Drag]{}.Apply()"},
		},
		{
			name:     "generic method, explicit type arguments",
			body:     "func main() { Println(Box(1).Take[string](None(), \"s\")) }\n",
			contains: []string{"std.None[Drag]{}.Apply()"},
		},
		{
			name:     "generic method, parameter mentions T",
			body:     "func main() { Println(Box(1).Take2(None(), true)) }\n",
			contains: []string{"std.None[bool]{}.Apply()"},
		},
		{
			name:     "method of a generic type, concrete parameter",
			body:     "func main() { Println(Cell(1).Put(None(), 2)) }\n",
			contains: []string{"std.None[Drag]{}.Apply()"},
		},
		{
			name:     "method of a generic type, parameter mentions the receiver's T",
			body:     "func main() { Println(Cell(\"a\").Or(None())) }\n",
			contains: []string{"std.None[string]{}.Apply()"},
		},
		{
			name:     "positional struct constructor",
			body:     "struct Pin(D Option[Drag], N int)\n\nfunc main() { Println(Pin(None(), 1)) }\n",
			contains: []string{"std.None[Drag]{}.Apply()"},
		},
		{
			name:     "positional struct constructor, nested",
			body:     "struct Pin(D Option[Drag], N int)\n\nstruct Two(A Pin, B Option[int])\n\nfunc main() { Println(Two(Pin(None(), 1), None())) }\n",
			contains: []string{"std.None[Drag]{}.Apply()", "std.None[int]{}.Apply()"},
		},
		{
			name:     "positional struct constructor of a struct with a companion Apply",
			body:     "struct Cfg(Name Option[string], Port int)\n\nfunc (c Cfg) Apply(p Option[int]) Cfg = Cfg(None[string](), p.GetOrElse(0))\n\nfunc main() { Println(Cfg(None(), 1)) }\n",
			contains: []string{"Cfg{Name: std.NewImmutable(std.None[string]{}.Apply())"},
		},
		{
			name:        "a call that goes to the companion Apply does not take a field's type",
			body:        "struct Cfg(Name Option[string], Port int)\n\nfunc (c Cfg) Apply(p Option[int]) Cfg = Cfg(None[string](), p.GetOrElse(0))\n\nfunc main() { Println(Cfg(Some(8080))) }\n",
			contains:    []string{"Cfg{}.Apply(std.Some[int]{}.Apply(8080))"},
			notContains: []string{"Some[string]"},
		},
		{
			name:     "positional sealed case constructor",
			body:     "sealed type Ev {\n    case Move(D Option[Drag])\n    case Stop()\n}\n\nfunc main() { Println(Move(None())) }\n",
			contains: []string{"std.None[Drag]{}.Apply()"},
		},
		{
			name:     "generic struct built with the enclosing function's same-named type parameter",
			body:     "struct Slot[T any](D Option[T], V T)\n\nfunc mk[T any](x T) Slot[T] = Slot[T](None(), x)\n\nfunc main() { Println(mk(1)) }\n",
			contains: []string{"std.None[T]{}.Apply()"},
		},
		{
			name:        "a field declared Immutable[T] takes a T, wrapped once",
			body:        "struct Held(X Immutable[int])\n\nfunc main() { Println(Held(5)) }\n",
			contains:    []string{"Held{X: std.NewImmutable(5)}"},
			notContains: []string{"NewImmutable(std.NewImmutable"},
		},
		{
			name:     "positional generic struct constructor, field mentions T",
			body:     "struct Slot[T any](D Option[T], V T)\n\nfunc main() { Println(Slot[string](None(), \"x\")) }\n",
			contains: []string{"std.None[string]{}.Apply()"},
		},
		{
			name:        "the argument's parameter wins over the enclosing result type",
			body:        "func f() Option[int] = Some(take(None(), 1))\n\nfunc main() { Println(f()) }\n",
			contains:    []string{"std.None[Drag]{}.Apply()"},
			notContains: []string{"std.None[int]{}.Apply()"},
		},
		{
			name:        "the argument's parameter wins over the match subject",
			body:        "func f(o Option[int]) int = o match {\n    case Some(n) => take(None(), n)\n    case _ => 0\n}\n\nfunc main() { Println(f(Some(1))) }\n",
			contains:    []string{"std.None[Drag]{}.Apply()"},
			notContains: []string{"std.None[int]{}.Apply()"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertTranspiled(t, prelude+tt.body, tt.contains, tt.notContains)
		})
	}
}

// assertTranspiled transpiles src and checks that the Go output has every
// fragment of contains and none of notContains.
func assertTranspiled(t *testing.T, src string, contains, notContains []string) {
	t.Helper()
	got, err := newBindTranspiler().Transpile(src, "")
	require.NoError(t, err)
	for _, want := range contains {
		assert.Contains(t, got, want)
	}
	for _, bad := range notContains {
		assert.NotContains(t, got, bad)
	}
}

// A bare `None()` that is the value of a match arm or an if/else branch takes
// the type of the value the match or if produces, never the type of the
// matched value. With an expected type (a typed val, a result slot, an
// argument) that type is used; without one, the type the other arms or
// branches produce.
func TestNoneBranchValueTypedByResult(t *testing.T) {
	const prelude = `package main

struct Drag(X int)

func take[T any](d Option[Drag], v T) T = v
`
	tests := []struct {
		name        string
		body        string
		contains    []string
		notContains []string
	}{
		{
			name: "match arm typed by a sibling arm",
			body: `
func pick[T any](o Option[int], v T) T {
    val m = o match {
        case Some(_) => Some(v)
        case None() => None()
    }
    m.GetOrElse(v)
}
`,
			contains:    []string{"std.None[T]{}.Apply()"},
			notContains: []string{"std.None[int]{}.Apply()"},
		},
		{
			name: "match arm typed by a later sibling arm",
			body: `
func pick[T any](o Option[int], v T) T {
    val m = o match {
        case None() => None()
        case Some(_) => Some(v)
    }
    m.GetOrElse(v)
}
`,
			contains:    []string{"std.None[T]{}.Apply()"},
			notContains: []string{"std.None[int]{}.Apply()"},
		},
		{
			name: "match on Option[any] never yields None[any]",
			body: `
func pick[T any](o Option[any], v T) T {
    val m = o match {
        case Some(_) => Some(v)
        case _ => None()
    }
    m.GetOrElse(v)
}
`,
			contains:    []string{"std.None[T]{}.Apply()"},
			notContains: []string{"std.None[any]{}.Apply()"},
		},
		{
			name: "default arm typed by a sibling arm",
			body: `
func label(o Option[int]) string {
    val m = o match {
        case Some(n) => Some(s"n=$n")
        case _ => None()
    }
    m.GetOrElse("none")
}
`,
			contains:    []string{"std.None[string]{}.Apply()"},
			notContains: []string{"std.None[int]{}.Apply()"},
		},
		{
			name: "block arm typed by a sibling arm",
			body: `
func label(o Option[int]) string {
    val m = o match {
        case Some(n) => Some(s"n=$n")
        case _ => {
            Println("empty")
            None()
        }
    }
    m.GetOrElse("none")
}
`,
			contains:    []string{"std.None[string]{}.Apply()"},
			notContains: []string{"std.None[int]{}.Apply()"},
		},
		{
			name: "sibling arm wins over the enclosing result type",
			body: `
func count(o Option[int]) Option[int] {
    val m = o match {
        case Some(n) => Some(s"n=$n")
        case _ => None()
    }
    m.Map((s) => s.Size())
}
`,
			contains:    []string{"std.None[string]{}.Apply()"},
			notContains: []string{"std.None[int]{}.Apply()"},
		},
		{
			name: "typed val",
			body: `
func label(o Option[int]) string {
    val m Option[string] = o match {
        case Some(_) => None()
        case _ => Some("empty")
    }
    m.GetOrElse("x")
}
`,
			contains:    []string{"std.None[string]{}.Apply()"},
			notContains: []string{"std.None[int]{}.Apply()"},
		},
		{
			name: "result slot",
			body: `
func flip(o Option[int]) Option[string] = o match {
    case Some(_) => None()
    case _ => Some("empty")
}
`,
			contains:    []string{"std.None[string]{}.Apply()"},
			notContains: []string{"std.None[int]{}.Apply()"},
		},
		{
			name: "argument slot",
			body: `
func useIt(o Option[int]) int = take(o match {
    case Some(_) => None()
    case _ => Some(Drag(1))
}, 7)
`,
			contains:    []string{"std.None[Drag]{}.Apply()"},
			notContains: []string{"std.None[int]{}.Apply()"},
		},
		{
			name: "if/else branch typed by the other branch",
			body: `
func pick[T any](b bool, v T) T {
    val m = if (b) Some(v) else None()
    m.GetOrElse(v)
}
`,
			contains: []string{"std.None[T]{}.Apply()"},
		},
		{
			name: "if/else branch typed by the other branch, None first",
			body: `
func pick[T any](b bool, v T) T {
    val m = if (b) None() else Some(v)
    m.GetOrElse(v)
}
`,
			contains: []string{"std.None[T]{}.Apply()"},
		},
		{
			name: "if/else inside a match arm is not typed by the match subject",
			body: `
func label(o Option[int], b bool) string = o match {
    case Some(_) => {
        val m = if (b) None() else Some("yes")
        m.GetOrElse("no")
    }
    case _ => "none"
}
`,
			contains:    []string{"std.None[string]{}.Apply()"},
			notContains: []string{"std.None[int]{}.Apply()"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertTranspiled(t, prelude+tt.body+"\nfunc main() {}\n", tt.contains, tt.notContains)
		})
	}
}
