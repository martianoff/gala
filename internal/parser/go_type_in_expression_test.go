package parser

import (
	"errors"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// firstSemanticError returns the single coded diagnostic in err, requiring that
// exactly one error was reported. The count matters: ANTLR recovers from the
// failure by inserting the brace it wanted and re-reports the rest of the file
// against the same composite literal, so a second error here means the cascade
// suppression regressed and the user is back to reading invented brace errors.
func firstSemanticError(t *testing.T, err error) *galaerr.SemanticError {
	t.Helper()
	require.Error(t, err)

	var multi *galaerr.MultiError
	require.True(t, errors.As(err, &multi), "expected a MultiError, got %T", err)
	require.Len(t, multi.Errors, 1, "expected exactly one diagnostic, got: %v", multi.Errors)

	var se *galaerr.SemanticError
	require.True(t, errors.As(multi.Errors[0], &se), "expected a SemanticError, got %v", multi.Errors[0])
	return se
}

// TestGoTypeInExpressionDiagnostic pins GALA-E0040: a Go slice or map type
// written in expression position is rejected with a coded diagnostic that names
// the offending type, points at it, and states the GALA spelling.
//
// The rejection itself is not new — every input here was already a compile
// error. What is asserted is the message, which used to be ANTLR's raw
// "missing '{' at '('": no code, no named type, and a position on the token
// AFTER the offending one.
func TestGoTypeInExpressionDiagnostic(t *testing.T) {
	p := NewAntlrGalaParser()

	tests := []struct {
		name string
		// input is a whole source file so the reported line/column can be
		// asserted against real coordinates rather than an offset into a
		// fragment.
		input      string
		wantMsg    string
		wantLine   int
		wantColumn int // 0-based, as stored
		wantEndCol int
		wantHint   string
	}{
		{
			name: "slice type as an explicit type argument",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val m = EmptyHashMap[string, []byte]()
    Println(m.Size())
}`,
			wantMsg:    "Go slice type []byte is not allowed in an expression",
			wantLine:   6,
			wantColumn: 33,
			wantEndCol: 39,
			wantHint:   "use Array[byte], or string for text, instead; Go slice and map types are an interop surface, allowed only where a type is expected: a function signature, a struct field, a val/var annotation, or a type alias",
		},
		{
			name: "non-byte slice type as an explicit type argument",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val m = EmptyHashMap[string, []int]()
    Println(m.Size())
}`,
			wantMsg:    "Go slice type []int is not allowed in an expression",
			wantLine:   6,
			wantColumn: 33,
			wantEndCol: 38,
			wantHint:   "use Array[int] instead; Go slice and map types are an interop surface, allowed only where a type is expected: a function signature, a struct field, a val/var annotation, or a type alias",
		},
		{
			name: "map type as an explicit type argument",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val m = EmptyHashMap[string, map[string]int]()
    Println(m.Size())
}`,
			wantMsg:    "Go map type map[string]int is not allowed in an expression",
			wantLine:   6,
			wantColumn: 33,
			wantEndCol: 47,
			wantHint:   "use HashMap[string, int] instead; Go slice and map types are an interop surface, allowed only where a type is expected: a function signature, a struct field, a val/var annotation, or a type alias",
		},
		{
			name: "slice type as an explicit type argument on a method call",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val a = ArrayOf(1, 2, 3)
    val b = a.Map[[]byte]((x) => nil)
    Println(b.Size())
}`,
			wantMsg:    "Go slice type []byte is not allowed in an expression",
			wantLine:   7,
			wantColumn: 18,
			wantEndCol: 24,
			wantHint:   "use Array[byte], or string for text, instead; Go slice and map types are an interop surface, allowed only where a type is expected: a function signature, a struct field, a val/var annotation, or a type alias",
		},
		{
			name: "slice type used as a conversion",
			input: `package main

func main() {
    val b = []byte("hi")
    Println(b)
}`,
			wantMsg:    "Go slice type []byte is not allowed in an expression",
			wantLine:   4,
			wantColumn: 12,
			wantEndCol: 18,
			wantHint:   "use Array[byte], or string for text, instead; Go slice and map types are an interop surface, allowed only where a type is expected: a function signature, a struct field, a val/var annotation, or a type alias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := p.Parse(tt.input)
			se := firstSemanticError(t, err)

			assert.Equal(t, galaerr.CodeGoTypeInExpression, se.Code)
			assert.Equal(t, tt.wantMsg, se.Msg)
			assert.Equal(t, tt.wantHint, se.Hint)
			assert.Equal(t, tt.wantLine, se.Line)
			assert.Equal(t, tt.wantColumn, se.Column)
			assert.Equal(t, tt.wantEndCol, se.EndColumn, "caret should span the offending type exactly")
		})
	}
}

