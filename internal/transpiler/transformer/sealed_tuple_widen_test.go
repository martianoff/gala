package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// TestSealedTupleArmWidening verifies that match arm result type unification
// widens a sealed-variant CASE to its declared sealed PARENT when a sibling
// arm produces the parent type — including when the case appears inside a
// tuple slot. This mirrors the original failure shape:
//
//	func dispatch(...) Tuple[A, Cmd[T]] = ... match {
//	    case Failure(e) => (a, MsgCmd[T](Msg = ...))   // case in slot 2
//	    case Success(s) => (a, otherFnReturningCmd())  // parent in slot 2
//	}
//
// Before the fix, match arm unification rejected this with "type mismatch
// in match expression: case Failure returns Tuple[A, MsgCmd[T]] but case
// Success returns Tuple[A, Cmd[T]]" even though every value of MsgCmd[T]
// is a value of Cmd[T] (its sealed parent).
func TestSealedTupleArmWidening(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    []string
		notExpected []string
	}{
		{
			// Direct case ↔ parent in a tuple slot. The Failure arm's tuple
			// slot 2 is bound to a value typed as the case (via a function
			// declared to return the case); the Success arm's slot 2 is the
			// parent. The function's declared return is the parent, so both
			// arms must widen against it.
			name: "tuple slot with case-vs-parent widens to parent",
			input: `package main

import "errors"

sealed type AppMsg {
    case Tick()
    case Fail(Reason string)
}

sealed type Cmd[T any] {
    case NoCmd()
    case MsgCmd(Msg T)
}

struct AppModel(Counter int)

func parentCmd(s string) Cmd[AppMsg] = NoCmd[AppMsg]()

func dispatch(n int) Tuple[AppModel, Cmd[AppMsg]] {
    return (if (n == 0) Failure[string](errors.New("boom")) else Success("ok")) match {
        case Failure(e) => {
            val cmd = MsgCmd[AppMsg](Msg = Fail(Reason = e.Error()))
            return (AppModel(Counter = 1), cmd)
        }
        case Success(s) => {
            return (AppModel(Counter = 2), parentCmd(s))
        }
    }
}

func main() {
    val r = dispatch(0)
    Println(r.V1.Counter)
}`,
			expected: []string{
				// IIFE return type widens to the parent
				"std.Tuple[AppModel, Cmd[AppMsg]]",
			},
			notExpected: []string{
				// Without widening the failure arm tuple type would carry the
				// case in slot 2. The fix must produce Cmd[AppMsg] in every
				// tuple-slot 2 site so the Go literal types agree.
				"std.Tuple[AppModel, MsgCmd[AppMsg]]{",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			tr := transformer.NewGalaASTTransformer()
			g := generator.NewGoCodeGenerator()
			trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			for _, exp := range tt.expected {
				assert.True(t, strings.Contains(got, exp),
					"Output missing %q\nGot:\n%s", exp, got)
			}
			for _, ne := range tt.notExpected {
				assert.False(t, strings.Contains(got, ne),
					"Output should NOT contain %q\nGot:\n%s", ne, got)
			}
		})
	}
}
