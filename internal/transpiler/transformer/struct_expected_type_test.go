package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A generic struct whose type parameters its constructor arguments do not
// (all) determine takes the rest from the expected type, wherever one reaches
// the construction: `func f() Tag[int] = Tag("x")` is `Tag[int]{...}`.
//
// Every output also goes through the package's Go type-check oracle.

const structExpectedDecls = `package main

struct Tag[T any](name string)
struct Pair[A any, B any](a A)
type Named[T any] struct {
    Name string
}
type IntTag Tag[int]
type TagAlias Tag[int]

func takeTag(t Tag[int]) Tag[int] = t
func takePair(p Pair[int, string]) Pair[int, string] = p
func applyTag(f func(int) Tag[int]) Tag[int] = f(1)
struct Holder(t Tag[int])
`

// TestStructExpectedTypeMatrix crosses each position an expected type reaches
// a struct construction from with each construction whose type arguments come
// (partly) from that expected type.
func TestStructExpectedTypeMatrix(t *testing.T) {
	trans := newAliasExpectedTranspiler()

	ctors := []struct {
		typ, expr, want string
	}{
		{"Tag[int]", `Tag("x")`, "Tag[int]{"},
		{"Tag[int]", `Tag(name = "x")`, "Tag[int]{"},
		{"Named[int]", `Named(Name = "x")`, "Named[int]{"},
		{"Pair[int, string]", `Pair(1)`, "Pair[int, string]{"},
		{"Pair[int, string]", `Pair(a = 1)`, "Pair[int, string]{"},
		{"Pair[int, string]", `Pair[int](1)`, "Pair[int, string]{"},
		{"IntTag", `Tag("x")`, "Tag[int]{"},
	}
	positions := []struct {
		name string
		src  func(typ, expr string) string
	}{
		{"expression body", func(ty, e string) string { return "func f() " + ty + " = " + e }},
		{"block trailing value", func(ty, e string) string { return "func f() " + ty + " {\n    " + e + "\n}" }},
		{"explicit return", func(ty, e string) string { return "func f() " + ty + " {\n    return " + e + "\n}" }},
		{"val annotation", func(ty, e string) string {
			return "func f() " + ty + " {\n    val x " + ty + " = " + e + "\n    x\n}"
		}},
		{"if-else branches", func(ty, e string) string { return "func f(b bool) " + ty + " = if (b) " + e + " else " + e }},
		{"match arms", func(ty, e string) string {
			return "func f(n int) " + ty + " = n match {\n    case 0 => " + e + "\n    case _ => " + e + "\n}"
		}},
		{"if-else block branches", func(ty, e string) string {
			return "func f(b bool) " + ty + " = if (b) {\n    " + e + "\n} else {\n    " + e + "\n}"
		}},
		{"if statement tail", func(ty, e string) string {
			return "func f(b bool) " + ty + " {\n    if (b) {\n        " + e + "\n    } else {\n        " + e + "\n    }\n}"
		}},
		{"match arm blocks", func(ty, e string) string {
			return "func f(n int) " + ty + " = n match {\n    case 0 => {\n        " + e + "\n    }\n    case _ => " + e + "\n}"
		}},
		{"lambda expression result", func(ty, e string) string {
			return "func apply(g func(int) " + ty + ") " + ty + " = g(1)\nfunc f() " + ty + " = apply((n) => " + e + ")"
		}},
		{"lambda block result", func(ty, e string) string {
			return "func apply(g func(int) " + ty + ") " + ty + " = g(1)\nfunc f() " + ty + " = apply((n) => {\n    " + e + "\n})"
		}},
		{"lambda explicit return", func(ty, e string) string {
			return "func apply(g func(int) " + ty + ") " + ty + " = g(1)\nfunc f() " + ty + " = apply((n) => {\n    return " + e + "\n})"
		}},
		{"argument", func(ty, e string) string {
			return "func take(v " + ty + ") " + ty + " = v\nfunc f() " + ty + " = take(" + e + ")"
		}},
		{"parenthesized", func(ty, e string) string { return "func f() " + ty + " = (" + e + ")" }},
	}
	for _, pos := range positions {
		for _, c := range ctors {
			t.Run(pos.name+"/"+c.typ+"/"+c.expr, func(t *testing.T) {
				got, err := trans.Transpile(structExpectedDecls+pos.src(c.typ, c.expr)+"\n", "")
				require.NoError(t, err)
				assert.Contains(t, got, c.want)
			})
		}
	}
}

func TestStructExpectedTypeOtherPositions(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	tests := []struct {
		name, input, want string
	}{
		{"argument", `func f() Tag[int] = takeTag(Tag("x"))`, "takeTag(Tag[int]{"},
		{"partial argument", `func f() Pair[int, string] = takePair(Pair(1))`, "takePair(Pair[int, string]{"},
		{"struct field", `func f() Holder = Holder(Tag("x"))`, "Tag[int]{"},
		{"lambda result", `func f() Tag[int] = applyTag((n) => Tag("x"))`, "Tag[int]{"},
		{"lambda block result", "func f() Tag[int] = applyTag((n) => {\n    Tag(\"x\")\n})", "Tag[int]{"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(structExpectedDecls+tt.input+"\n", "")
			require.NoError(t, err)
			assert.Contains(t, got, tt.want)
		})
	}
}

// TestStructExpectedTypeOnlyForTheConstruction pins that a result type binds
// only the construction that is the result value itself. A construction nested
// in it, or one whose value is used some other way, has no expected type, and
// a type parameter nothing determines is reported with a copy-pasteable hint.
func TestStructExpectedTypeOnlyForTheConstruction(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	tests := []struct {
		name, input string
		want        []string
	}{
		{
			name:  "unannotated val in a function returning the struct",
			input: "func f() Tag[int] {\n    val t = Tag(\"x\")\n    t\n}",
			want: []string{
				"cannot infer type argument T of generic struct Tag from its fields or the expected type",
				"annotate the binding (e.g. `val x Tag[int] = Tag(...)`) or write it explicitly (`Tag[int](...)`)",
			},
		},
		{
			name:  "method called on the construction",
			input: "func (t Tag[T]) Same() Tag[T] = t\nfunc f() Tag[int] = Tag(\"x\").Same()",
			want:  []string{"cannot infer type argument T of generic struct Tag"},
		},
		{
			name:  "partial: the hint keeps the type argument the fields fix",
			input: "func f() int {\n    val p = Pair(1)\n    p.a\n}",
			want:  []string{"cannot infer type argument B of generic struct Pair", "(`Pair[int, int](...)`)"},
		},
		{
			name:  "result of another struct type",
			input: "func f() Pair[int, string] = Tag(\"x\")",
			want:  []string{"cannot infer type argument T of generic struct Tag"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(structExpectedDecls+tt.input+"\n", "")
			require.Error(t, err)
			for _, want := range tt.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}
