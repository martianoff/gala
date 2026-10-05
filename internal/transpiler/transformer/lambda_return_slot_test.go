package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A lambda has its own return slot. A `return`, a `bind` block and a match in
// its body take their type from the lambda — its annotation, the expected
// function type's result, or, when neither is known, from the body itself —
// and never from the enclosing function's return type.
func TestLambdaReturnSlot(t *testing.T) {
	const prelude = `package main

import "strconv"

func apply[T any](f func() T) T = f()

func parse(s string) Try[int] = Try(strconv.Atoi(s))
`
	tests := []struct {
		name        string
		body        string
		contains    []string
		notContains []string
	}{
		{
			name: "returned if-expression of lambdas ignores the enclosing function type",
			body: `
func scaler(k int) func(string) string {
    val scale = apply(() => {
        return if (k > 0) (x int) => x * k else (x int) => x
    })
    return (s) => s"$s:${scale(5)}"
}
`,
			contains:    []string{"func(x int) int {"},
			notContains: []string{"func(x int) string"},
		},
		{
			name: "a later return None() infers from the lambda's first return",
			body: `
func small(n int) Option[string] {
    val o = apply(() => {
        if (n > 5) {
            return Some(n)
        }
        return None()
    })
    return o.Map((v) => s"v=$v")
}
`,
			contains:    []string{"std.None[int]{}"},
			notContains: []string{"std.None[string]"},
		},
		{
			name: "an early return None() guard infers from a later return",
			body: `
func guard(o Option[int]) Option[int] = o.FlatMap((x) => {
    if (x < 0) {
        return None()
    }
    return Some(x + 1)
})
`,
			contains: []string{"return std.None[int]{}", "func(x int) std.Option[int] {"},
		},
		{
			name: "an early return None() guard infers from the trailing value",
			body: `
func guard(o Option[int]) Option[int] = o.FlatMap((x) => {
    if (x < 0) {
        return None()
    }
    Some(x * 10)
})
`,
			contains: []string{"return std.None[int]{}"},
		},
		{
			name: "a return with an unbound type parameter never fixes the slot",
			body: `
func failFirst(s string) Try[int] = parse(s).FlatMap((n) => {
    if (n > 3) {
        return Failure(strconv.ErrRange)
    }
    bind m = parse(s)
    Success(n + m)
})
`,
			contains:    []string{"std.Failure[int]{}", "func(n int) std.Try[int] {"},
			notContains: []string{"[T]", "Success[T]"},
		},
		{
			name: "a return in an inlined statement-position match fills the lambda's slot",
			body: `
func firstZero(n int) Option[string] {
    val o = apply(() => {
        n match {
            case 0 => {
                return Some(0)
            }
            case _ => {
                Println("x")
            }
        }
        return None()
    })
    return o.Map((v) => s"v=$v")
}
`,
			contains: []string{"return std.None[int]{}", "apply(func() std.Option[int] {"},
		},
		{
			name: "a deferred return keeps a statement-position match inlined",
			body: `
func zeroIsNone(n int) Option[string] {
    val o = apply(() => {
        n match {
            case 0 => {
                return None()
            }
            case _ => {
                Println("x")
            }
        }
        Some(n)
    })
    return o.Map((v) => s"v=$v")
}
`,
			contains: []string{"return std.None[int]{}", "apply(func() std.Option[int] {"},
		},
		{
			name: "a return is not typed from the match subject",
			body: `
func subjectNotResult(o Option[int]) string {
    val r = apply(() => {
        o match {
            case None() => {
                return None()
            }
            case _ => {
                Println("some")
            }
        }
        return Some("x")
    })
    return s"$r"
}
`,
			contains:    []string{"return std.None[string]{}", "apply(func() std.Option[string] {"},
			notContains: []string{"return std.None[int]"},
		},
		{
			name: "a trailing value is not typed from the match subject",
			body: `
func trailingNone(o Option[int]) string = o match {
    case Some(v) => {
        val r = apply(() => {
            if (v > 1) {
                return Some("x")
            }
            None()
        })
        s"$r"
    }
    case _ => "none"
}
`,
			contains:    []string{"apply(func() std.Option[string] {", "return std.None[string]{}"},
			notContains: []string{"return std.None[int]"},
		},
		{
			name: "a return in a branch of an if-expression a val is initialized with leaves the lambda",
			body: `
func doubledSize(s string) Option[string] {
    val o = apply(() => {
        if (s == "x") {
            return None()
        }
        val n = if (s == "") {
            return Some(0)
        } else {
            s.ByteSize()
        }
        Some(n * 2)
    })
    return o.Map((v) => s"v=$v")
}
`,
			contains: []string{"return std.None[int]{}", "return std.Some[int]{}.Apply(0)", "apply(func() std.Option[int] {"},
		},
		{
			name: "returned match takes the lambda's type",
			body: `
func classify(n int) Option[string] {
    val code = apply(() => {
        return n match {
            case 0 => 10
            case _ => n + 1
        }
    })
    return Some(s"code=$code")
}
`,
			contains: []string{"apply(func() int {"},
		},
		{
			name: "bind in a FlatMap lambda takes its monad from its trailing value",
			body: `
func viaFlatMap(s string) Try[int] =
    parse(s).FlatMap((n) => {
        bind h = parse(s"${n + 1}")
        Success(h * 2)
    })
`,
			contains: []string{"func(_bind_h int) std.Try[int] {", "std.Success[int]{}.Apply(h.Get() * 2)"},
		},
		{
			name: "bind in a Try thunk uses the thunk's result, not the enclosing Try",
			body: `
func viaThunk(s string) Try[Try[int]] = Try(() => {
    bind n = parse(s)
    Success(n + 1)
})
`,
			contains:    []string{"func() std.Try[int] {", "std.Try_FlatMap[int, int](parse(s), func(_bind_n int) std.Try[int] {"},
			notContains: []string{"std.Try[std.Try[std.Try[int]]]", "std.Success[std.Try[int]]"},
		},
		{
			name: "bind in a lambda with a declared function type uses that type",
			body: `
func viaTyped(s string) Option[string] {
    val f func(string) Try[int] = (x) => {
        bind n = parse(x)
        Success(n * 10)
    }
    return f(s).ToOption().Map((v) => s"$v")
}
`,
			contains: []string{"func(_bind_n int) std.Try[int] {"},
		},
		{
			name: "nested lambdas each have their own slot",
			body: `
func nested(n int) string {
    val outer = apply(() => {
        val inner = apply(() => {
            return if (n > 0) n * 2 else 0
        })
        return inner > 2
    })
    return s"nested=$outer"
}
`,
			contains:    []string{"apply(func() bool {", "apply(func() int {"},
			notContains: []string{"func() string"},
		},
		{
			name: "bind in a function body still uses the function's return type",
			body: `
func run(s string) Try[int] {
    bind n = parse(s)
    Success(n + 1)
}
`,
			contains: []string{"std.Try_FlatMap[int, int](parse(s), func(_bind_n int) std.Try[int] {"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newBindTranspiler().Transpile(prelude+tt.body, "")
			require.NoError(t, err)
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
			for _, bad := range tt.notContains {
				assert.NotContains(t, got, bad)
			}
		})
	}
}

