package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A construction or generic call takes the type arguments its arguments leave
// open only from the slot it fills. The enclosing function's result type is
// that slot only for the value that is the result: a zero-argument sealed
// variant (`None()`), a tuple element or a call of a generic function whose
// type parameter only its result mentions (`parse()`) bound to an unannotated
// `val` in such a function gets nothing from it. A branch of an if or match
// with no slot type takes it from its sibling branches instead.
//
// Every output also goes through the package's Go type-check oracle.

const slotTypeArgDecls = `package main

struct Phantom[T any]()
struct Tagged[T any](Name string)

type MkTag[T any] struct {}
func (m MkTag[T]) Apply(name string) Tagged[T] = Tagged[T](name)

func parse[T any]() Option[T] = None[T]()
func parseS[T any](s string) Option[T] = None[T]()
func pair[A any, B any](a A) Tuple[A, Option[B]] = (a, None[B]())
func runThunk[T any](f func() T) T = f()
`

// TestSlotTypeArgsMatrix crosses each position a slot type reaches a value
// from with the zero-argument variant and the generic calls that take their
// type arguments from it.
func TestSlotTypeArgsMatrix(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	values := []struct {
		typ, expr, want string
	}{
		{"Option[string]", `None()`, "std.None[string]{}.Apply()"},
		{"Option[string]", `parse()`, "parse[string]()"},
		{"Option[string]", `parseS("x")`, `parseS[string]("x")`},
		{"Tuple[int, Option[string]]", `pair(1)`, "pair[int, string](1)"},
	}
	for _, pos := range expectedTypePositions {
		for _, v := range values {
			t.Run(pos.name+"/"+v.typ+"/"+v.expr, func(t *testing.T) {
				got, err := trans.Transpile(slotTypeArgDecls+pos.src(v.typ, v.expr)+"\n", "")
				require.NoError(t, err)
				assert.Contains(t, got, v.want)
			})
		}
	}
}

