package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newConcurrencyTranspiler builds a full transpiler wired to the std search path
// so the Sendable boundary marker resolves and the collection packages are
// available for the capture-safety cases.
func newConcurrencyTranspiler() *transpiler.GalaToGoTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
}

// TestSendableCaptureErrors covers PR3 enforcement: a closure (explicit lambda
// or by-name thunk) passed to a `Sendable[F]` boundary may only capture `val`
// bindings of Shareable types. Each negative case must fail with GALA-E0037 and
// name the offending capture; each positive case must transpile cleanly.
//
// The boundary here is a locally-declared function `func run(body Sendable[...])`
// — proving the check keys off the marker type, not off std function names.
func TestSendableCaptureErrors(t *testing.T) {
	trans := newConcurrencyTranspiler()

	type errCase struct {
		name           string
		input          string
		expectContains string // substring the message must contain (beyond the code)
		expectCapture  string // the identifier the caret must point at
	}

	cases := []errCase{
		{
			name: "var capture is a reassignment race",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    var counter = 0
    Println(run(() => counter + 1))
}`,
			expectContains: "reassignable var",
			expectCapture:  "counter",
		},
		{
			name: "var capture via thunk sugar",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    var counter = 0
    Println(run(counter + 1))
}`,
			expectContains: "reassignable var",
			expectCapture:  "counter",
		},
		{
			name: "collection_mutable capture is a mutable-pointee race",
			input: `package main

import . "martianoff/gala/collection_mutable"

func run(body Sendable[func() int]) int = body()

func main() {
    val buffer = ArrayOf(1, 2, 3)
    Println(run(() => buffer.Size()))
}`,
			expectContains: "not safe to share",
			expectCapture:  "buffer",
		},
		{
			name: "struct with a var field is not shareable",
			input: `package main

struct Counter(var n int)

func run(body Sendable[func() int]) int = body()

func main() {
    val c = Counter(0)
    Println(run(() => c.n))
}`,
			expectContains: "not safe to share",
			expectCapture:  "c",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "concurrency_check_test.gala")
			require.Error(t, err, "expected the boundary capture to be rejected")
			msg := err.Error()
			assert.Contains(t, msg, string(galaerr.CodeUnshareableCapture),
				"error must carry the stable code GALA-E0037")
			assert.Contains(t, msg, tc.expectContains,
				"error must explain WHY the capture is unsafe")
			assert.Contains(t, msg, tc.expectCapture,
				"error must name the offending capture")

			// The caret must point exactly at the offending capture identifier:
			// read the source at the reported (line, column) and confirm it
			// begins with that identifier. Column is 0-based (ANTLR convention).
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr, "expected a coded SemanticError")
			assert.Equal(t, galaerr.CodeUnshareableCapture, semErr.Code)
			srcLines := strings.Split(tc.input, "\n")
			require.GreaterOrEqual(t, semErr.Line, 1)
			require.LessOrEqual(t, semErr.Line, len(srcLines))
			lineText := srcLines[semErr.Line-1]
			require.LessOrEqual(t, semErr.Column, len(lineText))
			assert.True(t, strings.HasPrefix(lineText[semErr.Column:], tc.expectCapture),
				"caret at %d:%d should point at %q, got %q",
				semErr.Line, semErr.Column, tc.expectCapture, lineText[semErr.Column:])
		})
	}
}

// TestSendableCaptureAllowed is the negative half: safe captures — immutable
// collections, strings, ints, and top-level symbols — must transpile cleanly.
func TestSendableCaptureAllowed(t *testing.T) {
	trans := newConcurrencyTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{
			name: "immutable-int val capture",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    val base = 41
    Println(run(() => base + 1))
}`,
		},
		{
			name: "immutable-collection capture",
			input: `package main

import . "martianoff/gala/collection_immutable"

func run(body Sendable[func() int]) int = body()

func main() {
    val xs = ArrayOf(1, 2, 3)
    Println(run(() => xs.Size()))
}`,
		},
		{
			name: "thunk sugar with immutable capture",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    val label = "hi"
    Println(run(label.Size()))
}`,
		},
		{
			name: "capturing a top-level function is not a capture race",
			input: `package main

func helper() int = 7

func run(body Sendable[func() int]) int = body()

func main() {
    Println(run(() => helper()))
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "concurrency_check_ok.gala")
			require.NoError(t, err, "safe captures must transpile cleanly")
			assert.NotEmpty(t, out)
			// The transparent marker must never leak into generated Go.
			assert.NotContains(t, out, "Sendable", "Sendable must not appear in generated Go")
		})
	}
}
