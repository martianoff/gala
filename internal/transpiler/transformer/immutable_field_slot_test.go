package transformer_test

import "testing"

// A shorthand val field declared Immutable[T] is stored as that Immutable[T],
// not wrapped a second time, so it is the same field as one declared T: its
// value is a T, wrapped once, however it is supplied (positionally, by name,
// as a Copy override or as the field's default), and reading it yields a T.
// A var field declared Immutable[T] stores its value as is, so the value is
// the Immutable[T] itself.
func TestShorthandImmutableTypedField(t *testing.T) {
	const prelude = "package main\n\nstruct Box(X Immutable[int])\n\nstruct Wide(X Immutable[int64])\n\nstruct VBox(var X Immutable[int])\n\nstruct GBox[T any](X Immutable[T], Y T)\n\nstruct DBox(X Immutable[int64] = 5)\n\n"
	tests := []struct {
		name     string
		body     string
		contains []string
	}{
		{
			name:     "named argument",
			body:     "func main() { Println(Box(X = 6).X) }\n",
			contains: []string{"Box{X: std.NewImmutable(6)}"},
		},
		{
			name:     "positional argument",
			body:     "func main() { Println(Box(6).X) }\n",
			contains: []string{"Box{X: std.NewImmutable(6)}"},
		},
		{
			name:     "named argument converts an untyped constant to the wrapped type",
			body:     "func main() { Println(Wide(X = 6).X) }\n",
			contains: []string{"Wide{X: std.NewImmutable[int64](6)}"},
		},
		{
			name:     "positional argument converts an untyped constant to the wrapped type",
			body:     "func main() { Println(Wide(6).X) }\n",
			contains: []string{"Wide{X: std.NewImmutable[int64](6)}"},
		},
		{
			name:     "Copy override",
			body:     "func main() {\n    val b = Box(1)\n    Println(b.Copy(X = 7).X)\n}\n",
			contains: []string{"Box{X: std.NewImmutable(7)}"},
		},
		{
			name:     "field default",
			body:     "func main() { Println(DBox().X) }\n",
			contains: []string{"DBox{X: std.NewImmutable[int64](5)}"},
		},
		{
			name:     "var field, named argument",
			body:     "func main() { Println(VBox(X = 8).X) }\n",
			contains: []string{"VBox{X: std.NewImmutable[int](8)}"},
		},
		{
			name:     "var field, positional argument",
			body:     "func main() { Println(VBox(9).X) }\n",
			contains: []string{"VBox{X: std.NewImmutable[int](9)}"},
		},
		{
			name:     "generic struct, named arguments, explicit type argument",
			body:     "func main() { Println(GBox[int](X = 11, Y = 1).X) }\n",
			contains: []string{"GBox[int]{X: std.NewImmutable(11)"},
		},
		{
			name:     "generic struct, named arguments, inferred type argument",
			body:     "func main() { Println(GBox(X = 10, Y = 1).X) }\n",
			contains: []string{"GBox[int]{X: std.NewImmutable(10)"},
		},
		{
			name:     "reading the field yields the wrapped value",
			body:     "func main() {\n    val b = Box(1)\n    val n = b.X\n    Println(s\"${b.X} ${n + b.X}\")\n}\n",
			contains: []string{"std.NewImmutable(b.Get().X.Get())"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertTranspiled(t, prelude+tt.body, tt.contains, []string{"NewImmutable(std.NewImmutable", "NewImmutable[T]", ".Get().Get()"})
		})
	}
}

// A block form val field, or a shorthand field with an explicit `val`,
// declared Immutable[T] holds an Immutable[Immutable[T]]: its value is the
// Immutable[T], which the literal wraps once more.
func TestImmutableTypedFieldWrappedTwiceWhenDeclaredSo(t *testing.T) {
	const prelude = "package main\n\ntype Block struct {\n    X Immutable[int]\n}\n\nstruct Explicit(val X Immutable[int])\n\n"
	tests := []struct {
		name     string
		body     string
		contains []string
	}{
		{
			name:     "block form, positional argument",
			body:     "func main() { Println(Block(6).X.Get()) }\n",
			contains: []string{"Block{X: std.NewImmutable(std.NewImmutable[int](6))}"},
		},
		{
			name:     "block form, named argument",
			body:     "func main() { Println(Block(X = 6).X.Get()) }\n",
			contains: []string{"Block{X: std.NewImmutable(std.NewImmutable[int](6))}"},
		},
		{
			name:     "explicit val, named argument",
			body:     "func main() { Println(Explicit(X = 6).X.Get()) }\n",
			contains: []string{"Explicit{X: std.NewImmutable(std.NewImmutable[int](6))}"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertTranspiled(t, prelude+tt.body, tt.contains, nil)
		})
	}
}
