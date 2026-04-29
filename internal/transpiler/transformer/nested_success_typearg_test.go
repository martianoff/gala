package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
)

// TestNestedSuccessSealedTypeArg verifies that nested sealed-case patterns
// inside Success(...) over Try[Msg] preserve the Msg type argument when the
// match's scrutinee is a lambda parameter whose type is implicit (driven by
// the enclosing function's `func(Try[Msg]) string` return type).
//
// Without expected-param propagation through the `return` statement, the
// lambda's untyped parameter widened to any, the matched type degraded to
// Try[any], the IIFE wrapper became `func(obj Try[any])`, and the inner
// constructor's Unapply call (e.g., ChunkArrived{}.Unapply) ended up
// receiving a value of type any instead of Msg — Go rejected with:
//
//	cannot use Try[Msg] as Try[any] value in argument to func(obj Try[any])
//	cannot use _tmp_NN (variable of interface type any) as Msg value in
//	argument to ChunkArrived{}.Unapply: need type assertion
//
// All three forms exercise nested Success(Case(...)) patterns; the
// block-bodied function form (`func f() Lambda { return (t) => ... }`) is
// the one that triggered the bug, while the expression-bodied form was
// already working as a baseline check.
func TestNestedSuccessSealedTypeArg(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name  string
		input string
	}{
		{
			name: "block-bodied function returning lambda with nested Success(Case)",
			input: `package main

sealed type Msg {
    case ChunkArrived(Index int, Line string)
    case SessionFailed(Index int, Err string)
    case Other
}

func MakeHandler() func(Try[Msg]) string {
    return (t) => t match {
        case Failure(err) => s"fail: ${err.Error()}"
        case Success(ChunkArrived(_, line)) => s"chunk: $line"
        case Success(SessionFailed(_, e))   => s"sessfail: $e"
        case _ => "other"
    }
}

func main() {}
`,
		},
		{
			name: "expression-bodied function returning lambda with nested Success(Case)",
			input: `package main

sealed type Msg {
    case ChunkArrived(Index int, Line string)
    case SessionFailed(Index int, Err string)
    case Other
}

func MakeHandler() func(Try[Msg]) string =
    (t) => t match {
        case Failure(err) => s"fail: ${err.Error()}"
        case Success(ChunkArrived(_, line)) => s"chunk: $line"
        case Success(SessionFailed(_, e))   => s"sessfail: $e"
        case _ => "other"
    }

func main() {}
`,
		},
		{
			name: "direct match on Try[Msg] argument (baseline — was already working)",
			input: `package main

sealed type Msg {
    case ChunkArrived(Index int, Line string)
    case SessionFailed(Index int, Err string)
    case Other
}

func DoMatch(t Try[Msg]) string {
    return t match {
        case Failure(err) => s"fail: ${err.Error()}"
        case Success(ChunkArrived(_, line)) => s"chunk: $line"
        case Success(SessionFailed(_, e))   => s"sessfail: $e"
        case _ => "other"
    }
}

func main() {}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err, "transpilation should succeed")
			gen := stripGeneratedHeader(got)

			// Match-IIFE param must use Try[Msg], never Try[any].
			assert.False(t, strings.Contains(gen, "func(obj std.Try[any])") ||
				strings.Contains(gen, "func(obj Try[any])"),
				"match-IIFE parameter must NOT widen to Try[any]\n--- generated ---\n%s", gen)
			// Lambda surfaced from `return (t) => ...` must spell its
			// parameter as Try[Msg], not any.
			assert.False(t, strings.Contains(gen, "func(t any)") ||
				strings.Contains(gen, "func(t std.Try[any])"),
				"lambda parameter must NOT erase to any\n--- generated ---\n%s", gen)
		})
	}
}
