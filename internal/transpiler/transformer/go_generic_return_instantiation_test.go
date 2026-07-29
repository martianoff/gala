package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGoGenericReturnInstantiatedInLambda guards the call-site instantiation of a
// GO generic function's return type.
//
// A Go generic signature records its return exactly as declared — `slices.Clone`
// returns `S`, `slices.Concat` returns `S`, `maps.Clone` returns `M`. That is fine
// for a direct call, where nothing forces the type into the output, but a lambda
// has to write its result clause down. Without instantiation the transpiler copied
// the declared type through, emitting `func() S` / `func() M`: Go then rejected the
// generated file with `undefined: S`, naming a type parameter the user never wrote.
//
// The contrast case that isolates this to the Go path is that a GALA generic callee
// in the identical shape has always substituted correctly (its FunctionMetadata
// route runs inferFuncTypeParamsFromArgs); the Go route had no equivalent step.
//
// Hermetic: `slices` and `maps` are Go stdlib, resolved through goTypeInfo / the Go
// SDK with no GALA package wiring.
func TestGoGenericReturnInstantiatedInLambda(t *testing.T) {
	cases := []struct {
		name string
		// input must bind a lambda whose body is a single Go generic call.
		input string
		// want is the instantiated result clause the lambda must carry.
		want string
		// leaked are the callee's own type-parameter names, which must not
		// appear as the lambda's result type.
		leaked []string
	}{
		{
			name:   "slice-returning Go generic in a bare lambda",
			input:  "package main\n\nimport \"slices\"\n\nfunc f(xs []int) int {\n    val g = () => slices.Clone(xs)\n    return g().Size()\n}",
			want:   "func() []int",
			leaked: []string{"func() S"},
		},
		{
			name:   "Go generic routed through a user generic combinator",
			input:  "package main\n\nimport \"slices\"\n\nfunc apply[A any](body func() A) A = body()\n\nfunc f(xs []string) int = apply(() => slices.Clone(xs)).Size()",
			want:   "func() []string",
			leaked: []string{"func() S"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			trans := newForbiddenBuiltinTranspiler()
			out, err := trans.Transpile(tc.input, "")
			require.NoError(t, err, "a lambda over a Go generic call must transpile")
			require.Contains(t, out, tc.want,
				"the lambda's result clause should carry the instantiated return type:\n%s", out)
			for _, bad := range tc.leaked {
				require.False(t, strings.Contains(out, bad),
					"the Go callee's own type parameter must not surface as the lambda's result type (%q):\n%s", bad, out)
			}
		})
	}
}

// Two routes are deliberately not covered here and are covered end to end by
// examples/go_generic_lambda_return.gala and examples/multifile_lib_regress
// instead:
//
//   - the `map[K]V` return shape. Go's only map-returning stdlib generic is
//     `maps.Clone`, and the `maps` package yields no goTypeInfo in this sandbox at
//     all (even a direct `maps.Clone(m).Size()` fails to lower), so a test built on
//     it would assert nothing about instantiation.
//   - the explicit-type-argument route (`go_interop.MapEmpty[string, int]()`),
//     where the call takes no arguments and the type-argument list is the only
//     binding source. No zero-argument Go stdlib generic is reachable here.
