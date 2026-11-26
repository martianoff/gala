package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBranchConstantsTakeSlotType pins #1342: an if-expression or match whose
// branch values are untyped constants, filling a slot of a basic type other
// than the constants' default (`int64`, `float64`), has the slot's type, as a
// Go assignment converts a constant. It was an `int` IIFE, which Go rejects.
func TestBranchConstantsTakeSlotType(t *testing.T) {
	trans := newDefaultsTranspiler()
	cases := []struct {
		name, input string
		contains    []string
	}{
		{"if-expression in an int64 val", `package main

func main() {
    val c = true
    val a int64 = if (c) 1 << 40 else 0
    Println(a)
}`, []string{"func() int64 {"}},
		{"if-expression as a float64 result", `package main

func g(c bool) float64 = if (c) 1 else 2

func main() {
    Println(g(true))
}`, []string{"func g(c bool) float64 {", "return 2\n"}},
		{"match in an int64 val", `package main

func main() {
    val c = true
    val a int64 = c match {
        case true => 1
        case _ => 0
    }
    Println(a)
}`, []string{"func() int64 {"}},
		{"match as a float64 result", `package main

func h(c bool) float64 = c match {
    case true => 1
    case _ => 2.5
}

func main() {
    Println(h(true))
}`, []string{"func h(c bool) float64 {", "return 2.5"}},
		{"if-expression in a Go named scalar val", `package main

import "time"

func main() {
    val c = true
    val d time.Duration = if (c) 2 else 3
    Println(d)
}`, []string{"func() time.Duration {"}},
		{"if-expression as an int64 lambda result", `package main

func widen(f func(int) int64) func(int) int64 = f

func main() {
    val w = widen((k int) => if (k > 0) 1 << 40 else 0)
    Println(w(1))
}`, []string{"func(k int) int64 {", "return 1 << 40"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "branch_const_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
			assert.NotContains(t, out, "func() int {", "a constant branch kept its default type")
		})
	}
}