// Inside a generic function or method, the declaration's own type parameters
// and its receiver's are resolved types: a value of type T or Option[T] fills
// a lambda's result slot.
func TestLambdaReturnSlotInGenericDeclarations(t *testing.T) {
	const prelude = `package main

import . "martianoff/gala/collection_immutable"

func apply[T any](f func() T) T = f()

struct Box[T any](V T)
`
	tests := []struct {
		name     string
		body     string
		contains []string
	}{
		{
			name:     "function type parameter",
			body:     "func genC[T any](xs Array[T]) Array[T] = xs.Map((x) => { return x })\n",
			contains: []string{"func(x T) T {"},
		},
		{
			name: "function type parameter inside a generic type",
			body: `
func genB[T any](x T) Option[T] {
    val r = apply(() => {
        return Some(x)
    })
    return r
}
`,
			contains: []string{"apply(func() std.Option[T] {"},
		},
		{
			name: "receiver type parameter",
			body: `
func (b Box[T]) Get() T {
    val f = () => {
        return b.V
    }
    return f()
}
`,
			contains: []string{"func() T {"},
		},
		{
			name: "generic method with a FoldLeft block lambda",
			body: `
func (b Box[T]) Pairs[U any](us Array[U]) Array[Tuple[T, U]] = us.FoldLeft(EmptyArray[Tuple[T, U]](), (acc, u) => {
    if (acc.Size() > 1) {
        return acc
    }
    acc.Append((b.V, u))
})
`,
			contains: []string{"return acc"},
		},
		{
			name: "generic method with a FlatMap guard",
			body: `
func (b Box[T]) With[U any](o Option[U], skip bool) Option[Tuple[T, U]] = o.FlatMap((u) => {
    if (skip) {
        return None()
    }
    Some((b.V, u))
})
`,
			contains: []string{"return std.None[std.Tuple[T, U]]{}"},
		},
		{
			name: "generic method: FlatMap guard whose trailing value calls a function parameter",
			body: `
func (b Box[T]) Lift[U any](f func(T) Option[U]) Option[U] = Some(b.V).FlatMap((v) => {
    if (false) {
        return None()
    }
    f(v)
})
`,
			contains: []string{"return std.None[U]{}", "func(v T) std.Option[U] {"},
		},
		{
			name: "generic function: FlatMap guard whose trailing value calls a function parameter",
			body: `
func lift[T any, U any](t T, f func(T) Option[U]) Option[U] = Some(t).FlatMap((v) => {
    if (false) {
        return None()
    }
    f(v)
})
`,
			contains: []string{"return std.None[U]{}", "func(v T) std.Option[U] {"},
		},
		{
			name: "generic function: return None() typed by a declared type parameter",
			body: `
func direct[U any](b bool, o Option[U]) Option[U] {
    if (b) {
        return None()
    }
    return o
}
`,
			contains: []string{"return std.None[U]{}"},
		},
		{
			name: "generic method: return None() typed by a receiver type parameter",
			body: `
func (b Box[T]) Direct(k bool) Option[T] {
    if (k) {
        return None()
    }
    return Some(b.V)
}
`,
			contains: []string{"return std.None[T]{}"},
		},
		{
			name: "generic method: return None() typed by the method's own type parameter",
			body: `
func (b Box[T]) Pick[U any](k bool, o Option[U]) Option[U] {
    if (k) {
        return None()
    }
    return o
}
`,
			contains: []string{"return std.None[U]{}"},
		},
		{
			name: "generic method: None() in a match arm typed by the method's own type parameter",
			body: `
func (b Box[T]) Keep[U any](o Option[U]) Option[U] = o match {
    case Some(u) => Some(u)
    case _ => None()
}
`,
			contains: []string{"std.None[U]{}"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newBindTranspiler().Transpile(prelude+tt.body, "")
			require.NoError(t, err)
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
		})
	}
}

