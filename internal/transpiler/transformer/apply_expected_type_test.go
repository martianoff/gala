package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A companion Apply construction (`MkTag("x")`) whose arguments leave a type
// parameter open takes it from the expected type, matched against what Apply
// returns, under the rule a generic struct construction follows: a result
// type binds only the construction that is the result value, never one bound
// to an unannotated `val` or nested in the result.
//
// Every output also goes through the package's Go type-check oracle.

const applyExpectedDecls = `package main

struct Tagged[T any](Name string)
struct Half[A any, B any](First A)

type MkTag[T any] struct {}
func (m MkTag[T]) Apply(name string) Tagged[T] = Tagged[T](name)

type MkHalf[A any, B any] struct {}
func (m MkHalf[A, B]) Apply(a A) Half[A, B] = Half[A, B](a)

type IntTagged Tagged[int]
`

// TestApplyExpectedTypeMatrix crosses each position an expected type reaches
// a construction from with each companion Apply construction whose type
// arguments come (partly) from that expected type.
func TestApplyExpectedTypeMatrix(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	ctors := []struct {
		typ, expr, want string
	}{
		{"Tagged[int]", `MkTag("x")`, "MkTag[int]{}.Apply("},
		{"Half[int, string]", `MkHalf(1)`, "MkHalf[int, string]{}.Apply("},
		{"Half[int, string]", `MkHalf[int](1)`, "MkHalf[int, string]{}.Apply("},
		{"Half[int64, string]", `MkHalf(1)`, "MkHalf[int64, string]{}.Apply("},
		{"IntTagged", `MkTag("x")`, "MkTag[int]{}.Apply("},
		{"Either[string, int]", `Left("x")`, "std.Left[string, int]{}.Apply("},
	}
	for _, pos := range expectedTypePositions {
		for _, c := range ctors {
			t.Run(pos.name+"/"+c.typ+"/"+c.expr, func(t *testing.T) {
				got, err := trans.Transpile(applyExpectedDecls+pos.src(c.typ, c.expr)+"\n", "")
				require.NoError(t, err)
				assert.Contains(t, got, c.want)
			})
		}
	}
}

// TestApplyExpectedTypeOnlyForTheConstruction pins that the enclosing result
// type reaches only the construction that is the result value. It used to
// reach every companion Apply construction in the body, so a helper bound to
// an unannotated val became a Tagged[int] nobody wrote.
func TestApplyExpectedTypeOnlyForTheConstruction(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	tests := []struct {
		name, input string
		want        []string
	}{
		{
			name:  "unannotated val in a function returning the type Apply returns",
			input: "func f() Tagged[int] {\n    val h = MkTag(\"helper\")\n    Println(h.Name)\n    MkTag(\"result\")\n}",
			want: []string{
				"cannot infer type argument T of MkTag from its arguments or the expected type",
				"annotate the binding (e.g. `val x Tagged[int] = MkTag(...)`) or write it explicitly (`MkTag[int](...)`)",
			},
		},
		{
			name:  "nested in the result value",
			input: "func f() Tagged[int] = Tagged[int](MkTag(\"x\").Name)",
			want:  []string{"cannot infer type argument T of MkTag"},
		},
		{
			name:  "method called on the construction",
			input: "func (t Tagged[T]) Same() Tagged[T] = t\nfunc f() Tagged[int] = MkTag(\"x\").Same()",
			want:  []string{"cannot infer type argument T of MkTag"},
		},
		{
			name:  "partial: the hint keeps the type argument the arguments fix",
			input: "func f() int {\n    val h = MkHalf(1)\n    h.First\n}",
			want: []string{
				"cannot infer type argument B of MkHalf",
				"(e.g. `val x Half[int, int] = MkHalf(...)`) or write it explicitly (`MkHalf[int, int](...)`)",
			},
		},
		{
			name:  "partial written type arguments: the hint keeps them",
			input: "func f() int {\n    val h = MkHalf[int64](1)\n    1\n}",
			want:  []string{"cannot infer type argument B of MkHalf", "(`MkHalf[int64, int](...)`)"},
		},
		{
			name:  "a sealed variant bound to an unannotated val",
			input: "func f() Either[string, int] {\n    val l = Left(\"x\")\n    l\n}",
			want: []string{
				"GALA-E0018",
				"annotate the binding (e.g. `val x Either[string, int] = Left(...)`) or pass type args explicitly (`Left[string, int](...)`)",
			},
		},
		{
			name:  "result of another type",
			input: "func f() Half[int, string] = MkTag(\"x\")",
			want:  []string{"cannot infer type argument T of MkTag"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(applyExpectedDecls+tt.input+"\n", "")
			require.Error(t, err)
			for _, want := range tt.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

// TestUninferredTypeArgHints pins the hint of every "cannot infer type
// argument" diagnostic a construction with written type arguments gets: the
// written ones kept, the open ones a placeholder, never a type parameter name.
func TestUninferredTypeArgHints(t *testing.T) {
	const decls = `package main

sealed type Res[T any, E any] {
    case Ok(V T)
    case Err(Msg E)
}

type Mk[A any, B any] struct {}
func (m Mk[A, B]) Apply(a A) int = 1

`
	tests := []struct {
		name, input, want, notWant string
	}{
		{
			name:    "companion Apply whose value carries no type parameter",
			input:   "func main() { Println(Mk[int](2)) }",
			want:    "cannot infer type argument B of Mk from its arguments or the expected type; write it explicitly (`Mk[int, int](...)`)",
			notWant: "annotate",
		},
		{
			name:  "sealed variant, positional, partial type arguments",
			input: "func main() { Println(Ok[int](1)) }",
			want:  "annotate the binding (e.g. `val x Res[int, int] = Ok(...)`) or pass type args explicitly (`Ok[int, int](...)`)",
		},
		{
			name:  "sealed variant, named, partial type arguments",
			input: "func main() { Println(Ok[string](V = \"s\")) }",
			want:  "annotate the binding (e.g. `val x Res[string, int] = Ok(...)`) or pass type args explicitly (`Ok[string, int](...)`)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newAliasExpectedTranspiler().Transpile(decls+tt.input+"\n", "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.NotContains(t, err.Error(), "[A, B]")
			assert.NotContains(t, err.Error(), "[T, E]")
			if tt.notWant != "" {
				assert.NotContains(t, err.Error(), tt.notWant)
			}
		})
	}
}
