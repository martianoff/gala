package transformer

import (
	"go/ast"
	"testing"

	"martianoff/gala/internal/transpiler"

	"github.com/stretchr/testify/require"
)

// TestAstTypeToTranspilerTypeRoundTripsGoMaps guards the AST-to-type conversion
// for Go map types.
//
// Go slices and Go maps are legal in exactly the same type positions — a struct
// field, a parameter, a val annotation, a return. Slices converted; maps fell
// through to NilType, so anything typed from a declared `map[K]V` lost its type
// with no diagnostic. A struct field read (`ix.entries`) was the visible case:
// the value came back untyped, which then defeated argument-driven inference at
// any generic call it was passed to.
func TestAstTypeToTranspilerTypeRoundTripsGoMaps(t *testing.T) {
	tr, ok := NewGalaASTTransformer().(*galaASTTransformer)
	require.True(t, ok)

	cases := []struct {
		name string
		expr ast.Expr
		want string
	}{
		{
			name: "map with basic key and value",
			expr: &ast.MapType{Key: ast.NewIdent("string"), Value: ast.NewIdent("int")},
			want: "map[string]int",
		},
		{
			name: "map whose value is a slice",
			expr: &ast.MapType{
				Key:   ast.NewIdent("string"),
				Value: &ast.ArrayType{Elt: ast.NewIdent("byte")},
			},
			want: "map[string][]byte",
		},
		{
			name: "map nested as a slice element",
			expr: &ast.ArrayType{
				Elt: &ast.MapType{Key: ast.NewIdent("string"), Value: ast.NewIdent("int")},
			},
			want: "[]map[string]int",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := tr.astTypeToTranspilerType(tc.expr)
			require.False(t, got.IsNil(), "a Go map type position must not convert to NilType")
			require.Equal(t, tc.want, got.String())
		})
	}
}

// TestInstantiateGoSignatureReturns guards the multi-value counterpart of the
// call-site instantiation of a Go generic's return type.
//
// A Go generic signature records its returns exactly as declared, so
// `func MapGet[K comparable, V any](m map[K]V, k K) (V, bool)` reports `V` for
// the first slot. Consumers that bind more than the first return — a
// destructuring `val v, ok = …` and the (T, error) auto-destructure wrapper —
// write each slot's type down, so passing the declared types through gives a
// name the type of a parameter that exists only inside the callee's signature.
// The symptom is deferred: the binding succeeds, uses that do not pin a type
// keep working, and the build breaks at the first use that does, reporting
// `undefined: V` at a line that is not the cause.
//
// Hermetic: the signatures are built directly, so nothing here depends on the
// Go SDK or on which stdlib generics happen to be reachable. Go's stdlib has no
// generic function whose multi-value return mentions a type parameter, so the
// end-to-end routes are covered by examples/go_generic_multireturn_destructure
// and examples/multifile_lib_regress instead.
func TestInstantiateGoSignatureReturns(t *testing.T) {
	// func MapGet[K comparable, V any](m map[K]V, k K) (V, bool)
	mapGet := &transpiler.GoFuncSignature{
		Params: []transpiler.GoParam{
			{Name: "m", Type: transpiler.MapType{
				Key:  transpiler.BasicType{Name: "K"},
				Elem: transpiler.BasicType{Name: "V"},
			}},
			{Name: "k", Type: transpiler.BasicType{Name: "K"}},
		},
		Returns:    []transpiler.Type{transpiler.BasicType{Name: "V"}, transpiler.BasicType{Name: "bool"}},
		TypeParams: []string{"K", "V"},
	}
	// func Load[T any](path string) (T, error)
	loadT := &transpiler.GoFuncSignature{
		Params:     []transpiler.GoParam{{Name: "path", Type: transpiler.BasicType{Name: "string"}}},
		Returns:    []transpiler.Type{transpiler.BasicType{Name: "T"}, transpiler.BasicType{Name: "error"}},
		TypeParams: []string{"T"},
	}
	// func Cut(s, sep string) (string, string, bool) — not generic.
	cut := &transpiler.GoFuncSignature{
		Params: []transpiler.GoParam{
			{Name: "s", Type: transpiler.BasicType{Name: "string"}},
			{Name: "sep", Type: transpiler.BasicType{Name: "string"}},
		},
		Returns: []transpiler.Type{
			transpiler.BasicType{Name: "string"},
			transpiler.BasicType{Name: "string"},
			transpiler.BasicType{Name: "bool"},
		},
	}

	cases := []struct {
		name     string
		sig      *transpiler.GoFuncSignature
		typeArgs []transpiler.Type
		want     []string
	}{
		{
			name:     "type params bound by explicit type arguments",
			sig:      mapGet,
			typeArgs: []transpiler.Type{transpiler.BasicType{Name: "string"}, transpiler.BasicType{Name: "int"}},
			want:     []string{"int", "bool"},
		},
		{
			name:     "slot returning a type param the call cannot determine is left alone",
			sig:      mapGet,
			typeArgs: nil,
			want:     []string{"V", "bool"},
		},
		{
			name:     "partial explicit prefix binds only its own positions",
			sig:      mapGet,
			typeArgs: []transpiler.Type{transpiler.BasicType{Name: "string"}},
			want:     []string{"V", "bool"},
		},
		{
			name:     "value slot of a (T, error) return is instantiated, error slot untouched",
			sig:      loadT,
			typeArgs: []transpiler.Type{transpiler.NamedType{Package: "bytes", Name: "Buffer"}},
			want:     []string{"bytes.Buffer", "error"},
		},
		{
			name:     "non-generic signature passes through unchanged",
			sig:      cut,
			typeArgs: nil,
			want:     []string{"string", "string", "bool"},
		},
	}

	tr, ok := NewGalaASTTransformer().(*galaASTTransformer)
	require.True(t, ok, "instantiation is exercised on the concrete transformer")

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			declared := make([]string, len(tc.sig.Returns))
			for i, ret := range tc.sig.Returns {
				declared[i] = ret.String()
			}

			got := tr.instantiateGoSignatureReturns(tc.sig, nil, tc.typeArgs, false)
			require.Len(t, got, len(tc.want))
			for i, want := range tc.want {
				require.NotNil(t, got[i], "return slot %d must stay populated", i)
				require.Equal(t, want, got[i].String(), "return slot %d", i)
			}

			// Instantiation must not mutate the signature — the same cached
			// *GoFuncSignature is handed to every call site, so writing the
			// first call's type arguments into it would leak to the rest.
			for i, want := range declared {
				require.Equal(t, want, tc.sig.Returns[i].String(),
					"declared return slot %d must survive instantiation", i)
			}
		})
	}
}
