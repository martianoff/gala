package gala2lean

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/coreexport"
)

// TestSoleMark checks which placeholder arguments lowerPlaceholders admits:
// those the transformer rewrites into a lambda of one parameter whose
// renaming reaches the `_`.
func TestSoleMark(t *testing.T) {
	mark := func() coreexport.Node {
		return &coreexport.Unsupported{What: coreexport.UnsupportedPlaceholder}
	}
	name := func(x string) coreexport.Node { return &coreexport.Name{Name: x} }
	lit := func() coreexport.Node { return &coreexport.Int{Text: "1"} }
	sel := func(x coreexport.Node, f string) coreexport.Node { return &coreexport.Select{Target: x, Name: f} }
	call := func(callee coreexport.Node, args ...coreexport.Node) *coreexport.Call {
		return &coreexport.Call{Callee: callee, Args: args}
	}
	bin := func(op string, l, r coreexport.Node) coreexport.Node {
		return &coreexport.Binary{Op: op, Left: l, Right: r}
	}
	bracketed := call(sel(mark(), "Map"), name("f"))
	bracketed.Bracket = &coreexport.Bracket{Types: []coreexport.Node{&coreexport.TypeName{Name: "_"}}}
	tests := []struct {
		name  string
		arg   coreexport.Node
		admit bool
	}{
		{"a field", sel(mark(), "Lo"), true},
		{"a comparison", bin("==", sel(mark(), "Name"), name("n")), true},
		{"a method call with a literal argument", call(sel(mark(), "Contains"), lit()), true},
		{"an interpolated string beside the `_`", bin("+", mark(), &coreexport.Interp{Parts: []coreexport.Node{name("x")}}), true},
		{"two `_`", bin("+", mark(), mark()), false},
		{"no `_`", name("x"), false},
		{"a `_` in a call argument", call(name("inc"), mark()), false},
		{"a `_` in an interpolated string", &coreexport.Interp{Parts: []coreexport.Node{mark()}}, false},
		{"a method call with a computed argument", call(sel(mark(), "Contains"), bin("+", lit(), lit())), false},
		{"a call with type arguments", bracketed, false},
		{"an if expression", &coreexport.IfExpr{Cond: bin(">", sel(mark(), "Lo"), lit()), Then: lit(), Else: lit()}, false},
		{"a match", &coreexport.Match{Subject: mark()}, false},
		{"a lambda", &coreexport.Lambda{Body: mark()}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.admit, soleMark(tt.arg) != nil)
		})
	}
}
