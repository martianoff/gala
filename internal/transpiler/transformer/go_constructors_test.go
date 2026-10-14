package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSealedGoConstructors pins the constructors an exported sealed type gets
// for Go callers, and the doc comment naming them: NewT for its default value,
// NewT<Case> per exported case with options for the defaulted fields, none
// for an unexported type, and none under a name the package already declares.
func TestSealedGoConstructors(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		contains []string
		absent   []string
	}{
		{
			name: "a default case with field defaults",
			src: `package main

sealed type Light {
    case Off()
    default case On(Power int = 30, Room string = "Living")
}

func main() { Println(Off()) }`,
			contains: []string{
				"// Create a Light with NewLight, its default value (On with Power 30 and Room \"Living\"), or with a case constructor: NewLightOff, NewLightOn.",
				"func NewLight() Light {\n\treturn On{}.Apply(30, \"Living\")",
				"func NewLightOff() Light {\n\treturn Off{}.Apply()",
				"func NewLightOn(_opts ...LightOnOption) Light {",
				"_args := lightOnArgs{Power: 30, Room: \"Living\"}",
				"func WithLightOnPower(v int) LightOnOption {",
				"// WithLightOnRoom sets On's Room (default \"Living\").",
			},
		},
		{
			name: "no default case, required fields",
			src: `package main

sealed type Shape {
    case Circle(Radius int, Type string = "round")
    case Square(Side int)
}

func main() { Println(Square(1)) }`,
			contains: []string{
				"// Shape has no default value: create it with a case constructor, NewShapeCircle, NewShapeSquare. Its zero value Shape{} is none of its cases.",
				"func NewShapeSquare(side int) Shape {",
				"func NewShapeCircle(radius int, _opts ...ShapeCircleOption) Shape {",
			},
			absent: []string{"func NewShape() Shape"},
		},
		{
			name: "an unexported type",
			src: `package main

sealed type mode {
    case On()
    default case Off()
}

func main() { Println(On()) }`,
			absent: []string{"func NewMode", "func Newmode"},
		},
		{
			name: "a name the package declares",
			src: `package main

sealed type Light {
    case Off()
    default case On(Power int = 30)
}

func NewLight() Light = Off()

func main() { Println(NewLight()) }`,
			contains: []string{"func NewLightOn("},
			absent:   []string{"return On{}.Apply(30)\n}\n\n//line"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, tc.src)
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
