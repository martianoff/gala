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
	runGoCtorCases(t, []goCtorCase{
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
	})
}

// TestStructGoConstructors pins the constructor an exported shorthand struct
// gets for Go callers: its fields without a default as parameters, those with
// one as options over it, each immutable field wrapped. A struct with a field
// Go cannot set, an unexported struct, a block-form struct and a name the
// package declares get none.
func TestStructGoConstructors(t *testing.T) {
	runGoCtorCases(t, []goCtorCase{
		{
			name: "required fields and defaults",
			src: `package main

// Item is a cart line.
struct Item(SKU string, Qty int = 1, var Note string = "n")

func main() { Println(Item("a")) }`,
			contains: []string{
				"// Item is a cart line.\n// From Go, create it with NewItem. A composite literal Item{} does not apply GALA's field defaults: its fields hold Go's zero values.\ntype Item struct",
				"// NewItem returns a new Item, each option applied over its field defaults (Qty 1, Note \"n\").",
				"func NewItem(sku string, _opts ...ItemOption) Item {",
				"_args := itemArgs{Qty: 1, Note: \"n\"}",
				"return Item{SKU: std.NewImmutable(sku), Qty: std.NewImmutable(_args.Qty), Note: _args.Note}",
				"// WithItemQty sets Item's Qty (default 1).\nfunc WithItemQty(v int) ItemOption {",
				"func WithItemNote(v string) ItemOption {",
			},
		},
		{
			name: "no defaults",
			src: `package main

struct LineItem(SKU string, UnitCents int64)

func main() { Println(LineItem("a", 1)) }`,
			contains: []string{
				"// From Go, create it with NewLineItem.\ntype LineItem struct",
				"func NewLineItem(sku string, unitCents int64) LineItem {\n\treturn LineItem{SKU: std.NewImmutable(sku), UnitCents: std.NewImmutable(unitCents)}",
			},
			absent: []string{"LineItemOption"},
		},
		{
			name: "defaults that are Go's zero values",
			src: `package main

struct Cfg(Name string, Tries int = 0, Note string = "")

func main() { Println(Cfg("a")) }`,
			contains: []string{"// From Go, create it with NewCfg.\ntype Cfg struct", "func NewCfg(name string, _opts ...CfgOption) Cfg {"},
			absent:   []string{"A composite literal Cfg{}"},
		},
		{
			name: "argument structs of initialisms stay distinct",
			src: `package main

struct HTTPClient(URL string = "a")

struct HttpClient(URL string = "b")

func main() { Println(HTTPClient(), HttpClient()) }`,
			contains: []string{"type hTTPClientArgs struct", "type httpClientArgs struct", "func NewHTTPClient(", "func NewHttpClient("},
		},
		{
			name: "a generic struct",
			src: `package main

struct Box[T any](Value T, Extra Option[T] = None())

func main() { Println(Box(1)) }`,
			contains: []string{
				"A composite literal Box[T]{} does not apply GALA's field defaults",
				"func NewBox[T any](value T, _opts ...BoxOption[T]) Box[T] {",
				"_args := boxArgs[T]{Extra: std.None[T]{}.Apply()}",
				"func WithBoxExtra[T any](v std.Option[T]) BoxOption[T] {",
			},
		},
		{
			name: "an unexported field",
			src: `package main

struct Mixed(A int, b int)

func main() { Println(Mixed(1, 2)) }`,
			absent: []string{"func NewMixed"},
		},
		{
			name: "an unexported struct",
			src: `package main

struct item(A int)

func main() { Println(item(1)) }`,
			absent: []string{"func Newitem", "func NewItem"},
		},
		{
			name: "a block-form struct",
			src: `package main

type Cfg struct {
    Name string
}

func main() { Println(Cfg(Name = "a")) }`,
			absent: []string{"func NewCfg"},
		},
		{
			name: "a name the package declares",
			src: `package main

struct LineItem(SKU string, Qty int)

func NewLineItem(sku string) LineItem = LineItem(sku, 1)

func main() { Println(NewLineItem("a")) }`,
			absent: []string{"From Go, create it with", "func NewLineItem(sku string, qty int)"},
		},
		{
			name: "a struct and a sealed case generating one name",
			src: `package main

struct ShapeCircle(R int)

sealed type Shape {
    case Circle(R int)
}

func main() { Println(ShapeCircle(1), Circle(1)) }`,
			absent: []string{"func NewShapeCircle("},
		},
	})
}

// TestSealedGoPredicates pins the exported predicates Go callers test a
// sealed value's case with: one per exported case of an exported type, none
// under a name the type already declares as a method or field.
func TestSealedGoPredicates(t *testing.T) {
	runGoCtorCases(t, []goCtorCase{
		{
			name: "each exported case",
			src: `package main

sealed type Shape {
    case Circle(R int)
    case Square(S int)
    case dot()
}

func main() { Println(Circle(1)) }`,
			contains: []string{
				"// IsCircle reports whether s is the Circle case of Shape.\nfunc (s Shape) IsCircle() bool {\n\treturn s._variant == _Shape_Circle",
				"func (s Shape) IsSquare() bool {",
				"func (s Shape) isdot() bool {",
			},
			absent: []string{"func (s Shape) Isdot()"},
		},
		{
			name: "a method the type declares",
			src: `package main

sealed type Res {
    case Ok(V int)
    case Bad(E string)
}

func (r Res) IsOk() bool = r.isOk()

func main() { Println(Ok(1).IsOk()) }`,
			contains: []string{"func (s Res) IsBad() bool {", "func (r Res) IsOk() bool {"},
			absent:   []string{"func (s Res) IsOk() bool {"},
		},
		{
			// The field is stored under a Go name of its case, so the
			// predicate keeps its name.
			name: "a field named like a predicate",
			src: `package main

sealed type Flag {
    case On(IsOff bool)
    case Off()
}

func main() { Println(On(true)) }`,
			contains: []string{"func (s Flag) IsOn() bool {", "func (s Flag) IsOff() bool {", "OnIsOff  std.Immutable[bool]"},
			absent:   []string{"\tIsOff "},
		},
		{
			name: "an unexported type",
			src: `package main

sealed type mode {
    case On()
    case Off()
}

func main() { Println(On()) }`,
			absent: []string{"func (s mode) IsOn()"},
		},
	})
}

// goCtorCase is a source whose generated Go contains each of contains and none
// of absent.
type goCtorCase struct {
	name     string
	src      string
	contains []string
	absent   []string
}

func runGoCtorCases(t *testing.T, cases []goCtorCase) {
	t.Helper()
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