// TestSlotTypeArgsNotFromEnclosingResult pins that a value which is not the
// result value takes nothing from the enclosing function's result type: with
// no slot of its own, a type argument nothing else determines is an error
// with the pasteable hint.
func TestSlotTypeArgsNotFromEnclosingResult(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	tests := []struct {
		name, input string
		want        []string
	}{
		{
			name:  "zero-arg variant bound to an unannotated val",
			input: "func find(ok bool) Option[string] {\n    val empty = None()\n    if (ok) Some(\"x\") else empty\n}",
			want: []string{
				"GALA-E0018", `cannot infer type parameter for sealed variant constructor "None()"`,
				"annotate the binding (e.g. `val x Option[int] = None()`) or pass type args explicitly (`None[int]()`)",
			},
		},
		{
			name:  "zero-arg variant bound to a val inside a match arm",
			input: "func nested(x int) Option[int] {\n    val r = x match {\n        case 1 => {\n            val d = None()\n            d.GetOrElse(0)\n        }\n        case _ => 5\n    }\n    Some(r)\n}",
			want:  []string{"GALA-E0018"},
		},
		{
			name:  "zero-arg variant in a tuple bound to a val",
			input: "func f() Tuple[Option[string], int] {\n    val p = (None(), 1)\n    p\n}",
			want:  []string{"GALA-E0018"},
		},
		{
			name:  "zero-arg variant a method is called on",
			input: "func f() Option[string] = None().OrElse(Some(\"x\"))",
			want:  []string{"GALA-E0018"},
		},
		{
			name:  "nullary generic call bound to an unannotated val",
			input: "func g() Option[string] {\n    val raw = parse()\n    raw\n}",
			want: []string{
				"GALA-E0067", "cannot infer type argument T of parse from its arguments or the expected type",
				"annotate the binding (e.g. `val x Option[int] = parse(...)`) or pass type args explicitly (`parse[int](...)`)",
			},
		},
		{
			name:  "generic call with arguments bound to an unannotated val",
			input: "func g() Option[string] {\n    val raw = parseS(\"x\")\n    raw\n}",
			want:  []string{"GALA-E0067", "cannot infer type argument T of parseS"},
		},
		{
			name:  "the hint keeps the type argument the arguments fix",
			input: "func g() Tuple[int, Option[string]] {\n    val p = pair(1)\n    p\n}",
			want:  []string{"GALA-E0067", "cannot infer type argument B of pair", "(`pair[int, int](...)`)"},
		},
		{
			name:  "generic call a method is called on",
			input: "func g() string = parse().GetOrElse(\"x\")",
			want:  []string{"GALA-E0067", "cannot infer type argument T of parse"},
		},
		{
			name:  "generic call in statement position",
			input: "func g() Option[string] {\n    parse()\n    Some(\"x\")\n}",
			want:  []string{"GALA-E0067", "cannot infer type argument T of parse"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(slotTypeArgDecls+tt.input+"\n", "")
			require.Error(t, err)
			for _, want := range tt.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

// TestSlotTypeArgsFromSiblingBranches pins that a branch whose construction or
// generic call has no type argument of its own takes it from the type its
// sibling branches unify to, as a bare `None()` branch does, whichever branch
// comes first.
func TestSlotTypeArgsFromSiblingBranches(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	ifVal := func(a, b string) string {
		return "func f(ok bool) int {\n    val r = if (ok) " + a + " else " + b + "\n    Println(r)\n    1\n}"
	}
	matchVal := func(a, b string) string {
		return "func f(n int) int {\n    val r = n match {\n        case 0 => " + a + "\n        case _ => " + b + "\n    }\n    Println(r)\n    1\n}"
	}
	tests := []struct {
		name, input, want string
	}{
		{"struct construction", ifVal("Phantom[int]()", "Phantom()"), "Phantom[int]{}"},
		{"struct construction first", ifVal("Phantom()", "Phantom[int]()"), "Phantom[int]{}"},
		{"companion Apply", ifVal(`Tagged[int]("a")`, `MkTag("b")`), `MkTag[int]{}.Apply("b")`},
		{"companion Apply first", ifVal(`MkTag("b")`, `Tagged[int]("a")`), `MkTag[int]{}.Apply("b")`},
		{"nullary generic call", ifVal("Some(1)", "parse()"), "parse[int]()"},
		{"nullary generic call first", ifVal("parse()", "Some(1)"), "parse[int]()"},
		{"generic call with arguments", ifVal(`parseS("x")`, "Some(1)"), `parseS[int]("x")`},
		{"match arm", matchVal("parse()", "Some(1)"), "parse[int]()"},
		{"match arm block", matchVal("{\n            Println(0)\n            Phantom()\n        }", "Phantom[string]()"), "Phantom[string]{}"},
		{"zero-arg variant next to a generic call", ifVal("None()", `parseS[int]("x")`), "std.None[int]{}.Apply()"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(slotTypeArgDecls+tt.input+"\n", "")
			require.NoError(t, err)
			assert.Contains(t, got, tt.want)
		})
	}

	// Branches that all leave the type argument open have no type to share.
	_, err := trans.Transpile(slotTypeArgDecls+ifVal("parse()", "Phantom()")+"\n", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0067")
}

// TestSlotTypeArgsLambdaTrailingValue pins that a lambda's trailing value fills
// the slot an earlier `return` settled, so a zero-argument variant or generic
// call there takes its type from it rather than from the enclosing match
// subject or the enclosing function's result type.
func TestSlotTypeArgsLambdaTrailingValue(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	tests := []struct {
		name, value, want string
	}{
		{"zero-arg variant", "None()", "std.None[string]{}.Apply()"},
		{"generic call", "parse()", "parse[string]()"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "func f(o Option[int]) Option[int] = o match {\n    case Some(v) => {\n" +
				"        val r = runThunk(() => {\n            if (v > 1) {\n                return Some(\"x\")\n            }\n            " + tt.value + "\n        })\n" +
				"        Println(r)\n        o\n    }\n    case _ => o\n}"
			got, err := trans.Transpile(slotTypeArgDecls+src+"\n", "")
			require.NoError(t, err)
			assert.Contains(t, got, tt.want)
		})
	}
}
