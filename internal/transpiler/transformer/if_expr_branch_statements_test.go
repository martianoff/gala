package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The statements before the value of an if-expression's block branch, and
// those of a `bind` continuation, are lowered as in any other block: a match
// there is a statement-position match, an if-expression an if statement, and
// a plain value is evaluated but not used.
func TestBlockStatementsBeforeTheValue(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"match with a return in an if-expression branch",
			"val v = if (c) {\n        n match {\n            case 1 => { return 7 }\n            case _ => Println(\"other\")\n        }\n        5\n    } else 2\n    v",
			"return 7"},
		{"discarded if-expression in an if-expression branch",
			"val v = if (c) {\n        if (n > 1) Println(\"big\") else Println(\"small\")\n        n * 10\n    } else 0\n    v",
			`"small"`},
		{"match with a value arm and a void arm in a bind continuation",
			"val o = Some(n).FlatMap((m) => {\n        bind x = Some(m)\n        x match {\n            case 1 => note(x)\n            case _ => Println(\"other\")\n        }\n        Some(x)\n    })\n    o.GetOrElse(0)",
			"note(x)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\nfunc note(k int) int = k\n\nfunc run(c bool, n int) int {\n    " + tc.body + "\n}\n"
			assertTranspiled(t, src, []string{tc.want}, nil)
		})
	}
}

// An unused value before an if-expression branch's value is a GALA error, not
// a Go one.
func TestUnusedValueInIfExpressionBranchIsRejected(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"if-expression branch", "val v = if (c) { n + 1; 5 } else 2\n    v"},
		{"bind continuation", "val o = Some(n).FlatMap((m) => {\n        bind x = Some(m)\n        n + 1\n        Some(x)\n    })\n    o.GetOrElse(0)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newBindTranspiler().Transpile("package main\n\nfunc run(c bool, n int) int {\n    "+tc.body+"\n}\n", "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "`n+1` is evaluated but not used")
		})
	}
}