// A lambda of unknown result type needs one result value of a known type, and
// a `bind` block in it must end with a value of the block's monad; anything
// else is rejected with a GALA error, not handed to the Go compiler.
func TestLambdaReturnSlotErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name: "trailing value of another monad",
			input: `package main

import "strconv"

func apply[T any](f func() T) T = f()

func run(s string) string {
    val r = apply(() => {
        bind n = Try(strconv.Atoi(s))
        Some(n)
    })
    return s"$r"
}
`,
			wantErr: "must end with a std.Try value",
		},
		{
			name: "no result value has a fully known type",
			input: `package main

import "strconv"

func apply[T any](f func() T) T = f()

func run() string {
    val r = apply(() => {
        return Failure(strconv.ErrRange)
    })
    return s"$r"
}
`,
			wantErr: "cannot infer the result type of this lambda",
		},
		{
			// The match subject is not the lambda's result: it types nothing.
			name: "a trailing None() in a match nothing else types",
			input: `package main

func apply[T any](f func() T) T = f()

func onlyTrailingNone(o Option[int]) string = o match {
    case Some(_) => {
        val r = apply(() => {
            Println("side effect")
            None()
        })
        s"$r"
    }
    case _ => "none"
}
`,
			wantErr: "cannot infer the result type of this lambda",
		},
		{
			name: "bind in a function with no return type",
			input: `package main

import "strconv"

func run(s string) {
    bind n = Try(strconv.Atoi(s))
    Success(n)
}
`,
			wantErr: "`bind` requires the enclosing function to declare a monad return type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newBindTranspiler().Transpile(tt.input, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
