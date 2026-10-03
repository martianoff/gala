package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler/transformer"
)

// TestMatchArmBlockLocalIsTyped pins that a value-carrying block — a match
// arm, a branch of an arm's trailing if/else, a partial-function arm — whose
// value is a local the block declares takes that local's type. The value used
// to be typed after the block's scope had closed, so the local no longer
// resolved: it was counted as unresolved, and when no other arm or expected
// type supplied one, the match was lowered as void and its value used — Go
// rejected `func(obj int) {…}(n) (no value) used as value` — and a
// partial-function arm became `Option[void]` wrapping `Some[any]`.
func TestMatchArmBlockLocalIsTyped(t *testing.T) {
	t.Setenv("GALA_WARN_TYPES", "1")

	tests := []struct {
		name string
		src  string
		want string // a fragment of the generated Go
	}{
		{"only arm reads a val", `package main

func f(n int) {
    val r = n match {
        case x => {
            val tag = s"item-$x"
            tag
        }
    }
    Println(r.Size())
}
`, "func(obj int) string {"},
		{"arm reads a var a loop accumulates", `package main

func sumTo(n int) int = n match {
    case 0 => 0
    case x => {
        var acc = 0
        for i := 1; i <= x; i++ {
            acc = acc + i
        }
        acc
    }
}
`, "return acc"},
		{"default arm reads a val", `package main

func f(n int) {
    val r = n match {
        case 0 => 1.5
        case _ => {
            val half = 0.5
            half
        }
    }
    Println(r)
}
`, "func(obj int) float64 {"},
		{"trailing if reads a val", `package main

func f(n int) {
    val r = n match {
        case x => {
            val neg = "negative"
            if (x < 0) neg else "other"
        }
    }
    Println(r.Size())
}
`, "func(obj int) string {"},
		{"trailing if branches read their own vals", `package main

func f(n int) {
    val r = n match {
        case x => {
            if (x > 1) {
                val big = s"big$x"
                big
            } else {
                val small = "small"
                small
            }
        }
    }
    Println(r.Size())
}
`, "func(obj int) string {"},
		{"partial-function arm reads a val", `package main

import . "martianoff/gala/collection_immutable"

func f() {
    val ys = ArrayOf(1, 2, 3).Collect({
        case x if x > 1 => {
            val label = s"n$x"
            label
        }
    })
    Println(ys)
}
`, "std.Option[string]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trans, tr := newTranspilerWithTransformer()
			out, err := trans.Transpile(tt.src, "main.gala")
			require.NoError(t, err)
			assert.Contains(t, out, tt.want)
			assert.Empty(t, transformer.UnresolvedTypes(tr), "unresolved expressions")
		})
	}
}
