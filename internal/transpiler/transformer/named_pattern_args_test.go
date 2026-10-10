package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestNamedSubPatterns pins `Field = p` in a pattern: it matches p against
// the field of that name, wherever it is written, and a field left out
// matches anything. Read by position instead, `Rect(Height = h, Width = _)`
// bound Width to h. The runtime behaviour is pinned by
// examples/named_sub_patterns.
func TestNamedSubPatterns(t *testing.T) {
	const decls = `package main

sealed type Shape {
    case Circle(Radius int)
    case Rect(Width int, Height int)
}

struct Point(X int, Y int)

struct Even()

func (e Even) Unapply(n int) Option[int] = if (n % 2 == 0) Some(n / 2) else None[int]()
`
	cases := []struct {
		name    string
		fn      string
		wantErr string
	}{
		{
			name: "names in another order",
			fn:   "func f(s Shape) int = s match {\n    case Rect(Height = h, Width = _) => h\n    case _ => 0\n}",
		},
		{
			name: "fields left out",
			fn:   "func f(s Shape) int = s match {\n    case Circle(Radius = r) => r\n    case Rect(Height = h) => h\n}",
		},
		{
			name: "positional then named",
			fn:   "func f(s Shape) int = s match {\n    case Rect(1, Height = h) => h\n    case _ => 0\n}",
		},
		{
			name: "struct",
			fn:   "func f(p Point) int = p match {\n    case Point(Y = y) => y\n    case _ => 0\n}",
		},
		{
			name:    "unknown field",
			fn:      "func f(s Shape) int = s match {\n    case Rect(Depth = d) => d\n    case _ => 0\n}",
			wantErr: "'Rect' has no field 'Depth'",
		},
		{
			name:    "field matched twice",
			fn:      "func f(s Shape) int = s match {\n    case Rect(Height = a, Height = b) => a + b\n    case _ => 0\n}",
			wantErr: "field 'Height' of 'Rect' is matched twice",
		},
		{
			name:    "field given by position and by name",
			fn:      "func f(s Shape) int = s match {\n    case Rect(w, Width = v) => w + v\n    case _ => 0\n}",
			wantErr: "field 'Width' of 'Rect' is matched twice",
		},
		{
			name:    "positional after named",
			fn:      "func f(s Shape) int = s match {\n    case Rect(Height = h, 1) => h\n    case _ => 0\n}",
			wantErr: "a positional sub-pattern cannot follow a named one",
		},
		{
			name:    "too many positional",
			fn:      "func f(s Shape) int = s match {\n    case Rect(1, 2, 3, Height = h) => h\n    case _ => 0\n}",
			wantErr: "'Rect' has 2 fields, but this is sub-pattern 3",
		},
		{
			name:    "extractor with its own Unapply",
			fn:      "func f(n int) int = n match {\n    case Even(Half = h) => h\n    case _ => 0\n}",
			wantErr: "'Even' has no fields to match by name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + "\n" + tc.fn + "\n\nfunc main() {\n    Println(\"ok\")\n}"
			_, err := transpileBareVariant(t, src)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeInvalidNamedSubPattern))
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
