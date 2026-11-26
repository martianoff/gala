package gala2lean

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/coreexport"
)

// TestArgMarks checks which `_` of an argument a placeholder lambda of it
// takes, and in which order: those outside lambdas, case clauses and
// interpolated strings, by source position.
func TestArgMarks(t *testing.T) {
	mark := func(off int) *coreexport.Unsupported {
		return &coreexport.Unsupported{What: coreexport.UnsupportedPlaceholder, Pos: coreexport.Pos{Off: off}}
	}
	name := func(x string) coreexport.Node { return &coreexport.Name{Name: x} }
	lit := func() coreexport.Node { return &coreexport.Int{Text: "1"} }
	sel := func(x coreexport.Node, f string) coreexport.Node { return &coreexport.Select{Target: x, Name: f} }
	bin := func(op string, l, r coreexport.Node) coreexport.Node {
		return &coreexport.Binary{Op: op, Left: l, Right: r}
	}
	call := func(callee coreexport.Node, args ...coreexport.Node) *coreexport.Call {
		return &coreexport.Call{Callee: callee, Args: args}
	}
	m1, m2 := mark(1), mark(2)
	bracketed := call(name("f"), lit())
	bracketed.Bracket = &coreexport.Bracket{Exprs: []coreexport.Node{mark(3)}}
	tests := []struct {
		name string
		arg  coreexport.Node
		want []*coreexport.Unsupported
		ok   bool
	}{
		{"a field", sel(m1, "Lo"), []*coreexport.Unsupported{m1}, true},
		{"two, in source order whatever the tree order", bin("+", m2, m1), []*coreexport.Unsupported{m1, m2}, true},
		{"one in a nested call argument", call(name("inc"), m1), []*coreexport.Unsupported{m1}, true},
		{"one in an if expression", &coreexport.IfExpr{Cond: bin(">", sel(m1, "Lo"), lit()), Then: lit(), Else: lit()}, []*coreexport.Unsupported{m1}, true},
		{"a match subject, not its cases", &coreexport.Match{Subject: m1, Cases: []coreexport.Node{&coreexport.Case{Body: mark(5)}}}, []*coreexport.Unsupported{m1}, true},
		{"one in an interpolated string", &coreexport.Interp{Parts: []coreexport.Node{mark(4)}}, nil, true},
		{"one in a nested lambda", &coreexport.Lambda{Body: mark(4)}, nil, true},
		{"one between brackets", bracketed, nil, false},
		{"none", name("x"), nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := argMarks(tt.arg)
			require.Equal(t, tt.ok, ok)
			if tt.ok {
				require.Equal(t, tt.want, got)
			}
		})
	}
}
