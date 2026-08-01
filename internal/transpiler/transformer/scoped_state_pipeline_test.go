package transformer_test

import (
	"testing"

	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// TestNoScopedStateLeaksAfterTransform is the leak detector running against
// the real pipeline.
//
// Scoped transformer state — the expected type of the lambda being lowered,
// the enclosing match's subject type, whether the current match is in
// statement position — is saved and restored by whoever sets it. Once
// Transform returns, all of it must be back at its zero value.
//
// The error cases matter more than the success cases. A save/restore pair
// written as a plain assignment after the body transform (rather than a
// defer) skips its restore whenever the body returns an error, and every one
// of these inputs fails somewhere inside a lambda or match body.
//
// None of these inputs is currently expected to show residue, and that is the
// point: the invariant is asserted directly instead of resting on facts that
// happen to hold today. Right now a leak in one frame is masked by a correct
// defer in its caller, and a transformer is constructed per file and discarded
// on error — so a missing restore is invisible. Both of those are properties
// of the current callers, not of the code that leaks. This test fails when a
// frame stops cleaning up after itself, without waiting for a caller to change
// and turn it into a mis-inferred type.
func TestNoScopedStateLeaksAfterTransform(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		expectErr bool
	}{
		{
			name: "curried lambda in a typed slot",
			input: `package main

func main() {
    val add func(int) func(int) int = (a) => (b) => a + b
    Println(add(1)(2))
}`,
		},
		{
			name: "error inside a curried lambda body",
			input: `package main

func main() {
    val add func(int) func(int) int = (a) => (b) => a + undefinedSymbolHere
    Println(add(1)(2))
}`,
			expectErr: true,
		},
		{
			name: "error inside a lambda in a typed slot",
			input: `package main

func main() {
    val f func(int) int = (x) => x + noSuchValue
    Println(f(1))
}`,
			expectErr: true,
		},
		{
			// A curried lambda whose block body fails before the trailing
			// lambda consumes the expectation — the narrowest shape that
			// reaches transformLambdaWithExpectedType's inner-expectation
			// block and then takes an error return out of it.
			name: "curried lambda whose block body fails before the inner lambda",
			input: `package main

sealed type Shape {
    case Rect(W int, H int)
    case Circle(R int)
}

func main() {
    val f func(int) func(int) int = (a) => {
        val s Shape = Rect(1, 2)
        val n = s match {
            case Rect(w, h) => w * h
        }
        (b) => a + b + n
    }
    Println(f(1)(2))
}`,
			expectErr: true,
		},
		{
			name: "match in statement position with a lambda arm",
			input: `package main

sealed type Shape {
    case Rect(W int, H int)
    case Circle(R int)
}

func main() {
    val s Shape = Rect(2, 3)
    s match {
        case Rect(w, h) => Println(w * h)
        case Circle(r) => Println(r)
    }
}`,
		},
		{
			name: "error inside a match arm body",
			input: `package main

sealed type Shape {
    case Rect(W int, H int)
    case Circle(R int)
}

func main() {
    val s Shape = Rect(2, 3)
    s match {
        case Rect(w, h) => Println(w * missingName)
        case Circle(r) => Println(r)
    }
}`,
			expectErr: true,
		},
		{
			name: "non-exhaustive match rejected mid-transform",
			input: `package main

sealed type Shape {
    case Rect(W int, H int)
    case Circle(R int)
}

func area(s Shape) int = s match {
    case Rect(w, h) => w * h
}

func main() {
    Println(area(Rect(1, 2)))
}`,
			expectErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trans, tr := newTranspilerWithTransformer()
			_, err := trans.Transpile(tc.input, "scoped_state_test.gala")
			if tc.expectErr {
				require.Error(t, err, "expected this input to fail")
			} else {
				require.NoError(t, err)
			}

			require.Empty(t, transformer.ScopedStateResidue(tr),
				"scoped transformer state leaked after Transform (err=%v). "+
					"Some save/restore pair did not restore on this path — check that it "+
					"uses defer rather than a trailing assignment. See scoped_state.go.", err)
		})
	}
}
