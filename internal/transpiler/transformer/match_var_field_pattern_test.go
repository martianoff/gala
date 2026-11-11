package transformer_test

import "testing"

// A struct pattern reads each field directly off the matched value. A `var`
// field is a plain Go field, so it must be read as is; only a non-`var` field
// is a std.Immutable[T] and gets `.Get()`. Unwrapping a `var` field emits
// `_subj.X.Get()` on an int, which Go rejects.
func TestStructPatternVarFieldsAreNotUnwrapped(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		contains   []string
		notContain []string
	}{
		{
			name: "all var fields",
			input: `package main

struct Point(var X int, var Y int)

func origin(p Point) string = p match {
    case Point(x, y) => s"$x $y"
    case _ => "none"
}
`,
			contains:   []string{"x := _subj.X", "y := _subj.Y"},
			notContain: []string{"_subj.X.Get()", "_subj.Y.Get()"},
		},
		{
			name: "mixed var and immutable fields",
			input: `package main

struct Labeled(var Name string, Weight int)

func show(l Labeled) string = l match {
    case Labeled(n, w) => s"$n $w"
    case _ => "none"
}
`,
			contains:   []string{"n := _subj.Name", "w := _subj.Weight.Get()"},
			notContain: []string{"_subj.Name.Get()"},
		},
		{
			name: "literal sub-pattern and guard on var fields",
			input: `package main

struct Point(var X int, var Y int)

func axis(p Point) string = p match {
    case Point(0, y) => s"y $y"
    case Point(x, y) if x == y => s"diag $x"
    case _ => "none"
}
`,
			contains:   []string{"y := _subj.Y", "x := _subj.X"},
			notContain: []string{"_subj.X.Get()", "_subj.Y.Get()"},
		},
		{
			name: "brace-body struct declaration",
			input: `package main

type Cell struct {
    var Row int
    Label string
}

func show(c Cell) string = c match {
    case Cell(r, l) => s"$l $r"
    case _ => "none"
}
`,
			contains:   []string{"r := _subj.Row", "l := _subj.Label.Get()"},
			notContain: []string{"_subj.Row.Get()"},
		},
		{
			name: "nested pattern through a var field",
			input: `package main

struct Point(var X int, var Y int)
struct Segment(From Point, var To Point)

func ends(s Segment) string = s match {
    case Segment(Point(a, b), Point(c, d)) => s"$a $b $c $d"
    case _ => "none"
}
`,
			contains:   []string{"_subj.From.Get()"},
			notContain: []string{"_subj.To.Get()", ".X.Get()", ".Y.Get()"},
		},
		{
			name: "generic struct with a var field",
			input: `package main

struct Box[T any](var Item T, Tag string)

func show[T any](b Box[T]) string = b match {
    case Box(item, tag) => s"$tag $item"
    case _ => "none"
}
`,
			contains:   []string{"item := _subj.Item", "tag := _subj.Tag.Get()"},
			notContain: []string{"_subj.Item.Get()"},
		},
		{
			name: "any subject reads var fields off the asserted value",
			input: `package main

struct Point(var X int, var Y int)

func typed(v any) string = v match {
    case Point(x: int, y) => s"$x $y"
    case _ => "none"
}
`,
			contains:   []string{"std.As[Point](_subj)", "std.As[int](", ".X)", ".Y\n"},
			notContain: []string{".X.Get()", ".Y.Get()"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertTranspiled(t, tt.input, tt.contains, tt.notContain)
		})
	}
}
