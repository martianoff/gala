package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFmtShadowedByBinding pins the code GALA lowers to Go's fmt (Println,
// Print, s"..." interpolation, a sealed type's String) where the name fmt is
// taken — by a binding, a type parameter or a function — or fmt is imported
// under another name: it calls fmt under a name that reaches the package.
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
			contains: []string{`_gala_fmt "fmt"`, "_gala_fmt.Print(fmt.x.Get())", `_gala_fmt.Sprintf("x=%d", fmt.x.Get())`},
		},
		{
			name: "a parameter named fmt",
			body: `func show(fmt string, n int) string = s"$fmt:$n"
func main() { Println(show("n", 3)) }`,
			contains: []string{`_gala_fmt.Sprintf("%s:%d", fmt, n)`, "\tfmt.Println(show("},
		},
		{
			name: "a sealed type's String beside a package-level val named fmt",
			body: `val fmt = "%d"
sealed type Shape {
    case Circle(R int)
}
func main() { Println(Circle(2), fmt) }`,
			contains: []string{`_gala_fmt.Sprintf("Circle(%v)"`},
		},
		{
			name: "a function named fmt",
			body: `func fmt() int = 1
func main() { Println(fmt()) }`,
			contains: []string{"_gala_fmt.Println(fmt())"},
		},
		{
			name:     "a type parameter named fmt",
			body:     "func show[fmt any](x fmt) string = s\"v=$x\"\nfunc main() { Println(show(2)) }",
			contains: []string{`_gala_fmt.Sprintf("v=%v", x)`},
		},
		{
			name:     "fmt imported under another name",
			body:     "import f \"fmt\"\n\nfunc main() { Println(f.Sprint(1)) }",
			contains: []string{"f.Println(f.Sprint(1))"},
			absent:   []string{"_gala_fmt"},
		},
		{
			name:     "no binding named fmt",
			body:     `func main() { Println(1) }`,
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
