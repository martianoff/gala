package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"
)

// A generic method lowers to a standalone function, a separate path from other
// method calls; it must bind named arguments by name and fill omitted defaults
// like the rest. examples/generic_method_named_args.gala runs the valid shapes
// end to end; this covers the lowering and the calls that must be rejected.
func TestGenericMethodNamedArguments(t *testing.T) {
	const decls = `package main

type Box struct {
    val n int
}

func (b Box) Fold[T any](initial T, f func(T, int) T) T = f(initial, b.n)

func (b Box) Pick[T any](first T, second T, useSecond bool = false) T = if (useSecond) second else first

`
	for _, tt := range []struct {
		name    string
		call    string
		want    string // in the generated Go, when the call is valid
		wantErr string // in the error, when it is not
	}{
		{name: "named arguments reordered", call: `b.Pick(second = "b", first = "a", useSecond = true)`, want: `Box_Pick(b.Get(), "a", "b", true)`},
		{name: "omitted default filled", call: `b.Pick("a", "b")`, want: `Box_Pick(b.Get(), "a", "b", false)`},
		{name: "lambda bound by name", call: `b.Fold(f = (acc, x) => acc + x, initial = 10)`, want: `func(acc int, x int) int`},
		{name: "unknown parameter", call: `b.Pick("a", "b", third = true)`, wantErr: `unknown parameter "third" in call to Pick`},
		{name: "missing required argument", call: `b.Pick(second = "b")`, wantErr: `missing required argument "first" (parameter 1) in call to Pick`},
		{name: "parameter given twice", call: `b.Pick("a", "b", first = "c")`, wantErr: `parameter "first" specified both positionally and by name in call to Pick`},
		{name: "too many arguments", call: `b.Pick("a", "b", true, "d", useSecond = false)`, wantErr: `too many arguments in call to Pick`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			src := decls + "func main() {\n    val b = Box(n = 3)\n    Println(" + tt.call + ")\n}\n"
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

			got, err := trans.Transpile(src, "")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("transpile: %v", err)
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("generated Go does not contain %q:\n%s", tt.want, got)
			}
		})
	}
}
