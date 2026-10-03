package transformer_test

import (
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// TestGenericAliasWrongArity: a generic alias spelled with the wrong number of
// type arguments names no instance of its target, so a method call on a value
// of it cannot be judged. It used to be reported as a missing method
// (GALA-E0044, "Res declares no methods"); the type itself is what is wrong,
// and the Go compiler reports it ("too many type arguments for type Res"), so
// the transpile succeeds. Its output is deliberately invalid Go, which is why
// this file stays outside the generated-Go oracle.
func TestGenericAliasWrongArity(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := transpiler.NewGalaToGoTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

	for _, call := range []string{"r.IsSuccess()", "r.GetOrElse(0)"} {
		t.Run(call, func(t *testing.T) {
			_, err := trans.Transpile(`package main

type Res[T any] Try[T]

func main() {
    val r Res[int, string] = Success(1)
    Println(`+call+`)
}
`, "")
			require.NoError(t, err)
		})
	}
}