// TestGoTypeInExpressionLeavesLegalPositionsAlone is the accept half of the
// accept/reject set: every position where a Go slice or map type is legitimate
// must still parse. E0040 lives in the error listener, so a mis-scoped gate
// would show up here as a parse error on code that has always been valid.
func TestGoTypeInExpressionLeavesLegalPositionsAlone(t *testing.T) {
	p := NewAntlrGalaParser()

	tests := []struct {
		name  string
		input string
	}{
		{
			name: "function parameter and return type",
			input: `package main

func total(xs []int) []int = xs`,
		},
		{
			name: "struct field",
			input: `package main

type Box struct {
    data []byte
    seen map[string]int
}`,
		},
		{
			name: "val and var annotations",
			input: `package main

func main() {
    val b []byte = nil
    var m map[string]int = nil
    Println(b, m)
}`,
		},
		{
			name: "type alias",
			input: `package main

type Bytes []byte

type EntryMap map[string]int`,
		},
		{
			name: "lambda parameter annotation",
			input: `package main

func main() {
    val f = (xs []int) => xs
    Println(f)
}`,
		},
		{
			name: "type argument of a generic type in a signature",
			input: `package main

import . "martianoff/gala/collection_immutable"

func take(m HashMap[string, []byte]) HashMap[string, []byte] = m`,
		},
		{
			name: "type argument of a generic type in a struct field",
			input: `package main

import . "martianoff/gala/collection_immutable"

type Wrap struct {
    m HashMap[string, []byte]
}`,
		},
		{
			name: "slice inside a func type",
			input: `package main

func apply(f func([]byte) string, b []byte) string = f(b)`,
		},
		{
			name: "method parameter and interface method result",
			input: `package main

type Holder struct {}

func (h Holder) take(xs []int) map[string]int = nil

type Reader interface {
    Read() []byte
}`,
		},
		{
			name: "variadic parameter",
			input: `package main

func total(xs ...int) int = 0`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := p.Parse(tt.input)
			assert.NoError(t, err)
		})
	}
}

// TestGoTypeInExpressionDoesNotSwallowOtherSyntaxErrors is the reject half:
// unrelated malformed input must keep producing its own syntax error, uncoded,
// rather than being relabelled as a Go type in expression position.
func TestGoTypeInExpressionDoesNotSwallowOtherSyntaxErrors(t *testing.T) {
	p := NewAntlrGalaParser()

	tests := []struct {
		name  string
		input string
	}{
		{
			name: "unclosed call argument list",
			input: `package main

func main() {
    Println("ok"
}`,
		},
		{
			name: "stray closing brace at top level",
			input: `package main

func main() {
    Println("ok")
}
}`,
		},
		{
			name: "empty explicit type argument",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val m = EmptyHashMap[string, ]()
    Println("ok")
}`,
		},
		{
			name: "slice literal with an unterminated element list",
			input: `package main

func main() {
    val xs = []int{1, 2,
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := p.Parse(tt.input)
			require.Error(t, err)

			var multi *galaerr.MultiError
			require.True(t, errors.As(err, &multi))
			for _, sub := range multi.Errors {
				var se *galaerr.SemanticError
				if errors.As(sub, &se) {
					assert.NotEqual(t, galaerr.CodeGoTypeInExpression, se.Code,
						"unrelated syntax error was relabelled as GALA-E0040: %v", sub)
				}
			}
		})
	}
}

// TestSliceLiteralStillReachesTheAnalyzer guards the boundary between E0040 and
// GALA-E0007. A well-formed slice literal PARSES — it is rejected later, by the
// analyzer, with the literal-specific code. E0040 must not intercept it, or the
// user gets a message about type positions for a construct whose real problem
// is the literal itself.
func TestSliceLiteralStillReachesTheAnalyzer(t *testing.T) {
	p := NewAntlrGalaParser()

	for _, src := range []string{
		`package main

func main() {
    val xs = []int{1, 2, 3}
    Println(xs)
}`,
		`package main

func main() {
    val m = map[string]int{}
    Println(m)
}`,
	} {
		_, _, err := p.Parse(src)
		assert.NoError(t, err, "literal should parse; rejection belongs to the analyzer")
	}
}
