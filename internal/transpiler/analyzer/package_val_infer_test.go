package analyzer

import "testing"

// TestInferLiteralType: a quoted literal must span the whole initializer;
// an expression that merely starts and ends with quotes is not a literal.
func TestInferLiteralType(t *testing.T) {
	tests := []struct {
		expr, want string
	}{
		{`"x"`, "string"},
		{"`raw`", "string"},
		{`s"hi $name"`, "string"},
		{`f"${x}%.2f"`, "string"},
		{`s"${f("a")}"`, "string"},
		{`s"${m["k"]}"`, "string"},
		{`'a'`, "rune"},
		{`"a"=="b"`, ""},
		{`s"$X"==""`, ""},
		{`'a'=='b'`, ""},
		{`"a"+"b"`, ""},
		{`"\"quoted\""`, "string"},
		{"3", "int"},
		{"true", "bool"},
		{"x", ""},
	}
	for _, tc := range tests {
		if got := inferLiteralType(tc.expr); got != tc.want {
			t.Errorf("inferLiteralType(%s) = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// TestCallArgCount counts top-level arguments, skipping nested brackets and
// commas inside literals.
func TestCallArgCount(t *testing.T) {
	tests := []struct {
		args string
		want int
	}{
		{"()", 0},
		{"(10)", 1},
		{`("classic",Red())`, 2},
		{`(f(a,b),[1,2],"x,y")`, 3},
		{`(s"${g(1,2)}",'c')`, 2},
	}
	for _, tc := range tests {
		if got := callArgCount(tc.args); got != tc.want {
			t.Errorf("callArgCount(%s) = %d, want %d", tc.args, got, tc.want)
		}
	}
}
