package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A FlatMap whose receiver is itself a FlatMap result previously over-arity'd
// the outer monomorphized call: the receiver's element type was taken as the
// inner call's full [U, T] type-arg list rather than its single result element
// type, emitting `Try_FlatMap[U2, U1, T1]` (3 args) where the generic helper
// `Try_FlatMap[T, U]` wants 2. Each level must carry exactly two type args.
func TestChainedFlatMapTypeArgs(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name        string
		input       string
		wantOuter   string
		wantInner   string
		notContains string
	}{
		{
			name: "Try.FlatMap after Try.FlatMap",
			input: `package main

func chain() Try[string] =
    Try(() => 1)
        .FlatMap[int]((a) => Try(() => a + 1))
        .FlatMap[string]((b) => Try(() => s"v$b"))
`,
			wantOuter:   "std.Try_FlatMap[string, int]",
			wantInner:   "std.Try_FlatMap[int, int]",
			notContains: "std.Try_FlatMap[string, int, int]",
		},
		{
			name: "Option.FlatMap after Option.FlatMap",
			input: `package main

func chain() Option[string] =
    Some(1)
        .FlatMap[int]((a) => Some(a + 1))
        .FlatMap[string]((b) => Some(s"o$b"))
`,
			wantOuter:   "std.Option_FlatMap[string, int]",
			wantInner:   "std.Option_FlatMap[int, int]",
			notContains: "std.Option_FlatMap[string, int, int]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			assert.Contains(t, got, tt.wantInner, "inner FlatMap must keep its 2 type args")
			assert.Contains(t, got, tt.wantOuter, "outer FlatMap must use [methodU, innerResultElem], not the inner's full type-arg list")
			assert.NotContains(t, got, tt.notContains, "outer FlatMap must not leak the inner receiver's type param")
			// Guard against any 3-arg FlatMap monomorphization sneaking back in.
			assert.False(t, strings.Contains(got, "_FlatMap[string, int, int]"),
				"no FlatMap call should carry 3 type arguments")
		})
	}
}
