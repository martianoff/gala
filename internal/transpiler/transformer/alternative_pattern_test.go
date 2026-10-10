package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestAlternativePatternLowering pins the lowering of `p1 | p2`: the arm tests
// each alternative and ORs the results, never comparing with a bitwise OR of
// the values. The runtime behaviour is pinned by examples/alternative_patterns.
func TestAlternativePatternLowering(t *testing.T) {
	cases := []struct {
		name         string
		src          string
		wantContains []string
	}{
		{
			name: "integer literals",
			src: `package main

func small(n int) string = n match {
    case 1 | 2 => "one-or-two"
    case _     => "other"
}

func main() {
    Println(small(1))
}`,
			wantContains: []string{"if obj == 1 || obj == 2 {"},
		},
		{
			name: "string literals",
			src: `package main

func mode(m string) string = m match {
    case "debug" | "development" => "dev"
    case _                       => "other"
}

func main() {
    Println(mode("debug"))
}`,
			wantContains: []string{`if obj == "debug" || obj == "development" {`},
		},
		{
			name: "parenthesized alternatives are alternatives",
			src: `package main

func f(n int) string = n match {
    case (1 | 2) => "small"
    case _       => "other"
}

func main() {
    Println(f(1))
}`,
			wantContains: []string{"if obj == 1 || obj == 2 {"},
		},
		{
			name: "nested inside an extractor",
			src: `package main

func f(o Option[int]) string = o match {
    case Some(1 | 2) => "small"
    case _           => "other"
}

func main() {
    Println(f(Some(1)))
}`,
			wantContains: []string{"== 1 ||", "== 2)"},
		},
		{
			name: "guard applies to the whole alternative",
			src: `package main

func f(n int, ok bool) string = n match {
    case 1 | 2 if ok => "hit"
    case _           => "miss"
}

func main() {
    Println(f(1, true))
}`,
			wantContains: []string{"(obj == 1 || obj == 2) && ok"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, tc.src)
			require.NoError(t, err)
			for _, want := range tc.wantContains {
				require.Contains(t, out, want)
			}
			for _, bitwise := range []string{"1|2", "1 | 2"} {
				require.NotContains(t, out, bitwise, "`|` in a pattern must not lower to a bitwise OR")
			}
		})
	}
}

// TestAlternativePatternShortCircuits pins that alternatives are tried left to
// right: a later alternative's extractor runs only when no earlier one
// matched.
func TestAlternativePatternShortCircuits(t *testing.T) {
	src := `package main

sealed type Shape {
    case Circle(Radius int)
    case Rect(Width int, Height int)
}

func kind(s Shape) string = s match {
    case Circle(_) | Rect(_, _) => "any"
}

func main() {
    Println(kind(Circle(1)))
}`
	out, err := transpileBareVariant(t, src)
	require.NoError(t, err)
	circle := strings.Index(out, "Circle{}.Unapply(obj)")
	guard := strings.Index(out, "if !_tmp_")
	rect := strings.Index(out, "Rect{}.Unapply(obj)")
	require.True(t, circle >= 0 && guard > circle && rect > guard,
		"Rect's extractor must run only under the guard that Circle did not match:\n%s", out)
}

// TestAlternativePatternExhaustive pins that alternatives count toward the
// exhaustiveness of a sealed match, and that an uncovered variant is still
// reported.
func TestAlternativePatternExhaustive(t *testing.T) {
	const shape = `package main

sealed type Shape {
    case Circle(Radius int)
    case Rect(Width int, Height int)
    case Empty()
}
`
	cases := []struct {
		name    string
		arms    string
		wantErr string
	}{
		{
			name: "alternatives cover every variant",
			arms: `    case Circle(_) | Rect(_, _) => "solid"
    case Empty()                => "empty"`,
		},
		{
			name: "one alternative pattern covers every variant",
			arms: `    case Circle(_) | Rect(_, _) | Empty() => "any"`,
		},
		{
			name:    "a variant left out",
			arms:    `    case Circle(_) | Empty() => "round or empty"`,
			wantErr: "missing cases: Rect",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := shape + `
func kind(s Shape) string = s match {
` + tc.arms + `
}

func main() {
    Println(kind(Empty()))
}`
			_, err := transpileBareVariant(t, src)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeNonExhaustiveMatch))
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestAlternativePatternVariantArity pins that each alternative is checked
// against its variant's field count (GALA-E0004), like a whole pattern.
func TestAlternativePatternVariantArity(t *testing.T) {
	src := `package main

sealed type Shape {
    case Circle(Radius int)
    case Rect(Width int, Height int)
}

func kind(s Shape) string = s match {
    case Circle(_) | Rect(_) => "any"
}

func main() {
    Println(kind(Circle(1)))
}`
	_, err := transpileBareVariant(t, src)
	require.Error(t, err)
	require.Contains(t, err.Error(), string(galaerr.CodeVariantArityMismatch))
	require.Contains(t, err.Error(), `sealed variant "Rect" pattern binds 1 field(s) but declares 2`)
}

// TestAlternativePatternRejected pins GALA-E0071: an alternative that binds a
// name, and `|` mixed with an operator of the same precedence.
func TestAlternativePatternRejected(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		wantMsg string
	}{
		{
			name:    "alternative binds a name",
			pattern: "Some(n) | None()",
			wantMsg: "pattern alternative binds 'n'",
		},
		{
			name:    "nested alternative binds a name",
			pattern: "Some(n | 1)",
			wantMsg: "pattern alternative binds 'n'",
		},
		{
			name:    "mixed with +",
			pattern: "Some(1 + 1 | 3)",
			wantMsg: "cannot be mixed with '+'",
		},
		{
			name:    "wildcard alternative",
			pattern: "Some(1) | _",
			wantMsg: "a wildcard alternative matches everything",
		},
		{
			name:    "inside a comparison",
			pattern: "Some(1 | 2 > 1)",
			wantMsg: "cannot be used inside a comparison or boolean expression",
		},
		{
			name:    "mixed with ^",
			pattern: "Some(1 | 2 ^ 3)",
			wantMsg: "cannot be mixed with '^'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `package main

func f(o Option[int]) string = o match {
    case ` + tc.pattern + ` => "hit"
    case _ => "miss"
}

func main() {
    Println(f(Some(1)))
}`
			_, err := transpileBareVariant(t, src)
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeInvalidAlternativePattern))
			require.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}
