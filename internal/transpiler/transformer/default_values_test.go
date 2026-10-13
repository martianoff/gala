package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestDefaultValues pins that GALA code never creates an implicit zero value:
// an uninitialized var and a field a construction leaves out take the type's
// default value (a sealed type's default case with its field defaults, a
// struct's declared field defaults), Go's zero value where that is the
// default, and GALA-E0075 for a type with none.
func TestDefaultValues(t *testing.T) {
	const decls = `package main

sealed type Light {
    case Off()
    default case On(Power int = 30, Room string = "Living")
}

sealed type Bit {
    case One()
    case Zero()
}

struct Home(Name string, L Light, Rooms int = 1)

struct Job(Name string, Result Try[int])

type Point struct {
    X int
    Y int
}
`
	cases := []struct {
		name      string
		body      string
		contains  []string
		absent    []string
		wantError string
	}{
		{
			name:     "an uninitialized var of a sealed type takes its default case",
			body:     `func main() { var l Light; Println(l) }`,
			contains: []string{`var l Light = On{}.Apply(30, "Living")`},
		},
		{
			name:     "an omitted field takes its declared default and its type's default value",
			body:     `func main() { Println(Home{Name: "flat"}) }`,
			contains: []string{`L: std.NewImmutable(On{}.Apply(30, "Living"))`, `Rooms: std.NewImmutable(1)`},
		},
		{
			name:   "a type whose default is Go's zero value is left as it is",
			body:   `func main() { var o Option[int]; var p Point; Println(o, p, Point{X: 1}) }`,
			absent: []string{"var o std.Option[int] =", "var p Point =", "Point{X: std.NewImmutable(1), Y"},
		},
		{
			name: "a zero literal default on a non-basic field is lowered as written",
			body: `struct Holder(V any = 0)
func main() { Println(Holder{}) }`,
			contains: []string{"Holder{V: std.NewImmutable[any](0)}"},
		},
		{
			name:      "an uninitialized var of a sealed type without a default case",
			body:      `func main() { var b Bit; Println(b) }`,
			wantError: "Bit has no default value: it is a sealed type without a default case",
		},
		{
			name:      "an uninitialized var of Try",
			body:      `func main() { var t Try[int]; Println(t) }`,
			wantError: "Try has no default value",
		},
		{
			name:      "a field without a default value left out of a literal",
			body:      `func main() { Println(Job{Name: "x"}) }`,
			wantError: `field "Result" of "Job" is left out, and Try has no default value`,
		},
		{
			name:      "an uninitialized var of a struct holding one",
			body:      `func main() { var j Job; Println(j) }`,
			wantError: `field "Result" of "Job" is left out, and Try has no default value`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, decls+tc.body)
			if tc.wantError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), string(galaerr.CodeNoDefaultValue))
				require.Contains(t, err.Error(), tc.wantError)
				return
			}
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
