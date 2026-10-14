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
				"// Use NewLight for the default value (On with Power 30 and Room \"Living\"), or a case constructor: NewLightOff, NewLightOn. A composite literal Light{} does not apply GALA's field defaults: its fields hold Go's zero values.",
				"// NewLight returns Light's default value: On with Power 30 and Room \"Living\", what GALA uses where none is given.",
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
		{
			name: "two sealed types generating one name",
			src: `package main

sealed type Shape {
    case Circle(R int)
}

sealed type ShapeCircle {
    default case Dot()
}

func main() { Println(Circle(1), Dot()) }`,
			contains: []string{"func NewShapeCircleDot() ShapeCircle {"},
			absent:   []string{"func NewShapeCircle("},
		},
		{
			name: "an option name the package declares",
			src: `package main

sealed type Light {
    default case On(Power int = 30)
}

func WithLightOnPower() int = 1

func main() { Println(On(), WithLightOnPower()) }`,
			contains: []string{"// Use NewLight for the default value (On with Power 30).", "func NewLight() Light {"},
			absent:   []string{"func NewLightOn(", "LightOnOption"},
		},
		{
			name: "a generic type",
			src: `package main

sealed type Box[T any] {
    case Full(Value T)
    case Empty()
}

func main() { Println(Full(1)) }`,
			contains: []string{
				"Its zero value Box[T]{} is none of its cases.",
				"func NewBoxFull[T any](value T) Box[T] {",
			},
		},
		{
			name: "a generic default inferred from the field type",
			src: `package main

sealed type Box[T any] {
    case Full(Value T, Extra Option[T] = None(), Count int = 1)
    default case Empty()
}

func main() { Println(Full(1)) }`,
			contains: []string{
				"func NewBoxFull[T any](value T, _opts ...BoxFullOption[T]) Box[T] {",
				"func WithBoxFullCount[T any](v int) BoxFullOption[T] {",
			},
		},
		{
			name: "an unexported default case",
			src: `package main

sealed type Mode {
    case On()
    default case off()
}

func main() { Println(On()) }`,
			contains: []string{
				"// Use NewMode for the default value, or a case constructor: NewModeOn.",
				"// NewMode returns Mode's default value, what GALA uses where none is given.",
			},
		},
		{
			name: "a field named like a function a default calls",
			src: `package main

func width() int = 10

sealed type Shape {
    case Rect(Width int, Height int = width())
}

func main() { Println(Rect(1)) }`,
			contains: []string{
				"func NewShapeRect(width_ int, _opts ...ShapeRectOption) Shape {",
				"_args := shapeRectArgs{Height: width()}",
				"return Rect{}.Apply(width_, _args.Height)",
			},
		},
		{
			name: "a field named like an import",
			src: `package main

import "strings"

sealed type Name {
    case Of(Strings string)
}

func main() { Println(Of(strings.ToUpper("a"))) }`,
			contains: []string{"func NewNameOf(strings_ string) Name {"},
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
