package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

func transpileTupleMatch(t *testing.T, src string) (string, error) {
	t.Helper()
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g).Transpile(src, "main.gala")
}

// TestTupleMatchIrrefutableArmIsExhaustive pins that a tuple pattern made only
// of wildcards, bindings and nested such tuples closes a match without a
// `case _` default, while a refutable or guarded tuple arm still requires one.
func TestTupleMatchIrrefutableArmIsExhaustive(t *testing.T) {
	const divmod = `package main

func divmod(a int, b int) Tuple3[int, int, string] {
    if (b == 0) {
        return (0, 0, "division by zero")
    }
    return (a / b, a % b, "ok")
}

`
	tests := []struct {
		name     string
		body     string
		wantCode galaerr.ErrorCode
	}{
		{
			name: "all-binding last arm (expression position)",
			body: `func main() {
    val result = divmod(10, 3) match {
        case (q, r, "ok") => s"$q remainder $r"
        case (_, _, err)  => s"Error: $err"
    }
    Println(result)
}`,
		},
		{
			name: "all-binding last arm (statement position)",
			body: `func main() {
    divmod(10, 0) match {
        case (_, _, "ok") => Println("fine")
        case (_, _, msg)  => Println(msg)
    }
}`,
		},
		{
			name: "nested irrefutable tuple",
			body: `func classify(p Tuple[int, Tuple[string, bool]]) string = p match {
    case (0, _)          => "zero"
    case (n, (label, _)) => s"$n $label"
}

func main() {
    Println(classify((1, ("a", true))))
}`,
		},
		{
			name: "refutable last arm still needs a default",
			body: `func main() {
    val result = divmod(10, 3) match {
        case (q, r, "ok") => s"$q remainder $r"
        case (_, _, "x")  => "x"
    }
    Println(result)
}`,
			wantCode: galaerr.CodeMissingDefault,
		},
		{
			name: "guarded irrefutable arm still needs a default",
			body: `func main() {
    val result = divmod(10, 3) match {
        case (q, r, s) if q > 100 => s"$q $r $s"
    }
    Println(result)
}`,
			wantCode: galaerr.CodeMissingDefault,
		},
		{
			name: "guarded plain binding is not a default",
			body: `func main() {
    val n = 5
    val result = n match {
        case 1              => "one"
        case m if m > 100   => s"big $m"
    }
    Println(result)
}`,
			wantCode: galaerr.CodeMissingDefault,
		},
		{
			// A lowercase name that is a zero-field variant of the element's
			// sealed type is a variant test, not a binding.
			name: "lowercase sealed variant element still needs a default",
			body: `sealed type St {
    case idle()
    case busy(N int)
}

func show(p Tuple[St, int]) string = p match {
    case (busy(n), 0) => s"busy $n"
    case (idle, x)    => s"idle $x"
}

func main() {
    Println(show((idle(), 1)))
}`,
			wantCode: galaerr.CodeMissingDefault,
		},
		{
			// A tuple pattern shorter than its subject reads only the first
			// elements; it must not close the match.
			name: "tuple pattern shorter than the subject still needs a default",
			body: `func main() {
    val result = divmod(10, 3) match {
        case (q, r, "ok") => s"$q remainder $r"
        case (q, r)       => s"$q $r"
    }
    Println(result)
}`,
			wantCode: galaerr.CodeMissingDefault,
		},
		{
			name: "nested refutable tuple still needs a default",
			body: `func classify(p Tuple[int, Tuple[string, bool]]) string = p match {
    case (n, (label, true)) => s"$n $label"
}

func main() {
    Println(classify((1, ("a", true))))
}`,
			wantCode: galaerr.CodeMissingDefault,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := transpileTupleMatch(t, divmod+tt.body)
			if tt.wantCode == "" {
				require.NoError(t, err)
				require.Contains(t, out, `panic("unreachable")`)
				return
			}
			require.Error(t, err)
			var se *galaerr.SemanticError
			require.ErrorAs(t, err, &se)
			require.Equal(t, tt.wantCode, se.Code)
		})
	}
}
