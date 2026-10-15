package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFmtShadowedByBinding pins the code GALA lowers to Go's fmt (Println,
// Print, s"..." interpolation, a sealed type's String) where a binding named
// fmt is in scope: it calls fmt under a reserved import name, which the binding
// does not shadow.
func TestFmtShadowedByBinding(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		contains []string
		absent   []string
	}{
		{
			name: "a local val named fmt",
			body: `struct F(x int)
func main() {
    val fmt = F(1)
    Print(fmt.x)
    Println(s"x=${fmt.x}")
}`,
			contains: []string{`_gala_fmt "fmt"`, "_gala_fmt.Print(fmt.Get().x.Get())", `_gala_fmt.Sprintf("x=%d", fmt.Get().x.Get())`},
		},
		{
			name: "a parameter named fmt",
			body: `func show(fmt string, n int) string = s"$fmt:$n"
func main() { Println(show("n", 3)) }`,
			contains: []string{`_gala_fmt.Sprintf("%s:%d", fmt, n)`, "\tfmt.Println(show("},
		},
		{
			name: "no binding named fmt",
			body: `func main() { Println(1) }`,
			contains: []string{"fmt.Println(1)"},
			absent:   []string{"_gala_fmt"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, "package main\n\n"+tc.body+"\n")
			require.NoError(t, err)
			for _, want := range tc.contains {
				require.Contains(t, out, want)
			}
			for _, not := range tc.absent {
				require.NotContains(t, out, not)
			}
		})
	}
}
